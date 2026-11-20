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

// Small bodies retain the legacy inline representation. Larger bodies use
// shared database chunks, within the gateway's existing request body limit.
const inlineVideoTaskRetryBodyBytes = 8 << 20

func init() {
	service.RetryVideoTaskFunc = retryVideoTask
}

func captureVideoTaskRetry(c *gin.Context, info *relaycommon.RelayInfo) (*model.VideoTaskRetryState, error) {
	// Legacy guarded requests remain single-dispatch. New channel policies own
	// a bounded retry budget without changing the client's idempotent receipt.
	if c.GetString("transfer_execution_id") != "" && service.MediaRetry(c) == nil {
		return nil, nil
	}
	if common.RetryTimes <= 0 && service.MediaRetry(c) == nil || info.TaskRelayInfo == nil || info.LockedChannel != nil || info.OriginTaskID != "" || len(info.OriginTasks) > 0 || service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return nil, nil
	}
	constraints := service.GetChannelConstraints(c)
	if _, pinned, _ := constraints.ResolvedPin(); pinned {
		return nil, nil
	}
	state := &model.VideoTaskRetryState{
		Method: c.Request.Method, Path: c.Request.URL.Path, Query: c.Request.URL.RawQuery,
		ContentType: c.Request.Header.Get("Content-Type"), UserGroup: info.UserGroup,
		Accept: c.Request.Header.Get("Accept"),
		Action: info.Action,
	}
	isVideo := state.Path == "/v1/videos" || state.Path == "/v1/video/generations" || service.MediaRetry(c) != nil
	switch constant.NormalizeTaskAction(info.Action) {
	case constant.TaskActionTextToVideo, constant.TaskActionImageToVideo, constant.TaskActionFirstTailToVideo, constant.TaskActionReferenceToVideo:
		isVideo = true
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint)
		// Responses and Images have distinct rendering/lifecycle contracts.
		if !ok || pinned.Protocol != "openai_video" && (pinned.Protocol != "openai_image" || service.MediaRetry(c) == nil) {
			return nil, nil
		}
		isVideo = true
	}
	if !isVideo {
		return nil, nil
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedRoute)
		if !ok || pinned.Plugin == nil {
			return nil, nil
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
	if err != nil {
		return nil, err
	}
	if err := common.Unmarshal(encoded, &state.Constraints); err != nil {
		return nil, err
	}
	body, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	if body.Size() > inlineVideoTaskRetryBodyBytes {
		state.BodyRef, err = model.SaveTaskRetryBody(c.Request.Context(), body)
		return state, err
	}
	data, err := body.Bytes()
	if err != nil {
		return nil, err
	}
	state.Body = bytes.Clone(data)
	return state, nil
}

