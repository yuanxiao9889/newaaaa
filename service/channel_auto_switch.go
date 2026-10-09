package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const autoSwitchContextKey = "channel_auto_switch_attempt"

var autoSwitchLastPrune atomic.Int64

func autoSwitchProtocol(c *gin.Context) string {
	if c.Request == nil {
		return ""
	}
	// A model can support several independent protocols. Never let an Images
	// outage pause its chat endpoint, or persist a path containing task IDs.
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/") {
		if i := strings.LastIndex(path, ":"); i >= 0 {
			return "gemini:" + path[i+1:]
		}
	}
	if strings.HasPrefix(path, "/v1/videos/") {
		if _, action, found := strings.Cut(strings.TrimPrefix(path, "/v1/videos/"), "/"); found {
			return "/v1/videos/:id/" + action
		}
	}
	if route := c.FullPath(); route != "" {
		path = route
	}
	return path
}

// selectAutoSwitchChannel keeps ordinary channels in their existing priority
// slots. When an opted-in slot wins, its compatible pool supplies a healthy
// member; the pool therefore retains its manual place while using a backup.
func selectAutoSwitchChannel(c *gin.Context, group, modelName string, retry int, filters []dto.ChannelFilter) (*model.Channel, error) {
	base, err := model.GetRandomSatisfiedChannel(group, modelName, retry, filters)
	if err != nil || base == nil || !base.GetOtherSettings().AutoSwitchEnabled || base.Type == constant.ChannelTypeMidjourney || base.Type == constant.ChannelTypeMidjourneyPlus {
		return base, err
	}
	if _, pinned, _ := GetChannelConstraints(c).ResolvedPin(); pinned || RequestPolicy(c).SessionMode == "strict" {
		return base, nil
	}
	// A WebSocket carries several independent generations. Until they have
	// durable per-generation completion tracking, its handshake is not a probe.
	for _, filter := range filters {
		if filter.Kind == dto.FilterResponsesWebSocket {
			return base, nil
		}
	}
	if c.Request != nil && c.Request.URL.Path == "/v1/responses" {
		if _, taskProtocol := c.Get(jsplugin.ContextKeyPinnedEndpoint); !taskProtocol {
			var request struct {
				Background bool `json:"background"`
			}
			if common.UnmarshalBodyReusable(c, &request) != nil || request.Background {
				return base, nil
			}
		}
	}
	// Exclusions describe attempts already made by this request, not membership
	// of the configured pool. Counting only unused members disables failover
	// precisely when a two-channel pool has just suffered its first failure.
	membershipFilters := slices.Clone(filters)
	for i := range membershipFilters {
		membershipFilters[i].ExcludedChannelIDs = nil
	}
	candidates, err := model.GetSatisfiedChannels(group, modelName, membershipFilters)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(candidates))
	pool := make([]*model.Channel, 0, len(candidates))
	for _, ch := range candidates {
		if ch.GetOtherSettings().AutoSwitchEnabled && ch.Type != constant.ChannelTypeMidjourney && ch.Type != constant.ChannelTypeMidjourneyPlus {
			ids = append(ids, ch.Id)
			pool = append(pool, ch)
		}
	}
	protocol := autoSwitchProtocol(c)
	states, err := model.GetChannelAutoSwitchStates(ids, group, modelName, protocol)
	if err != nil {
		return nil, err
	}
	if len(pool) < 2 {
		configured, err := model.GetConfiguredChannelsForModel(group, modelName, membershipFilters)
		if err != nil {
			return nil, err
		}
		participants := 0
		for _, ch := range configured {
			if ch.GetOtherSettings().AutoSwitchEnabled && ch.Type != constant.ChannelTypeMidjourney && ch.Type != constant.ChannelTypeMidjourneyPlus {
				participants++
			}
		}
		if participants < 2 {
			return base, nil
		}
	}
	slices.SortFunc(pool, func(a, b *model.Channel) int {
		if a.GetPriority() > b.GetPriority() {
			return -1
		}
		if a.GetPriority() < b.GetPriority() {
			return 1
		}
		return a.Id - b.Id
	})
	used := c.GetStringSlice("use_channel")
	now := time.Now()
	// Probe a recovered member even while the healthy backup continues to
	// serve traffic. The durable Begin call owns the single probe globally.
	for _, probePass := range []bool{true, false} {
		eligible := make([]*model.Channel, 0, len(pool))
		for _, ch := range pool {
			if slices.Contains(used, strconv.Itoa(ch.Id)) {
				continue
			}
			if ok, _ := model.ChannelSatisfiesFilters(ch, modelName, filters); !ok {
				continue
			}
			state, exists := states[ch.Id]
			if probePass {
				if !exists || !state.CanProbe(now) {
					continue
				}
			} else if exists && state.Status != model.ChannelAutoSwitchHealthy {
				continue
			}
			eligible = append(eligible, ch)
		}
		for len(eligible) > 0 {
			ch := eligible[0]
			if !probePass {
				// Reuse ordinary weighted selection within the highest healthy
				// priority instead of making equal-priority pools stick to one ID.
				excluded := make([]int, 0, len(candidates))
				for _, candidate := range candidates {
					if !slices.ContainsFunc(eligible, func(item *model.Channel) bool { return item.Id == candidate.Id }) {
						excluded = append(excluded, candidate.Id)
					}
				}
				healthyFilters := append(slices.Clone(filters), dto.ChannelFilter{Kind: dto.FilterChannelIdentity, ExcludedChannelIDs: excluded})
				ch, err = model.GetRandomSatisfiedChannel(group, modelName, 0, healthyFilters)
				if err != nil {
					return nil, err
				}
				if ch == nil {
					break
				}
				if !slices.ContainsFunc(eligible, func(item *model.Channel) bool { return item.Id == ch.Id }) {
					break // A normalized-model fallback is outside this admitted pool.
				}
			}
			eligible = slices.DeleteFunc(eligible, func(item *model.Channel) bool { return item.Id == ch.Id })
			ticket, err := model.BeginChannelAutoSwitchAttempt(ch.Id, group, modelName, protocol, now)
			if err != nil {
				return nil, err
			}
			if ticket == nil {
				continue
			}
			ReleaseAutoSwitchAttempt(c)
			c.Set("auto_switch_task_dispatched", false)
			c.Set("auto_switch_task_rejected", false)
			c.Set(autoSwitchContextKey, ticket)
			c.Set("auto_switch_managed_channel", ch.Id)
			RequestPolicy(c).AddEvent(PolicyEvent{ChannelID: ch.Id, Group: group,
				Decision: PolicyDecision{Action: "attempt", Reason: "automatic_channel_selection", Source: "channel_health"},
				Health:   map[bool]string{true: "recovery_probe", false: "healthy"}[ticket.Probe]})
			return ch, nil
		}
	}
	// A depleted automatic pool must not resurrect its cooling members. Other
	// manually configured channels may still be used by ordinary routing.
	excluded := append([]int(nil), ids...)
	for _, id := range used {
		if n, err := strconv.Atoi(id); err == nil {
			excluded = append(excluded, n)
		}
	}
	fallbackFilters := append(slices.Clone(filters), dto.ChannelFilter{Kind: dto.FilterChannelIdentity, ExcludedChannelIDs: excluded})
	return model.GetRandomSatisfiedChannel(group, modelName, 0, fallbackFilters)
}

