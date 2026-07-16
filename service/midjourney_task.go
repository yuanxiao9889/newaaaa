package service

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
)

func NewMidjourneyAsyncTask(mjTask *model.Midjourney, relayInfo *relaycommon.RelayInfo, priceData types.PriceData) *model.Task {
	if mjTask == nil || mjTask.MjId == "" {
		return nil
	}

	now := time.Now().Unix()
	modelName := CovertMjpActionToModelName(mjTask.Action)
	group := ""
	tokenId := 0
	subscriptionId := 0
	billingSource := BillingSourceWallet
	upstreamModelName := ""
	channelId := mjTask.ChannelId
	if relayInfo != nil {
		group = relayInfo.UsingGroup
		tokenId = relayInfo.TokenId
		subscriptionId = relayInfo.SubscriptionId
		if relayInfo.BillingSource != "" {
			billingSource = relayInfo.BillingSource
		}
		if relayInfo.OriginModelName != "" {
			modelName = relayInfo.OriginModelName
		}
		if relayInfo.ChannelMeta != nil {
			if relayInfo.UpstreamModelName != "" {
				upstreamModelName = relayInfo.UpstreamModelName
			}
			if channelId == 0 {
				channelId = relayInfo.ChannelId
			}
		}
	}

	task := &model.Task{
		TaskID:     mjTask.MjId,
		Platform:   constant.TaskPlatformMidjourney,
		UserId:     mjTask.UserId,
		Group:      group,
		ChannelId:  channelId,
		Quota:      mjTask.Quota,
		Action:     mjTask.Action,
		Status:     MidjourneyStatusToTaskStatus(mjTask.Status, mjTask.Progress, mjTask.FailReason, mjTask.Code),
		FailReason: mjTask.FailReason,
		SubmitTime: normalizeMidjourneyTimestamp(mjTask.SubmitTime),
		StartTime:  normalizeMidjourneyTimestamp(mjTask.StartTime),
		FinishTime: normalizeMidjourneyTimestamp(mjTask.FinishTime),
		Progress:   midjourneyProgress(mjTask.Progress, mjTask.Status, mjTask.FailReason),
		Properties: model.Properties{
			Input:             mjTask.Prompt,
			UpstreamModelName: upstreamModelName,
			OriginModelName:   modelName,
		},
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID:      mjTask.MjId,
			UsageAccountingMode: model.TaskUsageAccountingSubmit,
			ResultURL:           midjourneyResultURL(mjTask.ImageUrl, mjTask.VideoUrl, nil),
			BillingSource:       billingSource,
			SubscriptionId:      subscriptionId,
			TokenId:             tokenId,
			BillingUpdatedAt:    now,
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      priceData.ModelPrice,
				GroupRatio:      priceData.GroupRatioInfo.GroupRatio,
				ModelRatio:      priceData.ModelRatio,
				OtherRatios:     cloneFloatRatios(priceData.OtherRatios),
				OriginModelName: modelName,
				PerCallBilling:  true,
			},
		},
	}
	if task.SubmitTime == 0 {
		task.SubmitTime = now
	}
	if task.Quota > 0 {
		task.PrivateData.PreConsumedQuota = task.Quota
		if task.Status == model.TaskStatusSuccess {
			task.PrivateData.BillingState = TaskBillingStateSettled
			task.PrivateData.ActualQuota = task.Quota
		} else {
			task.PrivateData.BillingState = TaskBillingStatePending
		}
	}
	task.SetData(mjTask)
	return task
}

func ApplyMidjourneyUpdateToAsyncTask(task *model.Task, update dto.MidjourneyDto) bool {
	if task == nil {
		return false
	}
	before := task.Snapshot()
	oldAction := task.Action
	oldSubmitTime := task.SubmitTime
	oldBillingState := task.PrivateData.BillingState
	oldActualQuota := task.PrivateData.ActualQuota

	if update.Action != "" {
		task.Action = update.Action
	}
	task.Status = MidjourneyStatusToTaskStatus(update.Status, update.Progress, update.FailReason, 0)
	task.Progress = midjourneyProgress(update.Progress, update.Status, update.FailReason)
	task.FailReason = update.FailReason
	if update.SubmitTime != 0 {
		task.SubmitTime = normalizeMidjourneyTimestamp(update.SubmitTime)
	}
	if update.StartTime != 0 {
		task.StartTime = normalizeMidjourneyTimestamp(update.StartTime)
	}
	if update.FinishTime != 0 {
		task.FinishTime = normalizeMidjourneyTimestamp(update.FinishTime)
	}
	if resultURL := midjourneyResultURL(update.ImageUrl, update.VideoUrl, update.VideoUrls); resultURL != "" {
		task.PrivateData.ResultURL = resultURL
	}
	if task.Status == model.TaskStatusSuccess && task.PrivateData.BillingState != TaskBillingStateRefunded {
		task.PrivateData.BillingState = TaskBillingStateSettled
		if task.PrivateData.ActualQuota == 0 {
			task.PrivateData.ActualQuota = task.Quota
		}
		task.PrivateData.BillingError = ""
		task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	}
	task.SetData(update)

	return !before.Equal(task.Snapshot()) ||
		oldAction != task.Action ||
		oldSubmitTime != task.SubmitTime ||
		oldBillingState != task.PrivateData.BillingState ||
		oldActualQuota != task.PrivateData.ActualQuota
}

func MidjourneyStatusToTaskStatus(status string, progress string, failReason string, code int) model.TaskStatus {
	normalizedStatus := strings.ToUpper(strings.TrimSpace(status))
	if failReason != "" || normalizedStatus == string(model.TaskStatusFailure) {
		return model.TaskStatusFailure
	}
	switch normalizedStatus {
	case string(model.TaskStatusSuccess):
		return model.TaskStatusSuccess
	case string(model.TaskStatusInProgress):
		return model.TaskStatusInProgress
	case string(model.TaskStatusQueued):
		return model.TaskStatusQueued
	case string(model.TaskStatusSubmitted):
		return model.TaskStatusSubmitted
	case string(model.TaskStatusNotStart):
		return model.TaskStatusNotStart
	}
	if progress == "100%" {
		return model.TaskStatusSuccess
	}
	if code == 22 {
		return model.TaskStatusQueued
	}
	if progress != "" && progress != "0%" {
		return model.TaskStatusInProgress
	}
	if code != 0 && code != 1 && code != 21 {
		return model.TaskStatusFailure
	}
	return model.TaskStatusSubmitted
}

func normalizeMidjourneyTimestamp(ts int64) int64 {
	if ts <= 0 {
		return 0
	}
	if ts > 1_000_000_000_000 {
		return ts / 1000
	}
	return ts
}

func midjourneyProgress(progress string, status string, failReason string) string {
	if failReason != "" || strings.EqualFold(status, string(model.TaskStatusFailure)) {
		return "100%"
	}
	if progress != "" {
		return progress
	}
	if strings.EqualFold(status, string(model.TaskStatusSuccess)) {
		return "100%"
	}
	return "0%"
}

func midjourneyResultURL(imageURL string, videoURL string, videoURLs []dto.ImgUrls) string {
	if imageURL != "" {
		return imageURL
	}
	if videoURL != "" {
		return videoURL
	}
	for _, item := range videoURLs {
		if item.Url != "" {
			return item.Url
		}
	}
	return ""
}

func cloneFloatRatios(input map[string]float64) map[string]float64 {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]float64, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