func buildVideoTaskRetryContext(ctx context.Context, task *model.Task) (*gin.Context, *relaycommon.RelayInfo, error) {
	retry := task.PrivateData.VideoRetry
	body := retry.Body
	if retry.BodyRef != nil {
		var err error
		body, err = retry.BodyRef.Read(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	target := retry.Path
	if retry.Query != "" {
		target += "?" + retry.Query
	}
	req, err := http.NewRequestWithContext(ctx, retry.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	service.BindMediaRetry(c, task.PrivateData.MediaRetry)
	if task.PrivateData.TransferExecutionID != "" {
		c.Set("transfer_execution_id", task.PrivateData.TransferExecutionID)
	}
	c.Request.Header.Set("Content-Type", retry.ContentType)
	c.Request.Header.Set("Accept", retry.Accept)
	c.Set(common.KeyRequestBody, body)
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
		if task.PrivateData.MediaRetry != nil {
			break
		}
		id, err := strconv.Atoi(used)
		if err != nil {
			return c, nil, fmt.Errorf("invalid video retry channel history")
		}
		identity.ExcludedChannelIDs = append(identity.ExcludedChannelIDs, id)
	}
	if task.PrivateData.MediaRetry == nil {
		identity.ExcludedChannelIDs = append(identity.ExcludedChannelIDs, task.ChannelId)
	}
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
	if task == nil || task.PrivateData.TransferExecutionID != "" && task.PrivateData.MediaRetry == nil {
		return false
	}
	retry := task.PrivateData.VideoRetry
	if retry == nil || !retry.Ready || retry.Submitting || len(retry.Body) == 0 && retry.BodyRef == nil || failure == nil || failure.Status != model.TaskStatusFailure ||
		retry.AcceptedAt <= 0 || now < retry.AcceptedAt ||
		task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		return false
	}
	if common.ShouldStopRetryForSafety("", failure.Reason) {
		return false
	}
	if state := task.PrivateData.MediaRetry; state != nil {
		if state.Remaining(time.Unix(now, 0)) == 0 {
			return false
		}
	} else if now-retry.AcceptedAt > service.VideoTaskRetryWindowSeconds || common.RetryTimes <= 0 || len(task.PrivateData.ChannelRetryPath) > common.RetryTimes {
		return false
	}
	_, pinned, _ := retry.Constraints.ResolvedPin()
	return !pinned
}

func retryVideoTask(ctx context.Context, task *model.Task, failure *relaycommon.TaskInfo) (bool, error) {
	finishedAt := time.Now()
	service.RecordAutoSwitchTaskResult(ctx, task, failure)
	now := time.Now().Unix()
	if state := task.PrivateData.MediaRetry; state != nil && failure != nil && failure.Status == model.TaskStatusFailure {
		state.RecordFailure(task.ChannelId, "", failure.Reason)
		if common.ShouldStopRetryForSafety("", failure.Reason) {
			state.StopReason = "safety_related_error"
		} else if state.Remaining(time.Now()) == 0 {
			state.StopReason = "retry_budget_exhausted"
		}
	}
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
		Status: model.TaskStatusFailure, Error: failure.Reason, AttemptedAt: finishedAt.Unix(),
		ElapsedMS: channelAttemptElapsedMS(retry.AttemptStartedAtMS, finishedAt),
	})
	won, err := task.UpdateWithStatus(fromStatus)
	if err != nil || !won {
		return true, err
	}
	budget := service.RetryBudget{Media: task.PrivateData.MediaRetry}
	cycle := &service.ChannelRetryCycle{Enabled: task.PrivateData.MediaRetry != nil, Budget: budget}
	if task.PrivateData.MediaRetry != nil {
		cycle.Start = task.PrivateData.MediaRetry.CycleStart
	}
	for budget.Remaining(common.RetryTimes-len(task.PrivateData.ChannelRetryPath)+1, len(task.PrivateData.ChannelRetryPath), time.Now()) > 0 {
		c, info, buildErr := buildVideoTaskRetryContext(ctx, task)
		if c != nil {
			defer common.CleanupBodyStorage(c)
			defer service.ReleaseAutoSwitchAttempt(c)
		}
		if buildErr != nil {
			failure.Reason += "; video channel retry stopped: " + buildErr.Error()
			break
		}
		cycle.History = task.PrivateData.ChannelRetryPath
		channel, channelErr := getChannel(c, info, &service.RetryParam{Cycle: cycle, Ctx: c, TokenGroup: task.Group, ModelName: task.Properties.OriginModelName, RequestPath: c.Request.URL.Path})
		if channelErr != nil {
			if state := task.PrivateData.MediaRetry; state != nil {
				state.StopReason = "no_eligible_channel"
			}
			logger.LogInfo(ctx, fmt.Sprintf("video task %s has no eligible retry channel", task.TaskID))
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
		service.PersistMediaRetryBeforeDispatch(c, func() error {
			won, err := task.UpdateWithStatus(fromStatus)
			if err != nil {
				return err
			}
			if !won {
				return fmt.Errorf("media retry owner changed before dispatch")
			}
			return nil
		})
		task.PrivateData.VideoRetry.AttemptStartedAtMS = time.Now().UnixMilli()
		result, taskErr := relay.RelayTaskSubmit(c, info)
		finishedAt := time.Now()
		if info.QuotaClamp != nil {
			task.PrivateData.VideoRetry.RecordQuotaClamp(info.QuotaClamp)
			logger.LogWarn(c, fmt.Sprintf("video task %s retry quota saturated: %s", task.TaskID, info.QuotaClamp))
		}
		if taskErr != nil {
			service.RecordAutoSwitchTaskSubmissionFailure(c, taskErr)
			task.PrivateData.VideoRetry.Details = append(task.PrivateData.VideoRetry.Details, model.AsyncImageRetryDetail{
				Attempt: len(task.PrivateData.ChannelRetryPath), ChannelID: channel.Id, ChannelName: channel.Name,
				ChannelType: channel.Type, Status: model.TaskStatusFailure, StatusCode: taskErr.StatusCode,
				ErrorCode: taskErr.Code, Error: taskErr.Message, AttemptedAt: finishedAt.Unix(),
				ElapsedMS: channelAttemptElapsedMS(task.PrivateData.VideoRetry.AttemptStartedAtMS, finishedAt),
			})
			failure.Reason = taskErr.Message
			// Only a definite non-2xx response permits another POST. Transport
			// and parse errors may hide an accepted (and billable) upstream job.
			if taskErr.LocalError || taskErr.Code != "fail_to_fetch_task" || taskErr.NoRetry || decideTaskRetry(c, taskErr, budget.Remaining(common.RetryTimes-len(task.PrivateData.ChannelRetryPath)+1, len(task.PrivateData.ChannelRetryPath), time.Now())).Action != "retry" {
				break
			}
			continue
		}
		if result == nil {
			service.DetachAutoSwitchAttempt(c)
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
		task.PrivateData.AutoSwitch = service.AutoSwitchAttempt(c)
		service.DetachAutoSwitchAttempt(c)
		service.RecordAutoSwitchTaskResult(ctx, task, result.Immediate)
		task.PrivateData.Key = ""
		if channel.Type == constant.ChannelTypeGemini || channel.Type == constant.ChannelTypeVertexAi || channel.Type == constant.ChannelTypeNewAPI {
			task.PrivateData.Key = info.ApiKey
		}
		task.PrivateData.PluginState = result.PluginState
		task.PrivateData.PromptTokens, task.PrivateData.CompletionTokens, task.PrivateData.TotalTokens = 0, 0, 0
		task.PrivateData.UsageDetails, task.PrivateData.PendingUsageLog = nil, nil
		task.PrivateData.TokenUsageLogged = false
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
		retryImmediate := false
		if immediate := result.Immediate; immediate != nil {
			task.Status, task.Progress = model.TaskStatus(immediate.Status), immediate.Progress
			if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
				task.FinishTime, task.Progress = time.Now().Unix(), taskcommon.ProgressComplete
				if task.Status == model.TaskStatusFailure {
					task.FailReason = immediate.Reason
					if task.PrivateData.MediaRetry != nil {
						retryImmediate = service.DecideMediaRetry(c, channel.Id, "", immediate.Reason).Action == "retry"
					}
				}
				task.PrivateData.ResultURL = immediate.Url
				if task.Status == model.TaskStatusSuccess && immediate.Url == "" {
					task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
				}
			}
		}
		if retryImmediate {
			task.Status, task.Progress, task.FailReason, task.FinishTime = fromStatus, taskcommon.ProgressSubmitted, "", 0
			task.PrivateData.VideoRetry.Submitting = true
		}
		won, err = task.UpdateWithStatus(fromStatus)
		if err != nil || !won {
			return true, err
		}
		if retryImmediate {
			fromStatus = task.Status
			continue
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