// ShouldDisableChannelForRequest lets the scoped circuit handle transient
// outages while retaining the administrator's hard-disable authentication rules.
func ShouldDisableChannelForRequest(c *gin.Context, channelID int, apiErr *types.NewAPIError) bool {
	if c != nil && apiErr != nil && c.GetInt("auto_switch_managed_channel") == channelID {
		_, reason := classifyAutoSwitchFailure(apiErr.StatusCode, string(apiErr.GetErrorCode()), apiErr.Error())
		if reason == "upstream_capacity" || reason == "upstream_unavailable" {
			return false
		}
	}
	return ShouldDisableChannel(apiErr)
}

func AutoSwitchAttempt(c *gin.Context) *model.ChannelAutoSwitchTicket {
	if c == nil {
		return nil
	}
	value, _ := c.Get(autoSwitchContextKey)
	ticket, _ := value.(*model.ChannelAutoSwitchTicket)
	return ticket
}

// DetachAutoSwitchAttempt transfers completion ownership to a persisted task.
func DetachAutoSwitchAttempt(c *gin.Context) {
	c.Set(autoSwitchContextKey, (*model.ChannelAutoSwitchTicket)(nil))
}

// ReleaseAutoSwitchAttempt is only for requests rejected before an upstream
// attempt, including validation or billing failures after channel selection.
func ReleaseAutoSwitchAttempt(c *gin.Context) {
	ticket := AutoSwitchAttempt(c)
	if ticket == nil {
		return
	}
	DetachAutoSwitchAttempt(c)
	// Deferred cleanup also runs on panic or cancellation. Once dispatch began,
	// an unconfirmed task may still be running even without a parsed receipt.
	if c.GetBool("auto_switch_task_dispatched") && !c.GetBool("auto_switch_task_rejected") {
		return
	}
	if err := model.ReleaseChannelAutoSwitchAttempt(*ticket, time.Now()); err != nil {
		logger.LogError(c, "release automatic channel selection: "+err.Error())
	}
}

