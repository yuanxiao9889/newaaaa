package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func enqueueTaskPluginAudio(c *gin.Context, info *relaycommon.RelayInfo) {
	defer service.ReleaseAutoSwitchAttempt(c)
	value, _ := c.Get(pluginruntime.ContextKeyPinnedRoute)
	pinned, ok := value.(pluginruntime.PinnedRoute)
	if !ok || pinned.Plugin == nil || pinned.Route.Type != pluginruntime.RouteTypeSubmit {
		respondPluginProtocolError(c, http.StatusBadRequest, "invalid_request", "Async audio requires a native submit route")
		return
	}
	if !slices.Contains(pinned.Plugin.Meta.RequiredCapabilities, pluginruntime.CapabilityNativeAudioQueue) {
		respondPluginProtocolError(c, http.StatusBadRequest, "invalid_request", "This audio plugin does not support queued generation")
		return
	}
	tasks, err := model.GetAsyncAudioQueueTasks(c.Request.Context())
	if err != nil {
		respondPluginProtocolError(c, 500, "queue_unavailable", "Failed to read audio queue")
		return
	}
	if len(tasks) >= 500 {
		respondPluginProtocolError(c, 429, "queue_full", "Audio queue is full")
		return
	}
	if _, taskErr := relay.PrepareTaskSubmission(c, info); taskErr != nil {
		if info.Billing != nil {
			info.Billing.Refund(c)
		}
		respondTaskSubmissionError(c, taskErr)
		return
	}
	durable := false
	defer func() {
		if !durable && info.Billing != nil {
			info.Billing.Refund(c)
		}
	}()
	request, _ := c.Get("task_request")
	body, err := common.Marshal(request)
	if err != nil {
		respondPluginProtocolError(c, 400, "invalid_request", "Unable to retain audio request")
		return
	}
	var config struct {
		AudioConfig struct {
			Format     string `json:"format"`
			SampleRate int    `json:"sample_rate"`
		} `json:"audio_config"`
	}
	if err = common.Unmarshal(body, &config); err != nil {
		respondPluginProtocolError(c, 400, "invalid_request", "Invalid audio request")
		return
	}
	if config.AudioConfig.Format == "" {
		config.AudioConfig.Format = "mp3"
	}
	if config.AudioConfig.SampleRate == 0 {
		config.AudioConfig.SampleRate = 24000
	}
	task := model.InitTask(constant.TaskPlatform(pinned.Plugin.Meta.Key), info)
	task.Action, task.Status, task.Progress = "audio_generation", model.TaskStatusSubmitted, "0%"
	if info.Billing != nil {
		task.Quota = info.Billing.GetPreConsumedQuota()
	}
	task.TokenName = c.GetString("token_name")
	task.PrivateData.Key = ""
	task.PrivateData.InternalAsync = true
	task.PrivateData.AudioQueue = &model.AsyncAudioQueueState{PluginKey: pinned.Plugin.Meta.Key, PluginVersion: pinned.Plugin.Meta.Version, Format: config.AudioConfig.Format, SampleRate: config.AudioConfig.SampleRate}
	task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
	task.PrivateData.RequestMethod, task.PrivateData.RequestPath, task.PrivateData.RequestContentType = http.MethodPost, c.Request.URL.Path, "application/json"
	task.PrivateData.BillingSource, task.PrivateData.SubscriptionId = info.BillingSource, info.SubscriptionId
	task.PrivateData.BillingState, task.PrivateData.UsageAccountingMode = service.TaskBillingStatePending, model.TaskUsageAccountingFinal
	task.PrivateData.PreConsumedQuota = task.Quota
	task.PrivateData.BillingContext = &model.TaskBillingContext{ModelPrice: info.PriceData.ModelPrice, FixedPriceUnit: info.PriceData.FixedPriceUnit, GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio, ModelRatio: info.PriceData.ModelRatio, OtherRatios: info.PriceData.OtherRatios(), OriginModelName: info.OriginModelName, PerCallBilling: info.PriceData.UsePrice, TieredSnapshot: info.TieredBillingSnapshot}
	dir := filepath.Join(common.GetAsyncImageRequestStoragePath(), "audio")
	if err = os.MkdirAll(dir, 0700); err == nil {
		task.PrivateData.RequestBodyPath = filepath.Join(dir, task.TaskID+".json")
		err = os.WriteFile(task.PrivateData.RequestBodyPath, body, 0600)
	}
	if err == nil {
		err = service.InsertImageTaskWithReferences(task)
	}
	if err != nil {
		if task.PrivateData.RequestBodyPath != "" {
			_ = os.Remove(task.PrivateData.RequestBodyPath)
		}
		respondPluginProtocolError(c, 500, "queue_unavailable", "Failed to retain audio task")
		return
	}
	durable = true
	c.JSON(http.StatusOK, gin.H{"task_id": task.TaskID, "status": "submitted", "status_url": "/v1/audio/tasks/" + task.TaskID, "content_url": service.AudioTaskContentURL(task.TaskID)})
}

