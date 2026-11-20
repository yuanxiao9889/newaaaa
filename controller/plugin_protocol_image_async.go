package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	taskjs "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Admission uses the plugin's validation and usage expression, then hands the
// original JSON/multipart body to the existing durable image queue.
func enqueueTaskPluginImage(c *gin.Context) {
	mode := relayconstant.RelayModeImagesGenerations
	if c.Request.URL.Path == "/v1/images/edits" {
		mode = relayconstant.RelayModeImagesEdits
	}
	request, err := helper.GetAndValidOpenAIImageRequest(c, mode)
	if err != nil {
		respondPluginProtocolError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	count, err := request.ImageCount(false)
	if err != nil || count != 1 {
		respondPluginProtocolError(c, http.StatusBadRequest, "invalid_request", "async image tasks only support n=1")
		return
	}
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		respondPluginProtocolError(c, http.StatusInternalServerError, "task_protocol_error", "Task protocol request failed")
		return
	}
	info.OriginModelName = c.GetString("resolved_task_model")
	info.Action = c.GetString("task_action")
	if taskErr := relay.ResolveOriginTask(c, info); taskErr != nil {
		respondTaskPluginImageError(c, taskErr)
		return
	}
	if taskErr := relay.ApplyOriginTaskAffinity(c, info); taskErr != nil {
		respondTaskPluginImageError(c, taskErr)
		return
	}
	if _, taskErr := relay.PrepareTaskSubmission(c, info); taskErr != nil {
		if info.Billing != nil {
			info.Billing.Refund(c)
		}
		service.ReleaseAutoSwitchAttempt(c)
		respondTaskPluginImageError(c, taskErr)
		return
	}
	info.RelayMode = mode
	if apiErr := relay.SubmitInternalAsyncImageTask(c, info, request); apiErr != nil {
		// Admission may reject before its own refund defer (for example a full queue).
		if info.Billing != nil {
			info.Billing.Refund(c)
		}
		respondTaskPluginImageError(c, service.TaskErrorFromAPIError(apiErr))
	}
}

// A plugin queue job owns one reservation and one task row. Its accepted
// provider response is checkpointed before rendering, so recovery never POSTs
// an accepted generation again, including synchronous upstream operations.
func runQueuedTaskPluginImage(ctx context.Context, task *model.Task) ([]byte, string, error) {
	if task.PrivateData.ImageRetry == nil {
		return nil, "", fmt.Errorf("plugin image request snapshot is missing")
	}
	body, _, proxy, _, err := runRetryableAsyncImageRelay(ctx, task)
	return body, proxy, err
}