func recordAutoSwitchResult(ctx context.Context, ticket *model.ChannelAutoSwitchTicket, outcome model.ChannelAutoSwitchOutcome, reason string) {
	if ticket == nil {
		return
	}
	if err := model.RecordChannelAutoSwitchResult(*ticket, outcome, reason, time.Now()); err != nil {
		logger.LogError(ctx, "record automatic channel outcome: "+err.Error())
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("channel automatic switching: channel=%d model=%s probe=%t outcome=%s reason=%s", ticket.ChannelID, ticket.Model, ticket.Probe, outcome, reason))
	now := time.Now().Unix()
	previous := autoSwitchLastPrune.Load()
	if now-previous >= 3600 && autoSwitchLastPrune.CompareAndSwap(previous, now) {
		if err := model.PruneChannelAutoSwitchAttempts(time.Unix(now, 0).Add(-48 * time.Hour)); err != nil {
			logger.LogError(ctx, "prune automatic channel observations: "+err.Error())
		}
	}
}

func RecordAutoSwitchRequestResult(c *gin.Context, success bool, apiErr *types.NewAPIError) {
	ticket := AutoSwitchAttempt(c)
	if ticket == nil {
		return
	}
	DetachAutoSwitchAttempt(c)
	outcome, reason := model.ChannelAutoSwitchNeutral, "unconfirmed_result"
	if success {
		outcome, reason = model.ChannelAutoSwitchSuccess, "completed"
	} else if apiErr != nil {
		outcome, reason = classifyAutoSwitchFailure(apiErr.StatusCode, string(apiErr.GetErrorCode()), apiErr.Error())
		if apiErr.GetErrorType() == types.ErrorTypeNewAPIError && apiErr.GetErrorCode() != types.ErrorCodeDoRequestFailed {
			outcome, reason = model.ChannelAutoSwitchNeutral, "local_rejection"
		}
	}
	if c.Request != nil && c.Request.Context().Err() != nil && !success {
		outcome, reason = model.ChannelAutoSwitchNeutral, "request_cancelled"
	}
	recordAutoSwitchResult(c, ticket, outcome, reason)
}