func StartAsyncAudioWorkerLoop() {
	for range 2 {
		go func() {
			for {
				runAsyncAudioWorkerOnce()
				time.Sleep(2 * time.Second)
			}
		}()
	}
	go func() {
		for {
			if err := service.CleanupExpiredAudioTasks(context.Background()); err != nil {
				common.SysError("audio cleanup: " + err.Error())
			}
			time.Sleep(10 * time.Minute)
		}
	}()
}

func runAsyncAudioWorkerOnce() {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("audio worker panic: %v", recovered))
		}
	}()
	tasks, err := model.GetAsyncAudioQueueTasks(context.Background())
	if err != nil {
		common.SysError("audio queue: " + err.Error())
		return
	}
	for _, task := range tasks {
		if task.PrivateData.AudioQueue == nil || task.PrivateData.AudioQueue.LeaseUntil > time.Now().Unix() {
			continue
		}
		won, err := model.ClaimAsyncAudioTask(task)
		if err != nil {
			common.SysError("claim audio task: " + err.Error())
			continue
		}
		if !won {
			continue
		}
		if err = executeAsyncAudioTask(task); err != nil {
			logger.LogError(context.Background(), "audio task "+task.TaskID+": "+err.Error())
		}
		return
	}
}

func executeAsyncAudioTask(task *model.Task) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	queue := task.PrivateData.AudioQueue
	if queue == nil {
		return fmt.Errorf("audio queue metadata missing")
	}
	if task.FailReason != "" {
		return finishAsyncAudioFailure(task, task.FailReason)
	}
	if !queue.Accepted {
		if queue.Submitting {
			return finishAsyncAudioFailure(task, "Audio submission outcome is unknown; automatic resubmission stopped")
		}
		if err := submitQueuedAudio(ctx, task); err != nil {
			return finishAsyncAudioFailure(task, err.Error())
		}
		// An accepted snapshot is saved before any storage or billing. A failed
		// checkpoint leaves Submitting set on disk and will never re-POST.
		if err := model.SaveAsyncAudioTask(ctx, task); err != nil {
			return err
		}
	}
	if task.FailReason != "" {
		return finishAsyncAudioFailure(task, task.FailReason)
	}
	if task.PrivateData.LocalPath == "" {
		if err := service.StoreAsyncAudioResult(ctx, task); err != nil {
			return finishAsyncAudioFailure(task, err.Error())
		}
		if err := model.SaveAsyncAudioTask(ctx, task); err != nil {
			return err
		}
	}
	if task.PrivateData.BillingState != service.TaskBillingStateSettled {
		if err := service.AdjustTaskQuotaForFinalSettlement(ctx, task, task.PrivateData.ActualQuota, "audio usage settlement"); err != nil {
			return err
		}
		if err := service.ConfirmTaskBillingSettled(ctx, task, task.Quota, "async audio stored", queue.QuotaClamp); err != nil {
			return err
		}
	}
	task.Status, task.Progress, task.FinishTime = model.TaskStatusSuccess, "100%", time.Now().Unix()
	if err := model.SaveAsyncAudioTask(ctx, task); err != nil {
		return err
	}
	_ = os.Remove(task.PrivateData.RequestBodyPath)
	return nil
}

