package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Keep replay data bounded and in the private task row so another polling node
// can resume without the submitting node's temporary multipart files.
const maxVideoTaskRetryBodyBytes = 8 << 20

func init() {
	service.RetryVideoTaskFunc = retryVideoTask
}

func captureVideoTaskRetry(c *gin.Context, info *relaycommon.RelayInfo) *model.VideoTaskRetryState {
	if common.RetryTimes <= 0 || info.TaskRelayInfo == nil || info.LockedChannel != nil || info.OriginTaskID != "" || len(info.OriginTasks) > 0 || service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return nil
	}
	constraints := service.GetChannelConstraints(c)
	if _, pinned, _ := constraints.ResolvedPin(); pinned {
		return nil
	}
	state := &model.VideoTaskRetryState{
		Method: c.Request.Method, Path: c.Request.URL.Path, Query: c.Request.URL.RawQuery,
		ContentType: c.Request.Header.Get("Content-Type"), UserGroup: info.UserGroup,
		Accept: c.Request.Header.Get("Accept"),
		Action: info.Action,
	}
	isVideo := state.Path == "/v1/videos" || state.Path == "/v1/video/generations"
	switch constant.NormalizeTaskAction(info.Action) {
	case constant.TaskActionTextToVideo, constant.TaskActionImageToVideo, constant.TaskActionFirstTailToVideo, constant.TaskActionReferenceToVideo:
		isVideo = true
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint)
		// Responses and Images have distinct rendering/lifecycle contracts.
		if !ok || pinned.Protocol != "openai_video" {
			return nil
		}
		isVideo = true
	}
	if !isVideo {
		return nil
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedRoute)
		if !ok || pinned.Plugin == nil {
			return nil
		}
		state.NativePluginKey, state.NativeRoutePath = pinned.Plugin.Meta.Key, pinned.Route.Path
	} else if key := c.Param("key"); key != "" {
		state.LegacyPluginKey = key
	}
	state.Params = make(map[string]string, len(c.Params))
	for _, param := range c.Params {
		state.Params[param.Key] = param.Value
	}
	// Deep-copy nested constraint slices; distribution mutates its own copy.
	encoded, err := common.Marshal(constraints)
	if err != nil || common.Unmarshal(encoded, &state.Constraints) != nil {
		return nil
	}
	body, err := common.GetBodyStorage(c)
	if err != nil || body.Size() > maxVideoTaskRetryBodyBytes {
		logger.LogWarn(c, "video early-failure retry unavailable: original request is unreadable or exceeds 8 MiB")
		return nil
	}
	data, err := body.Bytes()
	if err != nil || len(data) == 0 {
		return nil
	}
	state.Body = bytes.Clone(data)
	return state
}

func buildVideoTaskRetryContext(ctx context.Context, task *model.Task) (*gin.Context, *relaycommon.RelayInfo, error) {
	retry := task.PrivateData.VideoRetry
	target := retry.Path
	if retry.Query != "" {
		target += "?" + retry.Query
	}
	req, err := http.NewRequestWithContext(ctx, retry.Method, target, bytes.NewReader(retry.Body))
	if err != nil {
		return nil, nil, err
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	c.Request.Header.Set("Content-Type", retry.ContentType)
	c.Request.Header.Set("Accept", retry.Accept)
	c.Set(common.KeyRequestBody, retry.Body)
	c.Set("id", task.UserId)
	c.Set("token_id", task.PrivateData.TokenId)
	c.Set("token_name", task.TokenName)
	c.Set("group", task.Group)
	c.Set("user_group", retry.UserGroup)
	c.Set("task_action", retry.Action)
	c.Set("use_channel", append([]string(nil), task.PrivateData.ChannelRetryPath...))
	common.SetContextKey(c, constant.ContextKeyUserId, task.UserId)
	common.SetContextKey(c, constant.ContextKeyTokenId, task.PrivateData.TokenId)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, task.Group)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, task.Group)
	common.SetContextKey(c, constant.ContextKeyUserGroup, retry.UserGroup)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, task.Properties.OriginModelName)
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
	c.Set(common.RequestIdKey, task.TaskID)
	for key, value := range retry.Params {
		c.Params = append(c.Params, gin.Param{Key: key, Value: value})
	}
	constraintsJSON, err := common.Marshal(retry.Constraints)
	if err != nil {
		return c, nil, err
	}
	constraints := service.GetChannelConstraints(c)
	if err := common.Unmarshal(constraintsJSON, constraints); err != nil {
		return c, nil, err
	}
	identity := dto.ChannelFilter{Kind: dto.FilterChannelIdentity}
	for _, used := range task.PrivateData.ChannelRetryPath {
		id, err := strconv.Atoi(used)
		if err != nil {
			return c, nil, fmt.Errorf("invalid video retry channel history")
		}
		identity.ExcludedChannelIDs = append(identity.ExcludedChannelIDs, id)
	}
	identity.ExcludedChannelIDs = append(identity.ExcludedChannelIDs, task.ChannelId)
	constraints.AddFilter(identity)
	constraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterRequestPath, RequestPath: retry.Path})
	switch {
	case retry.NativePluginKey != "":
		generation := pluginruntime.DefaultRegistry.Generation()
		if generation == nil {
			return c, nil, fmt.Errorf("video task plugin unavailable")
		}
		plugin, found := generation.Get(retry.NativePluginKey)
		if !found {
			return c, nil, fmt.Errorf("video task plugin unavailable")
		}
		matched := false
		for _, route := range plugin.Meta.Routes {
			if route.Method == retry.Method && route.Path == retry.NativeRoutePath {
				c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: plugin, Route: route})
				matched = true
				break
			}
		}
		if !matched {
			return c, nil, fmt.Errorf("video task route unavailable")
		}
		middleware.PrepareTaskPluginRoute()(c)
	case retry.LegacyPluginKey != "":
		middleware.PrepareTaskPluginSubmit()(c)
	default:
		middleware.PinTaskPluginEndpoint()(c)
		if !c.IsAborted() {
			middleware.PrepareTaskPluginEndpoint()(c)
		}
	}
	if c.IsAborted() {
		return c, nil, fmt.Errorf("original video request can no longer be decoded")
	}
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		return c, nil, err
	}
	info.PublicTaskID, info.Action = task.TaskID, retry.Action
	info.BillingSource, info.SubscriptionId = task.PrivateData.BillingSource, task.PrivateData.SubscriptionId
	// Force getChannel to select from the remaining candidates rather than
	// reusing the incoming HTTP request's already-selected channel.
	info.ChannelMeta = &relaycommon.ChannelMeta{}
	return c, info, nil
}

