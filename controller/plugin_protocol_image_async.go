package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	taskjs "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
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
	if task.PrivateData.ImageRetry.Submitting {
		return nil, "", fmt.Errorf("plugin image submission outcome is unknown; automatic resubmission stopped")
	}
	body, err := os.ReadFile(task.PrivateData.ImageRetry.BodyPath)
	if err != nil {
		return nil, "", err
	}
	c, _, err := buildInternalAsyncBaseContext(ctx, task, body)
	if err != nil {
		return nil, "", err
	}
	defer common.CleanupBodyStorage(c)
	defer service.ReleaseAutoSwitchAttempt(c)
	middleware.PinTaskPluginEndpoint()(c)
	if !c.IsAborted() {
		middleware.PrepareTaskPluginEndpoint()(c)
	}
	if c.IsAborted() {
		return nil, "", fmt.Errorf("queued image plugin request can no longer be decoded")
	}
	value, _ := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	pinned, ok := value.(pluginruntime.PinnedEndpoint)
	if !ok || pinned.Plugin == nil || pinned.Protocol != pluginruntime.ProtocolOpenAIImage {
		return nil, "", fmt.Errorf("queued image plugin is unavailable")
	}
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		return nil, "", err
	}
	proxy := channel.GetSetting().Proxy
	if channel.Status != common.ChannelStatusEnabled {
		return nil, proxy, fmt.Errorf("queued image channel is disabled")
	}
	if apiErr := middleware.SetupContextForSelectedChannel(c, channel, task.Properties.OriginModelName); apiErr != nil {
		return nil, proxy, apiErr
	}
	value, _ = c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	pinned = value.(pluginruntime.PinnedEndpoint)
	if execution := task.PrivateData.Execution; execution == nil || execution.TaskPlugin == nil || execution.TaskPlugin.Key != pinned.Plugin.Meta.Key {
		return nil, proxy, fmt.Errorf("queued image plugin binding changed")
	}
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		return nil, proxy, err
	}
	info.PublicTaskID = task.TaskID
	info.OriginModelName = task.Properties.OriginModelName
	info.Action = c.GetString("task_action")
	bc := task.PrivateData.BillingContext
	if bc == nil {
		return nil, proxy, fmt.Errorf("queued image billing snapshot is missing")
	}
	info.PriceData = types.PriceData{ModelPrice: bc.ModelPrice, FixedPriceUnit: bc.FixedPriceUnit,
		ModelRatio: bc.ModelRatio, UsePrice: bc.PerCallBilling, Quota: task.Quota}
	info.PriceData.GroupRatioInfo.GroupRatio = bc.GroupRatio
	info.PriceData.ReplaceOtherRatios(bc.OtherRatios)
	info.TieredBillingSnapshot = bc.TieredSnapshot
	info.InitChannelMeta(c)
	info.UpstreamModelName = task.Properties.UpstreamModelName
	if task.PrivateData.PluginImage == nil {
		task.PrivateData.ImageRetry.Submitting = true
		if err := persistAsyncImageAttempt(task); err != nil {
			return nil, proxy, err
		}
		c.Request = c.Request.WithContext(service.BeginUpstreamDelivery(ctx, channel.Id, 1))
		result, taskErr := relay.RelayQueuedTaskSubmit(c, info)
		if taskErr != nil {
			return nil, proxy, fmt.Errorf("%s", taskSubmissionAPIError(taskErr).MaskSensitiveErrorWithStatusCode())
		}
		if result == nil {
			return nil, proxy, fmt.Errorf("plugin returned no submission result")
		}
		task.PrivateData.ImageRetry.Submitting = false
		task.Properties.UpstreamModelName = info.UpstreamModelName
		task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
		task.PrivateData.PluginState = result.PluginState
		task.PrivateData.PluginImage = &model.AsyncPluginImageState{Platform: result.Platform, Status: model.TaskStatusSubmitted}
		task.PrivateData.ActualQuota = result.Quota
		task.Data = result.TaskData
		bc.TieredSnapshot, bc.OtherRatios = info.TieredBillingSnapshot, info.PriceData.OtherRatios()
		if result.Immediate != nil {
			applyQueuedPluginImageResult(task, result.Immediate)
		}
		if err := persistAsyncImageAttempt(task); err != nil {
			return nil, proxy, err
		}
	}
	adaptor := taskjs.New(pinned.Plugin)
	adaptor.Init(info)
	for task.PrivateData.PluginImage.Status != model.TaskStatusSuccess && task.PrivateData.PluginImage.Status != model.TaskStatusFailure {
		select {
		case <-ctx.Done():
			return nil, proxy, ctx.Err()
		case <-time.After(taskPluginImagePollInterval):
		}
		providerTask := *task
		providerTask.Platform = task.PrivateData.PluginImage.Platform
		response, err := adaptor.FetchTask(channel.GetBaseURL(), info.ApiKey, &providerTask, proxy)
		if err != nil {
			return nil, proxy, err
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		response.Body.Close()
		if readErr != nil {
			return nil, proxy, readErr
		}
		if response.StatusCode/100 != 2 || len(responseBody) > 1<<20 {
			return nil, proxy, fmt.Errorf("plugin image polling returned an invalid response")
		}
		result, err := adaptor.ParseTaskResult(&providerTask, response, responseBody)
		if err != nil {
			return nil, proxy, err
		}
		if result == nil {
			return nil, proxy, fmt.Errorf("plugin image polling returned no task")
		}
		task.Data = responseBody
		applyQueuedPluginImageResult(task, result)
		if result.Status == model.TaskStatusSuccess && bc.TieredSnapshot != nil && len(result.UsageFacts) > 0 {
			settlement, facts, err := service.EvaluateTaskCompletionUsage(bc.TieredSnapshot, result.UsageFacts)
			if err != nil {
				return nil, proxy, err
			}
			task.PrivateData.ActualQuota = settlement.ActualQuotaAfterGroup
			bc.TieredSnapshot.UsageFacts = facts
		} else if result.Status == model.TaskStatusSuccess && bc.TieredSnapshot == nil {
			if quota := adaptor.AdjustBillingOnComplete(&providerTask, result); quota > 0 {
				task.PrivateData.ActualQuota = quota
			}
		}
		if err := persistAsyncImageAttempt(task); err != nil {
			return nil, proxy, err
		}
	}
	if task.PrivateData.PluginImage.Status == model.TaskStatusFailure {
		return nil, proxy, fmt.Errorf("image generation failed: %s", task.FailReason)
	}
	providerTask := *task
	providerTask.Platform, providerTask.Status = task.PrivateData.PluginImage.Platform, model.TaskStatusSuccess
	providerTask.PrivateData.ResultURL = task.PrivateData.PluginImage.ResultURL
	view, err := service.BuildTaskPluginView(&providerTask)
	if err != nil {
		return nil, proxy, err
	}
	viewValue, err := taskPluginProtocolJSONValue(view)
	if err != nil {
		return nil, proxy, err
	}
	requestValue, _ := c.Get(pluginruntime.ContextKeyProtocolRequest)
	request := requestValue.(pluginruntime.ProtocolRequestContext)
	payload, err := pinned.Plugin.Engine.CallPathWithAdmissionTimeout(ctx, pluginruntime.DefaultCallTimeout,
		"protocols", []string{pinned.Protocol, "render"}, request.JSValue(), viewValue)
	if err != nil {
		return nil, proxy, err
	}
	// Preserve provider URLs. The queue's result handling owns retention;
	// image protocol response_format must not force a base64 download here.
	encoded, err := common.Marshal(payload)
	return encoded, proxy, err
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
		task.FailReason = result.Reason
	}
	if usage := result.TokenUsage; usage != nil {
		task.PrivateData.PromptTokens, task.PrivateData.CompletionTokens, task.PrivateData.TotalTokens = usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens
		task.PrivateData.UsageDetails = service.BuildTaskUsageDetails(usage)
	}
}
