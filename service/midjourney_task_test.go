package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMidjourneyAsyncTaskCapturesBillingSnapshot(t *testing.T) {
	mjTask := &model.Midjourney{
		UserId:     7,
		Action:     constant.MjActionImagine,
		MjId:       "132",
		Prompt:     "cat",
		SubmitTime: 1700000000123,
		ChannelId:  9,
		Quota:      4200,
		Progress:   "0%",
		Code:       1,
	}
	relayInfo := &relaycommon.RelayInfo{
		UsingGroup:      "vip",
		BillingSource:   BillingSourceSubscription,
		SubscriptionId:  15,
		TokenId:         22,
		OriginModelName: "mj_imagine",
	}
	priceData := types.PriceData{
		ModelPrice: 0.042,
		ModelRatio: 0,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: 1.2,
		},
		UsePrice:    true,
		OtherRatios: map[string]float64{"mode": 2},
	}

	task := NewMidjourneyAsyncTask(mjTask, relayInfo, priceData)
	require.NotNil(t, task)

	assert.EqualValues(t, constant.TaskPlatformMidjourney, task.Platform)
	assert.Equal(t, "132", task.TaskID)
	assert.Equal(t, "132", task.PrivateData.UpstreamTaskID)
	assert.Equal(t, 7, task.UserId)
	assert.Equal(t, 9, task.ChannelId)
	assert.Equal(t, "vip", task.Group)
	assert.Equal(t, constant.MjActionImagine, task.Action)
	assert.EqualValues(t, model.TaskStatusSubmitted, task.Status)
	assert.EqualValues(t, 1700000000, task.SubmitTime)
	assert.Equal(t, 4200, task.Quota)
	assert.Equal(t, "cat", task.Properties.Input)
	assert.Equal(t, "mj_imagine", task.Properties.OriginModelName)
	assert.Equal(t, BillingSourceSubscription, task.PrivateData.BillingSource)
	assert.Equal(t, 15, task.PrivateData.SubscriptionId)
	assert.Equal(t, 22, task.PrivateData.TokenId)
	assert.Equal(t, TaskBillingStatePending, task.PrivateData.BillingState)
	assert.Equal(t, 4200, task.PrivateData.PreConsumedQuota)
	require.NotNil(t, task.PrivateData.BillingContext)
	assert.Equal(t, 0.042, task.PrivateData.BillingContext.ModelPrice)
	assert.Equal(t, 1.2, task.PrivateData.BillingContext.GroupRatio)
	assert.Equal(t, 2.0, task.PrivateData.BillingContext.OtherRatios["mode"])
	assert.True(t, task.PrivateData.BillingContext.PerCallBilling)
}

func TestApplyMidjourneyUpdateToAsyncTaskMapsSuccessResult(t *testing.T) {
	task := &model.Task{
		TaskID:     "132",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		Quota:      4200,
		SubmitTime: 1700000000,
		PrivateData: model.TaskPrivateData{
			BillingState:     TaskBillingStatePending,
			PreConsumedQuota: 4200,
		},
	}
	update := dto.MidjourneyDto{
		MjId:       "132",
		Action:     constant.MjActionImagine,
		Status:     "SUCCESS",
		Progress:   "100%",
		ImageUrl:   "https://img.example/result.png",
		SubmitTime: 1700000000123,
		StartTime:  1700000001123,
		FinishTime: 1700000002123,
		PromptEn:   "cat",
		Properties: &dto.Properties{FinalPrompt: "cat"},
	}

	changed := ApplyMidjourneyUpdateToAsyncTask(task, update)

	assert.True(t, changed)
	assert.Equal(t, constant.MjActionImagine, task.Action)
	assert.EqualValues(t, model.TaskStatusSuccess, task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.EqualValues(t, 1700000000, task.SubmitTime)
	assert.EqualValues(t, 1700000001, task.StartTime)
	assert.EqualValues(t, 1700000002, task.FinishTime)
	assert.Equal(t, "https://img.example/result.png", task.PrivateData.ResultURL)
	assert.Equal(t, TaskBillingStateSettled, task.PrivateData.BillingState)
	assert.Equal(t, 4200, task.PrivateData.ActualQuota)

	var data dto.MidjourneyDto
	require.NoError(t, common.Unmarshal(task.Data, &data))
	assert.Equal(t, "SUCCESS", data.Status)
	assert.Equal(t, "https://img.example/result.png", data.ImageUrl)
}

func TestApplyMidjourneyUpdateToAsyncTaskMapsFailure(t *testing.T) {
	task := &model.Task{
		TaskID:   "132",
		Status:   model.TaskStatusInProgress,
		Progress: "45%",
		Quota:    4200,
		PrivateData: model.TaskPrivateData{
			BillingState:     TaskBillingStatePending,
			PreConsumedQuota: 4200,
		},
	}
	update := dto.MidjourneyDto{
		MjId:       "132",
		Status:     "FAILURE",
		Progress:   "45%",
		FailReason: "upstream failed",
	}

	changed := ApplyMidjourneyUpdateToAsyncTask(task, update)

	assert.True(t, changed)
	assert.EqualValues(t, model.TaskStatusFailure, task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Equal(t, "upstream failed", task.FailReason)
	assert.Equal(t, TaskBillingStatePending, task.PrivateData.BillingState)
	assert.Equal(t, 0, task.PrivateData.ActualQuota)
}

func TestMidjourneyStatusToTaskStatusInfersProgress(t *testing.T) {
	assert.EqualValues(t, model.TaskStatusInProgress, MidjourneyStatusToTaskStatus("", "45%", "", 1))
	assert.EqualValues(t, model.TaskStatusQueued, MidjourneyStatusToTaskStatus("", "0%", "", 22))
}
