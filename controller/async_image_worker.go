package controller

import (
	"bytes"
	"context"
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

const asyncImageWorkerContextKey = "async_image_worker"

var internalAsyncImageWorkerSerial atomic.Int64
var internalAsyncImageWorkerGeneration atomic.Int64

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
		if err := executeInternalAsyncImageTask(context.Background(), task); err != nil {
			logger.LogError(context.Background(), fmt.Sprintf("internal async image worker %d task %s failed: %s", workerID, task.TaskID, err.Error()))
		}
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
	imageResponseBody, channelID, proxy, err := runInternalAsyncImageRelay(task)
	if err != nil {
		failInternalAsyncImageTask(ctx, task, err.Error())
		return err
	}

	var imageResp dto.ImageResponse
	if err = common.Unmarshal(imageResponseBody, &imageResp); err != nil {
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
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"
	task.FailReason = ""
	if task.StartTime == 0 {
		task.StartTime = now
	}
	task.FinishTime = now
	task.Data = buildStoredAsyncImageTaskData(imageResp, task.PrivateData.ResultURL)
	snapshotPath := clearInternalAsyncImageRequestSnapshotFields(task)
	won, updateErr := task.UpdateWithStatus(model.TaskStatusInProgress)
	if updateErr != nil {
		return updateErr
	}
	if !won {
		logger.LogWarn(ctx, fmt.Sprintf("internal async image task %s already transitioned, skip settle", task.TaskID))
		return nil
	}
	removeInternalAsyncImageRequestSnapshot(snapshotPath)
	service.ConfirmTaskBillingSettled(ctx, task, task.Quota, "async image stored")
	return nil
}

func runInternalAsyncImageRelay(task *model.Task) ([]byte, int, string, error) {
	body, err := os.ReadFile(task.PrivateData.RequestBodyPath)
	if err != nil {
		return nil, 0, "", fmt.Errorf("read request snapshot failed: %w", err)
	}

	var lastErr *types.NewAPIError
	var lastChannel *model.Channel
	var lastProxy string
	retryParam := &service.RetryParam{
		Ctx:        nil,
		TokenGroup: task.Group,
		ModelName:  task.Properties.OriginModelName,
		Retry:      common.GetPointer(0),
	}

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		c, recorder, imageReq, relayInfo, err := buildInternalAsyncImageContext(task, body)
		if err != nil {
			return nil, 0, "", err
		}
		retryParam.Ctx = c
		channel, channelErr := getChannel(c, relayInfo, retryParam)
		if channelErr != nil {
			lastErr = channelErr
			break
		}
		lastChannel = channel
		lastProxy = channel.GetSetting().Proxy
		addUsedChannel(c, channel.Id)
		relayInfo.Request = imageReq
		newAPIError := relay.ImageHelper(c, relayInfo)
		if newAPIError == nil {
			return recorder.Body.Bytes(), channel.Id, lastProxy, nil
		}
		lastErr = service.NormalizeViolationFeeError(newAPIError)
		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), lastErr)
		if !shouldRetry(c, lastErr, common.RetryTimes-retryParam.GetRetry()) {
			break
		}
	}

	if lastErr != nil {
		if lastChannel != nil {
			return nil, lastChannel.Id, lastProxy, lastErr
		}
		return nil, 0, "", lastErr
	}
	return nil, 0, "", fmt.Errorf("internal async image relay failed")
}

func buildInternalAsyncImageContext(task *model.Task, body []byte) (*gin.Context, *httptest.ResponseRecorder, *dto.ImageRequest, *relaycommon.RelayInfo, error) {
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
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if task.PrivateData.RequestContentType != "" {
		req.Header.Set("Content-Type", task.PrivateData.RequestContentType)
	}
	req.ContentLength = int64(len(body))
	c.Request = req
	c.Set(common.KeyRequestBody, body)
	c.Set("async_image_worker", true)
	c.Set(asyncImageWorkerContextKey, true)
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
	return c, recorder, imageReq, relayInfo, nil
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

func buildStoredAsyncImageTaskData(resp dto.ImageResponse, resultURL string) []byte {
	data := dto.ImageResponse{
		Created: resp.Created,
		Data:    make([]dto.ImageData, 0, 1),
	}
	item := dto.ImageData{Url: resultURL}
	if len(resp.Data) > 0 {
		item.RevisedPrompt = resp.Data[0].RevisedPrompt
	}
	data.Data = append(data.Data, item)
	body, err := common.Marshal(data)
	if err != nil {
		return nil
	}
	return body
}

func failInternalAsyncImageTask(ctx context.Context, task *model.Task, reason string) {
	now := time.Now().Unix()
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = reason
	if task.StartTime == 0 {
		task.StartTime = now
	}
	task.FinishTime = now
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
	service.RefundTaskQuota(ctx, task, reason)
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
	return snapshotPath
}

func removeInternalAsyncImageRequestSnapshot(snapshotPath string) {
	if snapshotPath != "" {
		_ = os.Remove(snapshotPath)
	}
}
