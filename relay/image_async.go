package relay

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func IsAsyncImageRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if c.GetBool("async_image_worker") {
		return false
	}
	asyncValue := strings.TrimSpace(c.Query("async"))
	if asyncValue == "" {
		return false
	}
	enabled, err := strconv.ParseBool(asyncValue)
	return err == nil && enabled
}

func IsAsyncGeminiImageRequest(c *gin.Context, request *dto.GeminiChatRequest) bool {
	if !IsAsyncImageRequest(c) || request == nil || request.IsStream(c) {
		return false
	}
	if !strings.Contains(c.Request.URL.Path, "generateContent") {
		return false
	}
	for _, modality := range request.GenerationConfig.ResponseModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "IMAGE") {
			return true
		}
	}
	return false
}

func validateAsyncImageTaskRequest(info *relaycommon.RelayInfo, request *dto.ImageRequest) *types.NewAPIError {
	if request == nil {
		return types.NewErrorWithStatusCode(fmt.Errorf("image request is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if request.N != nil && *request.N > 1 {
		return types.NewErrorWithStatusCode(fmt.Errorf("async image tasks only support n=1"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return nil
}

func validateAsyncGeminiImageTaskRequest(request *dto.GeminiChatRequest) *types.NewAPIError {
	if request == nil {
		return types.NewErrorWithStatusCode(fmt.Errorf("gemini image request is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if request.GenerationConfig.CandidateCount != nil && *request.GenerationConfig.CandidateCount > 1 {
		return types.NewErrorWithStatusCode(fmt.Errorf("async gemini image tasks only support candidateCount=1"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return nil
}

func asyncImageTaskAction(relayMode int) string {
	if relayMode == relayconstant.RelayModeImagesEdits {
		return constant.TaskActionImageEdit
	}
	return constant.TaskActionImageGenerate
}

func SubmitInternalAsyncImageTask(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ImageRequest) (ret *types.NewAPIError) {
	if err := validateAsyncImageTaskRequest(info, request); err != nil {
		return err
	}
	maxTasks := common.GetAsyncImageMaxUnfinishedTasks()
	if model.CountUnfinishedInternalAsyncImageTasks(maxTasks+1) >= maxTasks {
		return types.NewErrorWithStatusCode(fmt.Errorf("async image task queue is full"), types.ErrorCodeInvalidRequest, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}
	if err := preConsumeAsyncImageBilling(c, info); err != nil {
		return err
	}
	defer func() {
		if ret != nil && info != nil && info.Billing != nil {
			info.Billing.Refund(c)
		}
	}()

	if info.TaskRelayInfo == nil {
		info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	}
	info.TaskRelayInfo.PublicTaskID = model.GenerateTaskID()
	task := model.InitTask(constant.TaskPlatformInternalImage, info)
	task.Action = asyncImageTaskAction(info.RelayMode)
	task.Status = model.TaskStatusSubmitted
	task.Progress = taskcommon.ProgressSubmitted
	preConsumedQuota := info.FinalPreConsumedQuota
	if info.Billing != nil {
		preConsumedQuota = info.Billing.GetPreConsumedQuota()
	}
	if preConsumedQuota < 0 {
		preConsumedQuota = 0
	}
	task.Quota = preConsumedQuota
	task.PrivateData.AssetType = service.AsyncImageAssetType
	task.PrivateData.InternalAsync = true
	task.PrivateData.ResultURL = service.BuildAsyncImageContentURL(task.TaskID)
	task.PrivateData.RequestMethod = c.Request.Method
	task.PrivateData.RequestPath = c.Request.URL.Path
	task.PrivateData.RequestQuery = stripAsyncQuery(c.Request.URL.RawQuery)
	task.PrivateData.RequestContentType = c.Request.Header.Get("Content-Type")
	task.PrivateData.RequestRelayFormat = string(types.RelayFormatOpenAIImage)
	task.PrivateData.BillingSource = info.BillingSource
	task.PrivateData.SubscriptionId = info.SubscriptionId
	task.PrivateData.TokenId = info.TokenId
	task.PrivateData.BillingState = service.TaskBillingStatePending
	task.PrivateData.PreConsumedQuota = preConsumedQuota
	task.PrivateData.ActualQuota = 0
	task.PrivateData.BillingError = ""
	task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		ModelPrice:      info.PriceData.ModelPrice,
		GroupRatio:      info.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio:      info.PriceData.ModelRatio,
		OtherRatios:     info.PriceData.OtherRatios,
		OriginModelName: info.OriginModelName,
		PerCallBilling:  info.PriceData.UsePrice,
	}
	task.SetData(request)

	snapshotPath, snapshotSize, err := saveInternalAsyncImageRequestSnapshot(c, task.TaskID)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	task.PrivateData.RequestBodyPath = snapshotPath
	task.PrivateData.RequestBodySize = snapshotSize

	if err = task.Insert(); err != nil {
		_ = os.Remove(snapshotPath)
		return types.NewErrorWithStatusCode(err, types.ErrorCodeUpdateDataError, http.StatusInternalServerError)
	}

	c.JSON(http.StatusOK, dto.ImageAsyncSubmitResponse{
		TaskID:     task.TaskID,
		Status:     dto.ImageAsyncStatusSubmitted,
		StatusURL:  service.BuildAsyncImageStatusURL(task.TaskID),
		ContentURL: service.BuildAsyncImageContentURL(task.TaskID),
		ExpiresAt:  0,
	})
	return nil
}

func SubmitInternalAsyncGeminiImageTask(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (ret *types.NewAPIError) {
	if err := validateAsyncGeminiImageTaskRequest(request); err != nil {
		return err
	}
	maxTasks := common.GetAsyncImageMaxUnfinishedTasks()
	if model.CountUnfinishedInternalAsyncImageTasks(maxTasks+1) >= maxTasks {
		return types.NewErrorWithStatusCode(fmt.Errorf("async image task queue is full"), types.ErrorCodeInvalidRequest, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}
	if err := preConsumeAsyncImageBilling(c, info); err != nil {
		return err
	}
	defer func() {
		if ret != nil && info != nil && info.Billing != nil {
			info.Billing.Refund(c)
		}
	}()

	if info.TaskRelayInfo == nil {
		info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	}
	info.TaskRelayInfo.PublicTaskID = model.GenerateTaskID()
	task := model.InitTask(constant.TaskPlatformInternalImage, info)
	task.Action = constant.TaskActionGeminiImage
	task.Status = model.TaskStatusSubmitted
	task.Progress = taskcommon.ProgressSubmitted
	preConsumedQuota := info.FinalPreConsumedQuota
	if info.Billing != nil {
		preConsumedQuota = info.Billing.GetPreConsumedQuota()
	}
	if preConsumedQuota < 0 {
		preConsumedQuota = 0
	}
	task.Quota = preConsumedQuota
	task.PrivateData.AssetType = service.AsyncImageAssetType
	task.PrivateData.InternalAsync = true
	task.PrivateData.ResultURL = service.BuildAsyncImageContentURL(task.TaskID)
	task.PrivateData.RequestMethod = c.Request.Method
	task.PrivateData.RequestPath = c.Request.URL.Path
	task.PrivateData.RequestQuery = stripAsyncQuery(c.Request.URL.RawQuery)
	task.PrivateData.RequestContentType = c.Request.Header.Get("Content-Type")
	task.PrivateData.RequestRelayFormat = string(types.RelayFormatGemini)
	task.PrivateData.BillingSource = info.BillingSource
	task.PrivateData.SubscriptionId = info.SubscriptionId
	task.PrivateData.TokenId = info.TokenId
	task.PrivateData.BillingState = service.TaskBillingStatePending
	task.PrivateData.PreConsumedQuota = preConsumedQuota
	task.PrivateData.ActualQuota = 0
	task.PrivateData.BillingError = ""
	task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		ModelPrice:      info.PriceData.ModelPrice,
		GroupRatio:      info.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio:      info.PriceData.ModelRatio,
		OtherRatios:     info.PriceData.OtherRatios,
		OriginModelName: info.OriginModelName,
		PerCallBilling:  info.PriceData.UsePrice,
	}
	task.SetData(request)

	snapshotPath, snapshotSize, err := saveInternalAsyncImageRequestSnapshot(c, task.TaskID)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	task.PrivateData.RequestBodyPath = snapshotPath
	task.PrivateData.RequestBodySize = snapshotSize

	if err = task.Insert(); err != nil {
		_ = os.Remove(snapshotPath)
		return types.NewErrorWithStatusCode(err, types.ErrorCodeUpdateDataError, http.StatusInternalServerError)
	}

	c.JSON(http.StatusOK, dto.ImageAsyncSubmitResponse{
		TaskID:     task.TaskID,
		Status:     dto.ImageAsyncStatusSubmitted,
		StatusURL:  service.BuildAsyncImageStatusURL(task.TaskID),
		ContentURL: service.BuildAsyncImageContentURL(task.TaskID),
		ExpiresAt:  0,
	})
	return nil
}

func saveInternalAsyncImageRequestSnapshot(c *gin.Context, taskID string) (string, int64, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return "", 0, err
	}
	dir := filepath.Clean(common.GetAsyncImageRequestStoragePath())
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", 0, err
	}
	if _, err = storage.Seek(0, 0); err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, taskID+".body")
	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", 0, err
	}
	size, copyErr := file.ReadFrom(storage)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return "", 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", 0, closeErr
	}
	if _, err = storage.Seek(0, 0); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	if err = os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	return path, size, nil
}

func stripAsyncQuery(rawQuery string) string {
	if strings.TrimSpace(rawQuery) == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		key := part
		if idx := strings.Index(part, "="); idx >= 0 {
			key = part[:idx]
		}
		if strings.EqualFold(key, "async") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&")
}
