package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	asyncImageWorkerContextKey      = "async_image_worker"
	suppressRelayErrorLogContextKey = "suppress_relay_error_log"
)

var internalAsyncImageWorkerSerial atomic.Int64
var internalAsyncImageWorkerGeneration atomic.Int64
var internalAsyncImageTimeoutSweepAt atomic.Int64

type internalAsyncChannelRetryDetail struct {
	Attempt     int    `json:"attempt"`
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"`
	ChannelType int    `json:"channel_type,omitempty"`
	Status      string `json:"status"`
	StatusCode  int    `json:"status_code,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Error       string `json:"error,omitempty"`
	AttemptedAt int64  `json:"attempted_at"`
}

func StartInternalAsyncImageWorkerLoop() {
	common.SysLog(fmt.Sprintf("internal async image worker supervisor started: workers=%d max_unfinished=%d", common.GetAsyncImageWorkerConcurrency(), common.GetAsyncImageMaxUnfinishedTasks()))
	go superviseInternalAsyncImageWorkers()
}

func superviseInternalAsyncImageWorkers() {
	currentWorkers := -1
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		targetWorkers := common.GetAsyncImageWorkerConcurrency()
		if targetWorkers != currentWorkers {
			generation := internalAsyncImageWorkerGeneration.Add(1)
			currentWorkers = targetWorkers
			common.SysLog(fmt.Sprintf("internal async image worker target updated: workers=%d max_unfinished=%d", targetWorkers, common.GetAsyncImageMaxUnfinishedTasks()))
			for slot := 0; slot < targetWorkers; slot++ {
				workerID := int(internalAsyncImageWorkerSerial.Add(1))
				go runInternalAsyncImageWorker(workerID, slot, generation)
			}
		}
		<-ticker.C
	}
}

func runInternalAsyncImageWorker(workerID int, slot int, generation int64) {
	for {
		if internalAsyncImageWorkerGeneration.Load() != generation {
			return
		}
		if slot >= common.GetAsyncImageWorkerConcurrency() {
			return
		}
		runInternalAsyncImageWorkerOnce(workerID)
		time.Sleep(2 * time.Second)
	}
}

func runInternalAsyncImageWorkerOnce(workerID int) {
	sweepTimedOutInternalAsyncImageTasks(context.Background())
	staleBefore := time.Now().Unix() - int64(common.GetAsyncImageWorkerStaleMinutes()*60)
	tasks := model.GetRunnableInternalAsyncImageTasks(1, staleBefore)
	if len(tasks) == 0 {
		return
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if !claimInternalAsyncImageTask(task) {
			continue
		}
		ctx := context.Background()
		cancel := func() {}
		if timeoutSeconds := common.GetAsyncImageTaskTimeoutSeconds(); timeoutSeconds > 0 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		}
		if err := executeInternalAsyncImageTask(ctx, task); err != nil {
			logger.LogError(context.Background(), fmt.Sprintf("internal async image worker %d task %s failed: %s", workerID, task.TaskID, err.Error()))
		}
		cancel()
	}
}

func sweepTimedOutInternalAsyncImageTasks(ctx context.Context) {
	now := time.Now().Unix()
	lastSweepAt := internalAsyncImageTimeoutSweepAt.Load()
	if now-lastSweepAt < 15 {
		return
	}
	if !internalAsyncImageTimeoutSweepAt.CompareAndSwap(lastSweepAt, now) {
		return
	}
	timeoutSeconds := common.GetAsyncImageTaskTimeoutSeconds()
	if timeoutSeconds <= 0 {
		return
	}
	cutoff := now - int64(timeoutSeconds)
	tasks := model.GetTimedOutInternalAsyncImageTasks(cutoff, 100)
	if len(tasks) == 0 {
		return
	}
	reason := fmt.Sprintf("async image task timed out after %d seconds", timeoutSeconds)
	for _, task := range tasks {
		if task == nil || !task.PrivateData.InternalAsync {
			continue
		}
		failInternalAsyncImageTask(ctx, task, reason)
	}
}

func claimInternalAsyncImageTask(task *model.Task) bool {
	oldStatus := task.Status
	now := time.Now().Unix()
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	if task.StartTime == 0 {
		task.StartTime = now
	}
	task.PrivateData.WorkerAttempts++
	task.PrivateData.WorkerHeartbeatAt = now
	won, err := task.UpdateWithStatus(oldStatus)
	if err != nil {
		logger.LogError(context.Background(), fmt.Sprintf("claim internal async image task %s failed: %s", task.TaskID, err.Error()))
		return false
	}
	return won
}

func executeInternalAsyncImageTask(ctx context.Context, task *model.Task) error {
	imageResponseBody, channelID, proxy, usage, err := runInternalAsyncImageRelay(ctx, task)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("async image task timed out after %d seconds", common.GetAsyncImageTaskTimeoutSeconds())
		}
		failInternalAsyncImageTask(ctx, task, err.Error())
		return err
	}

	imageResp, err := parseInternalAsyncImageResponse(task, imageResponseBody)
	if err != nil {
		failInternalAsyncImageTask(ctx, task, "parse image response failed: "+err.Error())
		return err
	}
	assetRef := firstImageAssetRef(imageResp)
	if assetRef == "" {
		err = fmt.Errorf("upstream returned no image result")
		failInternalAsyncImageTask(ctx, task, err.Error())
		return err
	}
	if err = service.StoreAsyncImageResult(task, proxy, assetRef); err != nil {
		failInternalAsyncImageTask(ctx, task, "store async image result failed: "+err.Error())
		return err
	}

	now := time.Now().Unix()
	task.ChannelId = channelID
	task.Progress = "100%"
	task.FailReason = ""
	if task.StartTime == 0 {
		task.StartTime = now
	}
	task.FinishTime = now
	task.Data = service.BuildStoredAsyncImageTaskData(imageResp, task.PrivateData.ResultURL)

	preSettlementQuota := task.Quota
	var quotaClamp *common.QuotaClamp
	if usage != nil {
		task.PrivateData.PromptTokens = usage.PromptTokens
		task.PrivateData.CompletionTokens = usage.CompletionTokens
		task.PrivateData.TotalTokens = usage.TotalTokens
		task.PrivateData.UsageDetails = service.BuildTaskUsageDetails(usage)
		actualQuota, clamp := service.CalculateTaskQuotaByUsageChecked(task, usage)
		quotaClamp = clamp
		if actualQuota > 0 {
			if err = service.AdjustTaskQuotaForFinalSettlement(ctx, task, actualQuota, "async image usage settlement"); err != nil {
				return fmt.Errorf("async image usage settlement failed: %w", err)
			}
		}
	}

	task.Status = model.TaskStatusSuccess
	snapshotPath := clearInternalAsyncImageRequestSnapshotFields(task)
	won, updateErr := task.UpdateWithStatus(model.TaskStatusInProgress)
	if updateErr != nil || !won {
		if task.Quota != preSettlementQuota {
			if rollbackErr := service.AdjustTaskQuotaForFinalSettlement(ctx, task, preSettlementQuota, "rollback after task status transition failure"); rollbackErr != nil {
				logger.LogError(ctx, fmt.Sprintf("internal async image task %s billing rollback failed: %s", task.TaskID, rollbackErr.Error()))
			}
		}
		if updateErr != nil {
			return updateErr
		}
		logger.LogWarn(ctx, fmt.Sprintf("internal async image task %s already transitioned, skip settle", task.TaskID))
		return nil
	}
	removeInternalAsyncImageRequestSnapshot(snapshotPath)
	if err = service.ConfirmTaskBillingSettled(ctx, task, task.Quota, "async image stored", quotaClamp); err != nil {
		return fmt.Errorf("confirm async image billing failed: %w", err)
	}
	return nil
}

func runInternalAsyncImageRelay(ctx context.Context, task *model.Task) ([]byte, int, string, *dto.Usage, error) {
	body, err := os.ReadFile(task.PrivateData.RequestBodyPath)
	if err != nil {
		return nil, 0, "", nil, fmt.Errorf("read request snapshot failed: %w", err)
	}

	var lastErr *types.NewAPIError
	var lastChannel *model.Channel
	var lastCtx *gin.Context
	var lastProxy string
	var lastUsage *dto.Usage
	retryPath := make([]string, 0, common.RetryTimes+1)
	retryDetails := make([]internalAsyncChannelRetryDetail, 0, common.RetryTimes+1)
	retryParam := &service.RetryParam{
		Ctx:        nil,
		TokenGroup: task.Group,
		ModelName:  task.Properties.OriginModelName,
		Retry:      common.GetPointer(0),
	}

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		c, recorder, channel, newAPIError, err := runInternalAsyncImageRelayOnce(ctx, task, body, retryParam)
		if err != nil {
			return nil, 0, "", nil, err
		}
		if c != nil {
			lastCtx = c
			if usageValue, ok := c.Get("async_image_usage"); ok {
				if usage, ok := usageValue.(*dto.Usage); ok {
					lastUsage = usage
				}
			}
		}
		if channel != nil {
			lastChannel = channel
			lastProxy = channel.GetSetting().Proxy
			retryPath = append(retryPath, fmt.Sprintf("%d", channel.Id))
			persistInternalAsyncChannelRetryPath(task, retryPath)
		}
		if newAPIError == nil {
			if channel == nil {
				return nil, 0, "", nil, fmt.Errorf("internal async image relay selected no channel")
			}
			setInternalAsyncChannelRetryPath(task, retryPath)
			return recorder.Body.Bytes(), channel.Id, lastProxy, lastUsage, nil
		}
		lastErr = service.NormalizeViolationFeeError(newAPIError)
		retryDetails = append(retryDetails, buildInternalAsyncChannelRetryDetail(retryParam.GetRetry()+1, channel, lastErr))
		if channel != nil {
			c.Set(suppressRelayErrorLogContextKey, true)
			c.Set("async_channel_retry_path", append([]string(nil), retryPath...))
			processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), lastErr)
		}
		if !shouldRetry(c, lastErr, common.RetryTimes-retryParam.GetRetry()) {
			break
		}
	}
	setInternalAsyncChannelRetryPath(task, retryPath)

	if lastErr != nil {
		recordInternalAsyncImageFinalErrorLog(lastCtx, task, lastChannel, lastErr, retryPath, retryDetails)
		if lastChannel != nil {
			return nil, lastChannel.Id, lastProxy, nil, lastErr
		}
		return nil, 0, "", nil, lastErr
	}
	return nil, 0, "", nil, fmt.Errorf("internal async image relay failed")
}

func runInternalAsyncImageRelayOnce(ctx context.Context, task *model.Task, body []byte, retryParam *service.RetryParam) (*gin.Context, *httptest.ResponseRecorder, *model.Channel, *types.NewAPIError, error) {
	if task.PrivateData.RequestRelayFormat == string(types.RelayFormatGemini) || task.Action == constant.TaskActionGeminiImage {
		return runInternalAsyncGeminiImageRelayOnce(ctx, task, body, retryParam)
	}
	return runInternalAsyncOpenAIImageRelayOnce(ctx, task, body, retryParam)
}

func runInternalAsyncOpenAIImageRelayOnce(ctx context.Context, task *model.Task, body []byte, retryParam *service.RetryParam) (*gin.Context, *httptest.ResponseRecorder, *model.Channel, *types.NewAPIError, error) {
	c, recorder, imageReq, relayInfo, err := buildInternalAsyncImageContext(ctx, task, body)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	retryParam.Ctx = c
	channel, channelErr := getChannel(c, relayInfo, retryParam)
	if channelErr != nil {
		return c, recorder, channel, channelErr, nil
	}
	addUsedChannel(c, channel.Id)
	relayInfo.Request = imageReq
	return c, recorder, channel, relay.ImageHelper(c, relayInfo), nil
}

func runInternalAsyncGeminiImageRelayOnce(ctx context.Context, task *model.Task, body []byte, retryParam *service.RetryParam) (*gin.Context, *httptest.ResponseRecorder, *model.Channel, *types.NewAPIError, error) {
	c, recorder, geminiReq, relayInfo, err := buildInternalAsyncGeminiImageContext(ctx, task, body)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	retryParam.Ctx = c
	channel, channelErr := getChannel(c, relayInfo, retryParam)
	if channelErr != nil {
		return c, recorder, channel, channelErr, nil
	}
	addUsedChannel(c, channel.Id)
	relayInfo.Request = geminiReq
	return c, recorder, channel, geminiRelayHandler(c, relayInfo), nil
}

func buildInternalAsyncChannelRetryDetail(attempt int, channel *model.Channel, err *types.NewAPIError) internalAsyncChannelRetryDetail {
	detail := internalAsyncChannelRetryDetail{
		Attempt:     attempt,
		Status:      "error",
		AttemptedAt: common.GetTimestamp(),
	}
	if channel != nil {
		detail.ChannelID = channel.Id
		detail.ChannelName = channel.Name
		detail.ChannelType = channel.Type
	}
	if err != nil {
		detail.StatusCode = err.StatusCode
		detail.ErrorType = string(err.GetErrorType())
		detail.ErrorCode = string(err.GetErrorCode())
		detail.Error = err.MaskSensitiveErrorWithStatusCode()
	}
	return detail
}

func buildInternalAsyncImageContext(ctx context.Context, task *model.Task, body []byte) (*gin.Context, *httptest.ResponseRecorder, *dto.ImageRequest, *relaycommon.RelayInfo, error) {
	c, recorder, err := buildInternalAsyncBaseContext(ctx, task, body)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	relayMode := relayconstant.RelayModeImagesGenerations
	if task.Action == constant.TaskActionImageEdit {
		relayMode = relayconstant.RelayModeImagesEdits
	}
	c.Set("relay_mode", relayMode)
	imageReq, err := helper.GetAndValidOpenAIImageRequest(c, relayMode)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if task.Properties.OriginModelName == "" {
		task.Properties.OriginModelName = imageReq.Model
		common.SetContextKey(c, constant.ContextKeyOriginalModel, imageReq.Model)
	}
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAIImage, imageReq, nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	prepareInternalAsyncRelayInfo(task, relayInfo)
	return c, recorder, imageReq, relayInfo, nil
}

func buildInternalAsyncGeminiImageContext(ctx context.Context, task *model.Task, body []byte) (*gin.Context, *httptest.ResponseRecorder, *dto.GeminiChatRequest, *relaycommon.RelayInfo, error) {
	c, recorder, err := buildInternalAsyncBaseContext(ctx, task, body)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	c.Set("relay_mode", relayconstant.RelayModeGemini)
	geminiReq, err := helper.GetAndValidateGeminiRequest(c)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatGemini, geminiReq, nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	prepareInternalAsyncRelayInfo(task, relayInfo)
	return c, recorder, geminiReq, relayInfo, nil
}

func buildInternalAsyncBaseContext(ctx context.Context, task *model.Task, body []byte) (*gin.Context, *httptest.ResponseRecorder, error) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	method := task.PrivateData.RequestMethod
	if method == "" {
		method = http.MethodPost
	}
	path := task.PrivateData.RequestPath
	if path == "" {
		if task.Action == constant.TaskActionImageEdit {
			path = "/v1/images/edits"
		} else {
			path = "/v1/images/generations"
		}
	}
	target := path
	if task.PrivateData.RequestQuery != "" {
		target += "?" + task.PrivateData.RequestQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	if task.PrivateData.RequestContentType != "" {
		req.Header.Set("Content-Type", task.PrivateData.RequestContentType)
	}
	req.ContentLength = int64(len(body))
	c.Request = req
	c.Set(common.KeyRequestBody, body)
	c.Set("async_image_worker", true)
	c.Set(asyncImageWorkerContextKey, true)
	c.Set(suppressRelayErrorLogContextKey, true)
	c.Set("id", task.UserId)
	c.Set("token_id", task.PrivateData.TokenId)
	c.Set("group", task.Group)
	c.Set("user_group", task.Group)
	common.SetContextKey(c, constant.ContextKeyUserId, task.UserId)
	common.SetContextKey(c, constant.ContextKeyTokenId, task.PrivateData.TokenId)
	common.SetContextKey(c, constant.ContextKeyTokenKey, "async-image-worker")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, task.Group)
	common.SetContextKey(c, constant.ContextKeyUserGroup, task.Group)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, task.Group)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, task.Properties.OriginModelName)
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
	c.Set(common.RequestIdKey, task.TaskID)
	setInternalAsyncIdentity(c, task)
	return c, recorder, nil
}

func recordInternalAsyncImageFinalErrorLog(c *gin.Context, task *model.Task, channel *model.Channel, err *types.NewAPIError, retryPath []string, retryDetails []internalAsyncChannelRetryDetail) {
	if c == nil || task == nil || err == nil || !constant.ErrorLogEnabled || !types.IsRecordErrorLog(err) {
		return
	}
	path := append([]string(nil), retryPath...)
	content := internalAsyncImageFinalErrorContent(err, path)
	channelID := 0
	channelName := ""
	channelType := 0
	if channel != nil {
		channelID = channel.Id
		channelName = channel.Name
		channelType = channel.Type
	}
	modelName := task.Properties.OriginModelName
	if modelName == "" && task.PrivateData.BillingContext != nil {
		modelName = task.PrivateData.BillingContext.OriginModelName
	}
	other := map[string]interface{}{
		"error_type":               err.GetErrorType(),
		"error_code":               err.GetErrorCode(),
		"status_code":              err.StatusCode,
		"channel_id":               channelID,
		"channel_name":             channelName,
		"channel_type":             channelType,
		"is_task":                  true,
		"async_task":               true,
		"task_id":                  task.TaskID,
		"async_channel_retry_path": path,
		"admin_info": map[string]interface{}{
			"use_channel": path,
		},
	}
	if len(retryDetails) > 0 {
		other["async_channel_retry_details"] = retryDetails
	}
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	} else if task.PrivateData.RequestPath != "" {
		other["request_path"] = task.PrivateData.RequestPath
	}
	c.Set(common.RequestIdKey, task.TaskID)
	c.Set("id", task.UserId)
	c.Set("token_id", task.PrivateData.TokenId)
	c.Set("group", task.Group)
	if channelID > 0 {
		c.Set("channel_id", channelID)
		c.Set("channel_name", channelName)
		c.Set("channel_type", channelType)
	}
	useTimeSeconds := internalAsyncImageFinalErrorUseTime(task, c)
	model.RecordErrorLog(c, task.UserId, channelID, modelName, c.GetString("token_name"), content, task.PrivateData.TokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), task.Group, other)
}

func internalAsyncImageFinalErrorContent(err *types.NewAPIError, retryPath []string) string {
	content := err.MaskSensitiveErrorWithStatusCode()
	if len(retryPath) == 0 {
		return content
	}
	return fmt.Sprintf("%s; retry: %s", content, strings.Join(retryPath, " -> "))
}

func internalAsyncImageFinalErrorUseTime(task *model.Task, c *gin.Context) int {
	now := time.Now()
	if task != nil && task.StartTime > 0 {
		elapsed := now.Unix() - task.StartTime
		if elapsed > 0 {
			return int(elapsed)
		}
		return 0
	}
	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		return 0
	}
	elapsed := int(now.Sub(startTime).Seconds())
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func setInternalAsyncIdentity(c *gin.Context, task *model.Task) {
	if c == nil || task == nil {
		return
	}
	if task.UserId > 0 {
		if username, err := model.GetUsernameById(task.UserId, false); err == nil && username != "" {
			c.Set("username", username)
		}
	}
	if task.PrivateData.TokenId > 0 {
		if token, err := model.GetTokenById(task.PrivateData.TokenId); err == nil && token != nil && token.Name != "" {
			c.Set("token_name", token.Name)
		}
	}
}

func setInternalAsyncChannelRetryPath(task *model.Task, retryPath []string) {
	if task == nil || len(retryPath) == 0 {
		return
	}
	task.PrivateData.ChannelRetryPath = append([]string(nil), retryPath...)
}

func persistInternalAsyncChannelRetryPath(task *model.Task, retryPath []string) {
	setInternalAsyncChannelRetryPath(task, retryPath)
	if task == nil || task.ID == 0 || len(retryPath) == 0 {
		return
	}
	if err := model.UpdateTaskPrivateData(task.ID, task.PrivateData); err != nil {
		logger.LogError(context.Background(), fmt.Sprintf("persist internal async image task %s retry path failed: %s", task.TaskID, err.Error()))
	}
}

func prepareInternalAsyncRelayInfo(task *model.Task, relayInfo *relaycommon.RelayInfo) {
	relayInfo.ChannelMeta = &relaycommon.ChannelMeta{}
	relayInfo.PriceData.OtherRatios = map[string]float64{}
	if bc := task.PrivateData.BillingContext; bc != nil {
		relayInfo.PriceData.ModelPrice = bc.ModelPrice
		relayInfo.PriceData.GroupRatioInfo.GroupRatio = bc.GroupRatio
		relayInfo.PriceData.ModelRatio = bc.ModelRatio
		relayInfo.PriceData.OtherRatios = bc.OtherRatios
	}
	if relayInfo.PriceData.OtherRatios == nil {
		relayInfo.PriceData.OtherRatios = map[string]float64{}
	}
	relayInfo.PriceData.Quota = task.Quota
	relayInfo.PriceData.QuotaToPreConsume = task.Quota
}

func parseInternalAsyncImageResponse(task *model.Task, body []byte) (dto.ImageResponse, error) {
	if task != nil && (task.PrivateData.RequestRelayFormat == string(types.RelayFormatGemini) || task.Action == constant.TaskActionGeminiImage) {
		return geminiImageResponseToOpenAIImageResponse(body)
	}
	var imageResp dto.ImageResponse
	err := common.Unmarshal(body, &imageResp)
	return imageResp, err
}

func geminiImageResponseToOpenAIImageResponse(body []byte) (dto.ImageResponse, error) {
	var geminiResp dto.GeminiChatResponse
	if err := common.Unmarshal(body, &geminiResp); err != nil {
		return dto.ImageResponse{}, err
	}
	imageResp := dto.ImageResponse{
		Created: common.GetTimestamp(),
		Data:    make([]dto.ImageData, 0, 1),
	}
	for _, candidate := range geminiResp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || !strings.HasPrefix(strings.ToLower(part.InlineData.MimeType), "image/") || strings.TrimSpace(part.InlineData.Data) == "" {
				continue
			}
			imageResp.Data = append(imageResp.Data, dto.ImageData{
				B64Json: fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data),
			})
			return imageResp, nil
		}
	}
	return imageResp, fmt.Errorf("gemini response returned no image inlineData")
}

func firstImageAssetRef(resp dto.ImageResponse) string {
	for _, item := range resp.Data {
		if strings.TrimSpace(item.Url) != "" {
			return strings.TrimSpace(item.Url)
		}
		if strings.TrimSpace(item.B64Json) != "" {
			return strings.TrimSpace(item.B64Json)
		}
	}
	return ""
}

func failInternalAsyncImageTask(ctx context.Context, task *model.Task, reason string) {
	now := time.Now().Unix()
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = formatInternalAsyncFailureReason(task, reason)
	if task.StartTime == 0 {
		task.StartTime = now
	}
	task.FinishTime = now
	task.Data = service.BuildFailedAsyncImageTaskData(task)
	snapshotPath := clearInternalAsyncImageRequestSnapshotFields(task)
	won, err := task.UpdateWithStatus(model.TaskStatusInProgress)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("fail internal async image task %s update failed: %s", task.TaskID, err.Error()))
		return
	}
	if !won {
		logger.LogWarn(ctx, fmt.Sprintf("internal async image task %s already transitioned, skip refund", task.TaskID))
		return
	}
	removeInternalAsyncImageRequestSnapshot(snapshotPath)
	if err := service.RefundTaskQuota(ctx, task, reason); err != nil {
		logger.LogError(ctx, fmt.Sprintf("refund internal async image task %s failed: %s", task.TaskID, err.Error()))
	}
}

func formatInternalAsyncFailureReason(task *model.Task, reason string) string {
	if task == nil || len(task.PrivateData.ChannelRetryPath) == 0 {
		return reason
	}
	if strings.Contains(reason, "Async Channel Retry Path:") {
		return reason
	}
	return fmt.Sprintf("%s\nAsync Channel Retry Path: %s", reason, strings.Join(task.PrivateData.ChannelRetryPath, " -> "))
}

func clearInternalAsyncImageRequestSnapshotFields(task *model.Task) string {
	if task == nil {
		return ""
	}
	snapshotPath := task.PrivateData.RequestBodyPath
	task.PrivateData.RequestBodyPath = ""
	task.PrivateData.RequestContentType = ""
	task.PrivateData.RequestBodySize = 0
	task.PrivateData.RequestMethod = ""
	task.PrivateData.RequestPath = ""
	task.PrivateData.RequestQuery = ""
	task.PrivateData.RequestRelayFormat = ""
	return snapshotPath
}

func removeInternalAsyncImageRequestSnapshot(snapshotPath string) {
	if snapshotPath != "" {
		_ = os.Remove(snapshotPath)
	}
}