func submitQueuedAudio(ctx context.Context, task *model.Task) error {
	queue := task.PrivateData.AudioQueue
	plugin, ok := pluginruntime.DefaultRegistry.Get(queue.PluginKey)
	if !constant.TaskPluginEnabled || !ok || plugin.Meta.Version != queue.PluginVersion {
		return fmt.Errorf("Queued audio plugin is unavailable or changed")
	}
	body, err := os.ReadFile(task.PrivateData.RequestBodyPath)
	if err != nil {
		return fmt.Errorf("Retained audio request is unavailable")
	}
	c, _, err := buildInternalAsyncBaseContext(ctx, task, body)
	if err != nil {
		return err
	}
	defer common.CleanupBodyStorage(c)
	defer service.ReleaseAutoSwitchAttempt(c)
	var request any
	if err = common.DecodeJson(c.Request.Body, &request); err != nil {
		return err
	}
	c.Set("task_request", request)
	c.Set("task_action", task.Action)
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin, Generation: pluginruntime.DefaultRegistry.Generation()})
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil || channel.Status != common.ChannelStatusEnabled {
		return fmt.Errorf("Queued audio channel is unavailable")
	}
	if channel.GetSetting().TaskPluginKey != queue.PluginKey {
		return fmt.Errorf("Queued audio plugin binding changed")
	}
	if apiErr := middleware.SetupContextForSelectedChannel(c, channel, task.Properties.OriginModelName); apiErr != nil {
		return fmt.Errorf("Queued audio channel cannot be initialized")
	}
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		return err
	}
	info.PublicTaskID, info.Action = task.TaskID, task.Action
	info.OriginModelName = task.Properties.OriginModelName
	info.InitChannelMeta(c)
	info.UpstreamModelName = task.Properties.UpstreamModelName
	bc := task.PrivateData.BillingContext
	if bc == nil {
		return fmt.Errorf("Audio billing snapshot missing")
	}
	info.PriceData = types.PriceData{ModelPrice: bc.ModelPrice, FixedPriceUnit: bc.FixedPriceUnit, ModelRatio: bc.ModelRatio, UsePrice: bc.PerCallBilling, Quota: task.Quota}
	info.PriceData.GroupRatioInfo.GroupRatio = bc.GroupRatio
	info.PriceData.ReplaceOtherRatios(bc.OtherRatios)
	info.TieredBillingSnapshot = bc.TieredSnapshot
	queue.Submitting = true
	if err = model.SaveAsyncAudioTask(ctx, task); err != nil {
		return err
	}
	result, taskErr := relay.RelayQueuedTaskSubmit(c, info)
	if taskErr != nil {
		return fmt.Errorf("%s", taskSubmissionAPIError(taskErr).MaskSensitiveErrorWithStatusCode())
	}
	if result == nil || result.Immediate == nil {
		return fmt.Errorf("Audio plugin must return a completed result")
	}
	if result.Immediate.Status != model.TaskStatusSuccess && result.Immediate.Status != model.TaskStatusFailure {
		return fmt.Errorf("Invalid audio completion status")
	}
	queue.Accepted, queue.Submitting = true, false
	queue.QuotaClamp = info.QuotaClamp
	task.Data, task.PrivateData.ActualQuota = result.TaskData, result.Quota
	task.PrivateData.UpstreamTaskID, task.PrivateData.PluginState = result.UpstreamTaskID, result.PluginState
	bc.TieredSnapshot = info.TieredBillingSnapshot
	if result.Immediate.Status == model.TaskStatusFailure {
		task.FailReason = "Audio generation failed"
	}
	return nil
}

func finishAsyncAudioFailure(task *model.Task, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	task.FailReason = reason
	if err := model.SaveAsyncAudioTask(ctx, task); err != nil {
		return err
	}
	if err := service.RefundTaskQuota(ctx, task, reason); err != nil {
		return err
	}
	task.Status, task.Progress, task.FinishTime = model.TaskStatusFailure, "100%", time.Now().Unix()
	task.Data = nil
	if err := model.SaveAsyncAudioTask(ctx, task); err != nil {
		return err
	}
	_ = os.Remove(task.PrivateData.RequestBodyPath)
	return nil
}

func audioTaskForRequest(c *gin.Context) *model.Task {
	task, exists, err := model.GetByTaskId(c.GetInt("id"), c.Param("task_id"))
	if err != nil {
		respondPluginProtocolError(c, 500, "task_query_failed", "Failed to query audio task")
		return nil
	}
	if c.GetInt("id") <= 0 || !exists || task.PrivateData.AudioQueue == nil {
		respondPluginProtocolError(c, 404, "task_not_found", "Audio task not found")
		return nil
	}
	return task
}

func GetAudioTaskContent(c *gin.Context) {
	if task := audioTaskForRequest(c); task != nil {
		service.ServeAudioTaskContent(c, task)
	}
}

func GetAudioTask(c *gin.Context) {
	task := audioTaskForRequest(c)
	if task == nil {
		return
	}
	c.Header("Cache-Control", "private, no-store")
	status := "submitted"
	var result any
	response := gin.H{"task_id": task.TaskID, "status": status, "content_url": service.AudioTaskContentURL(task.TaskID), "expires_at": task.PrivateData.ExpiresAt}
	switch task.Status {
	case model.TaskStatusInProgress:
		status = "in_progress"
	case model.TaskStatusFailure:
		status = "failed"
		publicError := task.UserError()
		response["error"] = gin.H{"code": publicError.Code, "message": publicError.Summary()}
	case model.TaskStatusSuccess:
		status = "succeeded"
		_, fileErr := os.Stat(task.PrivateData.LocalPath)
		if task.PrivateData.ExpiresAt <= time.Now().Unix() || fileErr != nil {
			status = "expired"
		} else if err := common.Unmarshal(task.Data, &result); err == nil {
			response["result"] = result
		} else {
			respondPluginProtocolError(c, 500, "audio_result_invalid", "Saved audio metadata is unavailable")
			return
		}
	}
	response["status"] = status
	c.JSON(http.StatusOK, response)
}