// The shared queue selector supplies a compatible plugin channel. Accepted work
// stays on that channel; only an explicit rejection permits another selection.
func runQueuedTaskPluginImageAttempt(ctx context.Context, task *model.Task, cycle *service.ChannelRetryCycle, c *gin.Context, channel *model.Channel) ([]byte, string, bool, error) {
	originalChannel := task.ChannelId
	proxy := channel.GetSetting().Proxy
	if channel.Status != common.ChannelStatusEnabled {
		return nil, proxy, false, fmt.Errorf("queued image channel is disabled")
	}
	value, _ := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	pinned := value.(pluginruntime.PinnedEndpoint)
	replacing := task.PrivateData.PluginImage == nil && (len(task.PrivateData.ChannelRetryPath) > 0 || channel.Id != originalChannel || task.PrivateData.RequestRelayFormat != relay.PluginAsyncImageFormat)
	if !replacing {
		if execution := task.PrivateData.Execution; execution == nil || execution.TaskPlugin == nil || execution.TaskPlugin.Key != pinned.Plugin.Meta.Key {
			return nil, proxy, false, fmt.Errorf("queued image plugin binding changed")
		}
	}
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		return nil, proxy, false, err
	}
	info.PublicTaskID = task.TaskID
	info.OriginModelName = task.Properties.OriginModelName
	info.Action = c.GetString("task_action")
	bc := task.PrivateData.BillingContext
	if bc == nil {
		return nil, proxy, false, fmt.Errorf("queued image billing snapshot is missing")
	}
	info.PriceData = types.PriceData{ModelPrice: bc.ModelPrice, FixedPriceUnit: bc.FixedPriceUnit,
		ModelRatio: bc.ModelRatio, UsePrice: bc.PerCallBilling, Quota: task.Quota}
	info.PriceData.GroupRatioInfo.GroupRatio = bc.GroupRatio
	info.PriceData.ReplaceOtherRatios(bc.OtherRatios)
	info.TieredBillingSnapshot = bc.TieredSnapshot
	info.InitChannelMeta(c)
	info.UpstreamModelName = task.Properties.UpstreamModelName
	if task.PrivateData.PluginImage == nil {
		retry := task.PrivateData.ImageRetry
		if len(task.PrivateData.ChannelRetryPath) > 0 && cycle.Budget.Remaining(1, len(task.PrivateData.ChannelRetryPath), time.Now()) <= 0 {
			return nil, proxy, false, fmt.Errorf("plugin image retry budget exhausted")
		}
		task.ChannelId = channel.Id
		task.PrivateData.RequestRelayFormat = relay.PluginAsyncImageFormat
		retry.SettlementQuota = nil
		retry.SettlementInput = nil
		task.PrivateData.ChannelRetryPath = append(task.PrivateData.ChannelRetryPath, strconv.Itoa(channel.Id))
		retry.CycleStart, retry.Submitting = cycle.Start, true
		task.PrivateData.AutoSwitch = service.AutoSwitchAttempt(c)
		if err := persistAsyncImageAttempt(task); err != nil {
			return nil, proxy, false, err
		}
		info.RetryIndex = len(task.PrivateData.ChannelRetryPath) - 1
		info.Billing, err = service.NewImageTaskRetryBilling(ctx, task, info)
		if err != nil {
			return nil, proxy, false, err
		}
		c.Request = c.Request.WithContext(service.BeginUpstreamDelivery(ctx, channel.Id, len(task.PrivateData.ChannelRetryPath)))
		var result *relay.TaskSubmitResult
		var taskErr *dto.TaskError
		retry.AttemptStartedAtMS = time.Now().UnixMilli()
		if replacing {
			// Re-price the replacement provider using its own schema. The task funds
			// adapter reserves only the difference, atomically, before the new POST.
			info.PriceData = types.PriceData{}
			result, taskErr = relay.RelayTaskSubmit(c, info)
		} else {
			result, taskErr = relay.RelayQueuedTaskSubmit(c, info)
		}
		if taskErr != nil {
			finishedAt := time.Now()
			service.RecordAutoSwitchTaskSubmissionFailure(c, taskErr)
			apiErr := taskSubmissionAPIError(taskErr)
			retry.Details = append(retry.Details, buildInternalAsyncChannelRetryDetail(len(task.PrivateData.ChannelRetryPath), channel, apiErr, retry.AttemptStartedAtMS, finishedAt))
			remaining := cycle.Budget.Remaining(common.RetryTimes-len(task.PrivateData.ChannelRetryPath)+1, len(task.PrivateData.ChannelRetryPath), time.Now())
			confirmed := !taskErr.LocalError && !taskErr.NoRetry && taskErr.Code == "fail_to_fetch_task" && c.GetBool("auto_switch_task_rejected")
			again := confirmed && !retry.SkipRetry && (task.PrivateData.MediaRetry != nil || operation_setting.ShouldRetryByStatusCode(taskErr.StatusCode)) && decideTaskRetry(c, taskErr, remaining).Action == "retry"
			if !again {
				recordInternalAsyncImageFinalErrorLog(c, task, channel, apiErr, task.PrivateData.ChannelRetryPath, retry.Details)
				return nil, proxy, false, fmt.Errorf("%s", apiErr.MaskSensitiveErrorWithStatusCode())
			}
			retry.Submitting = false
			task.PrivateData.AutoSwitch = nil
			if err := persistAsyncImageAttempt(task); err != nil {
				return nil, proxy, false, err
			}
			return nil, proxy, true, nil
		}
		if result == nil {
			return nil, proxy, false, fmt.Errorf("plugin returned no submission result")
		}
		service.DetachAutoSwitchAttempt(c)
		execution := service.TaskExecutionSnapshotFromContext(c)
		if task.PrivateData.Execution != nil {
			execution.RequestID = task.PrivateData.Execution.RequestID
		}
		task.PrivateData.Execution = execution
		if replacing {
			bc = &model.TaskBillingContext{ModelPrice: info.PriceData.ModelPrice, FixedPriceUnit: info.PriceData.FixedPriceUnit,
				GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio, ModelRatio: info.PriceData.ModelRatio,
				OtherRatios: info.PriceData.OtherRatios(), OriginModelName: info.OriginModelName,
				PerCallBilling: common.StringsContains(constant.TaskPricePatches, info.OriginModelName) || info.PriceData.UsePrice,
				TieredSnapshot: info.TieredBillingSnapshot}
			task.PrivateData.BillingContext = bc
		}
		task.PrivateData.ImageRetry.Submitting = false
		task.Properties.UpstreamModelName = info.UpstreamModelName
		task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
		task.PrivateData.PluginState = result.PluginState
		task.PrivateData.PluginImage = &model.AsyncPluginImageState{Platform: result.Platform, Status: model.TaskStatusSubmitted}
		task.PrivateData.ActualQuota = result.Quota
		task.Data = result.TaskData
		if len(result.TaskData) > 1<<20 && result.Immediate != nil && result.Immediate.Status == model.TaskStatusSuccess {
			// Keep Base64 results recoverable without growing database rows or
			// rejecting an image the provider already generated and charged.
			dir := filepath.Clean(common.GetAsyncImageRequestStoragePath())
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, proxy, false, err
			}
			file, err := os.CreateTemp(dir, task.TaskID+"-result-*.json")
			if err != nil {
				return nil, proxy, false, err
			}
			_, writeErr := file.Write(result.TaskData)
			if writeErr == nil {
				writeErr = file.Sync()
			}
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = os.Remove(file.Name())
				return nil, proxy, false, errors.Join(writeErr, closeErr)
			}
			task.PrivateData.PluginImage.ResponseBodyPath = file.Name()
			task.Data = nil
		}
		bc.TieredSnapshot, bc.OtherRatios = info.TieredBillingSnapshot, info.PriceData.OtherRatios()
		if result.Immediate != nil {
			applyQueuedPluginImageResult(task, result.Immediate)
			service.RecordAutoSwitchTaskResult(ctx, task, result.Immediate)
		}
		if err := persistAsyncImageAttempt(task); err != nil {
			return nil, proxy, false, err
		}
	}
	adaptor := taskjs.New(pinned.Plugin)
	adaptor.Init(info)
	for task.PrivateData.PluginImage.Status != model.TaskStatusSuccess && task.PrivateData.PluginImage.Status != model.TaskStatusFailure {
		select {
		case <-ctx.Done():
			return nil, proxy, false, ctx.Err()
		case <-time.After(taskPluginImagePollInterval):
		}
		providerTask := *task
		providerTask.Platform = task.PrivateData.PluginImage.Platform
		response, err := adaptor.FetchTask(ctx, channel.GetBaseURL(), info.ApiKey, &providerTask, proxy)
		if err != nil {
			return nil, proxy, false, err
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		response.Body.Close()
		if readErr != nil {
			return nil, proxy, false, readErr
		}
		if response.StatusCode/100 != 2 || len(responseBody) > 1<<20 {
			return nil, proxy, false, fmt.Errorf("plugin image polling returned an invalid response")
		}
		result, err := adaptor.ParseTaskResult(&providerTask, response, responseBody)
		if err != nil {
			return nil, proxy, false, err
		}
		if result == nil {
			return nil, proxy, false, fmt.Errorf("plugin image polling returned no task")
		}
		task.Data = responseBody
		applyQueuedPluginImageResult(task, result)
		service.RecordAutoSwitchTaskResult(ctx, task, result)
		if result.Status == model.TaskStatusSuccess && bc.TieredSnapshot != nil && len(result.UsageFacts) > 0 {
			settlement, facts, err := service.EvaluateTaskCompletionUsage(bc.TieredSnapshot, result.UsageFacts)
			if err != nil {
				return nil, proxy, false, err
			}
			task.PrivateData.ActualQuota = settlement.ActualQuotaAfterGroup
			bc.TieredSnapshot.UsageFacts = facts
		} else if result.Status == model.TaskStatusSuccess && bc.TieredSnapshot == nil {
			if quota := adaptor.AdjustBillingOnComplete(&providerTask, result); quota > 0 {
				task.PrivateData.ActualQuota = quota
			}
		}
		if err := persistAsyncImageAttempt(task); err != nil {
			return nil, proxy, false, err
		}
	}
	if task.PrivateData.PluginImage.Status == model.TaskStatusFailure {
		state := task.PrivateData.MediaRetry
		if state != nil && !task.PrivateData.ImageRetry.SkipRetry {
			finishedAt := time.Now()
			if failedAtMS := task.PrivateData.PluginImage.FailedAtMS; failedAtMS > 0 {
				finishedAt = time.UnixMilli(failedAtMS)
			}
			decision := service.DecideMediaRetry(c, channel.Id, "", task.FailReason)
			task.PrivateData.ImageRetry.Details = append(task.PrivateData.ImageRetry.Details, model.AsyncImageRetryDetail{
				Attempt: state.Attempts, ChannelID: channel.Id, ChannelName: channel.Name, Status: model.TaskStatusFailure,
				Error: task.FailReason, ErrorType: decision.Reason, AttemptedAt: finishedAt.Unix(),
				ElapsedMS: channelAttemptElapsedMS(task.PrivateData.ImageRetry.AttemptStartedAtMS, finishedAt),
			})
			if decision.Action == "retry" {
				task.PrivateData.PluginImage, task.PrivateData.AutoSwitch = nil, nil
				task.PrivateData.PluginState, task.Data = nil, nil
				task.PrivateData.UpstreamTaskID, task.FailReason = "", ""
				task.PrivateData.PromptTokens, task.PrivateData.CompletionTokens, task.PrivateData.TotalTokens = 0, 0, 0
				task.PrivateData.UsageDetails = nil
				if err := persistAsyncImageAttempt(task); err != nil {
					return nil, proxy, false, err
				}
				return nil, proxy, true, nil
			}
		}
		return nil, proxy, false, fmt.Errorf("image generation failed: %s", task.FailReason)
	}
	providerTask := *task
	providerTask.Platform, providerTask.Status = task.PrivateData.PluginImage.Platform, model.TaskStatusSuccess
	providerTask.PrivateData.ResultURL = task.PrivateData.PluginImage.ResultURL
	if path := task.PrivateData.PluginImage.ResponseBodyPath; path != "" {
		providerTask.Data, err = os.ReadFile(path)
		if err != nil {
			return nil, proxy, false, fmt.Errorf("read accepted image result: %w", err)
		}
	}
	view, err := service.BuildTaskPluginView(&providerTask)
	if err != nil {
		return nil, proxy, false, err
	}
	viewValue, err := taskPluginProtocolJSONValue(view)
	if err != nil {
		return nil, proxy, false, err
	}
	requestValue, _ := c.Get(pluginruntime.ContextKeyProtocolRequest)
	request := requestValue.(pluginruntime.ProtocolRequestContext)
	payload, err := pinned.Plugin.Engine.CallPathWithAdmissionTimeout(ctx, pluginruntime.DefaultCallTimeout,
		"protocols", []string{pinned.Protocol, "render"}, request.JSValue(), viewValue)
	if err != nil {
		return nil, proxy, false, err
	}
	// Preserve provider URLs. The queue's result handling owns retention;
	// image protocol response_format must not force a base64 download here.
	encoded, err := common.Marshal(payload)
	return encoded, proxy, false, err
}

func applyQueuedPluginImageResult(task *model.Task, result *relaycommon.TaskInfo) {
	task.PrivateData.PluginImage.Status = model.TaskStatus(result.Status)
	if result.Url != "" {
		task.PrivateData.PluginImage.ResultURL = result.Url
	}
	if len(result.PluginState) > 0 {
		task.PrivateData.PluginState = result.PluginState
	}
	if result.Status == model.TaskStatusFailure {
		task.PrivateData.PluginImage.FailedAtMS = time.Now().UnixMilli()
		task.FailReason = result.Reason
	}
	if usage := result.TokenUsage; usage != nil {
		task.PrivateData.PromptTokens, task.PrivateData.CompletionTokens, task.PrivateData.TotalTokens = usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens
		task.PrivateData.UsageDetails = service.BuildTaskUsageDetails(usage)
	}
}