func canRetryVideoTask(task *model.Task, failure *relaycommon.TaskInfo, now int64) bool {
	if task == nil {
		return false
	}
	retry := task.PrivateData.VideoRetry
	if retry == nil || !retry.Ready || retry.Submitting || len(retry.Body) == 0 || failure == nil || failure.Status != model.TaskStatusFailure ||
		retry.AcceptedAt <= 0 || now < retry.AcceptedAt || now-retry.AcceptedAt > service.VideoTaskRetryWindowSeconds || common.RetryTimes <= 0 || len(task.PrivateData.ChannelRetryPath) > common.RetryTimes ||
		task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		return false
	}
	_, pinned, _ := retry.Constraints.ResolvedPin()
	return !pinned
}

func retryVideoTask(ctx context.Context, task *model.Task, failure *relaycommon.TaskInfo) (bool, error) {
	now := time.Now().Unix()
	if !canRetryVideoTask(task, failure, now) {
		return false, nil
	}
	retry := task.PrivateData.VideoRetry
	ctx, cancel := context.WithTimeout(ctx, time.Duration(service.VideoTaskRetrySubmitTimeoutSeconds-30)*time.Second)
	defer cancel()
	fromStatus := task.Status
	retry.Submitting, retry.SubmitStartedAt = true, now
	retry.Details = append(retry.Details, model.AsyncImageRetryDetail{
		Attempt: len(task.PrivateData.ChannelRetryPath), ChannelID: task.ChannelId,
		Status: model.TaskStatusFailure, Error: failure.Reason, AttemptedAt: now,
	})
	won, err := task.UpdateWithStatus(fromStatus)
	if err != nil || !won {
		return true, err
	}
	for len(task.PrivateData.ChannelRetryPath) <= common.RetryTimes {
		c, info, buildErr := buildVideoTaskRetryContext(ctx, task)
		if c != nil {
			defer common.CleanupBodyStorage(c)
		}
		if buildErr != nil {
			failure.Reason += "; video channel retry stopped: " + buildErr.Error()
			break
		}
		channel, channelErr := getChannel(c, info, &service.RetryParam{Ctx: c, TokenGroup: task.Group, ModelName: task.Properties.OriginModelName, RequestPath: c.Request.URL.Path})
		if channelErr != nil {
			logger.LogInfo(ctx, fmt.Sprintf("video task %s has no unused eligible retry channel", task.TaskID))
			break
		}
		task.ChannelId = channel.Id
		task.PrivateData.ChannelRetryPath = append(task.PrivateData.ChannelRetryPath, strconv.Itoa(channel.Id))
		task.PrivateData.VideoRetry.Attempt = len(task.PrivateData.ChannelRetryPath)
		won, err = task.UpdateWithStatus(fromStatus)
		if err != nil || !won {
			return true, err
		}
		info.RetryIndex = len(task.PrivateData.ChannelRetryPath) - 1
		service.AppendUsedChannel(c, channel.Id)
		info.Billing, err = service.NewVideoTaskRetryBilling(ctx, task, info)
		if err != nil {
			failure.Reason = "video channel retry billing unavailable: " + err.Error()
			break
		}
		result, taskErr := relay.RelayTaskSubmit(c, info)
		if info.QuotaClamp != nil {
			task.PrivateData.VideoRetry.RecordQuotaClamp(info.QuotaClamp)
			logger.LogWarn(c, fmt.Sprintf("video task %s retry quota saturated: %s", task.TaskID, info.QuotaClamp))
		}
		if taskErr != nil {
			task.PrivateData.VideoRetry.Details = append(task.PrivateData.VideoRetry.Details, model.AsyncImageRetryDetail{
				Attempt: len(task.PrivateData.ChannelRetryPath), ChannelID: channel.Id, ChannelName: channel.Name,
				ChannelType: channel.Type, Status: model.TaskStatusFailure, StatusCode: taskErr.StatusCode,
				ErrorCode: taskErr.Code, Error: taskErr.Message, AttemptedAt: time.Now().Unix(),
			})
			failure.Reason = taskErr.Message
			// Only a definite non-2xx response permits another POST. Transport
			// and parse errors may hide an accepted (and billable) upstream job.
			if taskErr.LocalError || taskErr.Code != "fail_to_fetch_task" || taskErr.NoRetry || decideTaskRetry(c, taskErr, common.RetryTimes-len(task.PrivateData.ChannelRetryPath)+1).Action != "retry" {
				break
			}
			continue
		}
		if result == nil {
			failure.Reason = "video channel submission outcome is unknown"
			break
		}
		// Retain the accepted job even if a submit-time price adjustment could
		// not be reserved; the existing completion settlement reconciles it.
		// Immediate failure leaves the reservation to the terminal refund
		// path, which records its original amount and single rollback log.
		if result.Immediate == nil || result.Immediate.Status != model.TaskStatusFailure {
			settleErr := info.Billing.Reserve(result.Quota)
			if settleErr == nil {
				settleErr = info.Billing.Settle(result.Quota)
			}
			if settleErr != nil {
				task.PrivateData.BillingError = "video retry submit settlement deferred"
			} else {
				task.PrivateData.BillingError = ""
			}
		}
		task.Platform, task.Action = result.Platform, info.Action
		task.Properties.UpstreamModelName = info.UpstreamModelName
		task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
		if c.GetBool("native_task_adaptor") {
			task.PrivateData.Execution = nil
		}
		task.PrivateData.UpstreamTaskID, task.PrivateData.ResultURL = result.UpstreamTaskID, ""
		task.PrivateData.Key = ""
		if channel.Type == constant.ChannelTypeGemini || channel.Type == constant.ChannelTypeVertexAi || channel.Type == constant.ChannelTypeNewAPI {
			task.PrivateData.Key = info.ApiKey
		}
		task.PrivateData.PluginState = result.PluginState
		task.PrivateData.PollFailures = 0
		task.PrivateData.ActualQuota = result.Quota
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			ModelPrice: info.PriceData.ModelPrice, FixedPriceUnit: info.PriceData.FixedPriceUnit,
			GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio, ModelRatio: info.PriceData.ModelRatio,
			OtherRatios: info.PriceData.OtherRatios(), OriginModelName: info.OriginModelName,
			PerCallBilling: common.StringsContains(constant.TaskPricePatches, info.OriginModelName) || info.PriceData.UsePrice,
			TieredSnapshot: info.TieredBillingSnapshot,
		}
		task.Status, task.Progress, task.FailReason = model.TaskStatusSubmitted, taskcommon.ProgressSubmitted, ""
		task.StartTime, task.FinishTime = 0, 0
		task.Data = result.TaskData
		task.PrivateData.VideoRetry.AcceptedAt = time.Now().Unix()
		task.PrivateData.VideoRetry.Submitting = false
		if immediate := result.Immediate; immediate != nil {
			task.Status, task.Progress = model.TaskStatus(immediate.Status), immediate.Progress
			if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
				task.FinishTime, task.Progress = time.Now().Unix(), taskcommon.ProgressComplete
				if task.Status == model.TaskStatusFailure {
					task.FailReason = immediate.Reason
				}
				task.PrivateData.ResultURL = immediate.Url
				if task.Status == model.TaskStatusSuccess && immediate.Url == "" {
					task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
				}
			}
		}
		won, err = task.UpdateWithStatus(fromStatus)
		if err != nil || !won {
			return true, err
		}
		logger.LogInfo(ctx, fmt.Sprintf("video task %s resubmitted to channel %d (attempt %d)", task.TaskID, channel.Id, len(task.PrivateData.ChannelRetryPath)))
		if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
			return true, service.SettleTaskBillingOnComplete(ctx, relay.GetTaskAdaptor(task.Platform), task, result.Immediate)
		}
		return true, nil
	}
	// The caller owns the terminal CAS and the single final refund. Persisted
	// Submitting remains set until that CAS, excluding other polling workers.
	task.PrivateData.VideoRetry.Submitting = false
	return false, nil
}