// RecordAutoSwitchTaskSubmissionFailure distinguishes a local rejection before
// dispatch from a local parsing error after the provider may have accepted work.
// An unconfirmed probe keeps its durable lease until expiry; neither a deferred
// cleanup nor another request may immediately submit a replacement probe.
func RecordAutoSwitchTaskSubmissionFailure(c *gin.Context, taskErr *dto.TaskError) {
	ticket := AutoSwitchAttempt(c)
	if ticket == nil || taskErr == nil {
		return
	}
	if !c.GetBool("auto_switch_task_dispatched") {
		ReleaseAutoSwitchAttempt(c)
		return
	}
	if !c.GetBool("auto_switch_task_rejected") {
		if ticket.Probe || taskErr.NoRetry {
			DetachAutoSwitchAttempt(c)
			return
		}
		if taskErr.Code != "do_request_failed" {
			RecordAutoSwitchRequestResult(c, false, nil)
			return
		}
	}
	err := taskErr.Error
	if err == nil {
		err = errors.New(taskErr.Message)
	}
	RecordAutoSwitchRequestResult(c, false, types.NewOpenAIError(err, types.ErrorCode(taskErr.Code), taskErr.StatusCode))
}

// RecordAutoSwitchTaskResult observes the upstream attempt, independently from
// retry eligibility and billing. A failure arriving minutes after submission
// still protects future requests. The ticket makes repeat polls idempotent.
func RecordAutoSwitchTaskResult(ctx context.Context, task *model.Task, result *relaycommon.TaskInfo) {
	if task == nil || task.PrivateData.AutoSwitch == nil || result == nil {
		return
	}
	if result.Status == string(model.TaskStatusSuccess) {
		recordAutoSwitchResult(ctx, task.PrivateData.AutoSwitch, model.ChannelAutoSwitchSuccess, "completed")
	} else if result.Status == string(model.TaskStatusFailure) {
		outcome, reason := classifyAutoSwitchFailure(0, "", result.Reason)
		recordAutoSwitchResult(ctx, task.PrivateData.AutoSwitch, outcome, reason)
	}
}

func RecordAutoSwitchTaskFailure(ctx context.Context, task *model.Task, status int, code, message string) {
	if task == nil {
		return
	}
	outcome, reason := classifyAutoSwitchFailure(status, code, message)
	recordAutoSwitchResult(ctx, task.PrivateData.AutoSwitch, outcome, reason)
}

// Availability classification is deliberately conservative. Raw upstream text
// is used only for classification and never stored in the health/status API.
func classifyAutoSwitchFailure(status int, code, message string) (model.ChannelAutoSwitchOutcome, string) {
	description := strings.ToLower(code + " " + message)
	for _, ignored := range []string{"content.policy", "content_policy", "content_filter", "moderation", "safety", "nsfw", "违规", "审核", "敏感", "invalid_request", "invalid parameter", "参数", "insufficient_user_quota", "预扣费", "用户余额", "用户额度", "cancelled", "canceled"} {
		if strings.Contains(description, ignored) {
			return model.ChannelAutoSwitchNeutral, "request_rejected"
		}
	}
	if status == http.StatusTooManyRequests || strings.Contains(description, "video_busy") || strings.Contains(description, "rate_limit") {
		return model.ChannelAutoSwitchFailure, "upstream_capacity"
	}
	if status >= 500 && status <= 599 {
		return model.ChannelAutoSwitchFailure, "upstream_unavailable"
	}
	if status == 401 || status == 403 {
		return model.ChannelAutoSwitchFailure, "upstream_authentication"
	}
	for _, unavailable := range []string{"submission_confirmation_timeout", "upstream_poll_timeout", "service_unavailable", "service unavailable", "服务暂时不可用", "upstream error: 503", "overloaded", "connection reset", "connection refused", "context deadline exceeded", "do_request_failed", "poll failed: transport_error", "poll failed: transient"} {
		if strings.Contains(description, unavailable) {
			return model.ChannelAutoSwitchFailure, "upstream_unavailable"
		}
	}
	return model.ChannelAutoSwitchNeutral, "unclassified_failure"
}
