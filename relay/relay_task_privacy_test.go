package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserTaskModelDtoHidesInternalChannelInfo(t *testing.T) {
	task := &model.Task{
		ID:          1,
		TaskID:      "task_public",
		Platform:    constant.TaskPlatform("suno"),
		UserId:      7,
		Group:       "default",
		TokenId:     23,
		TokenName:   "task-token",
		ChannelId:   12,
		ChannelName: "upstream-a",
		Status:      model.TaskStatusFailure,
		FailReason:  "upstream-a model-upstream; retry: 12",
		Data:        []byte(`{"model":"model-upstream","platform":"private-plugin"}`),
		Properties: model.Properties{
			OriginModelName:   "model-public",
			UpstreamModelName: "model-upstream",
		},
		PrivateData: model.TaskPrivateData{
			InternalAsync:    true,
			ChannelRetryPath: []string{"12", "18"},
			PromptTokens:     111,
			CompletionTokens: 222,
			TotalTokens:      333,
			UsageDetails: &dto.TaskUsageDetails{
				PromptTokens:     111,
				CompletionTokens: 222,
				TotalTokens:      333,
				PromptTokensDetails: dto.InputTokenDetails{
					TextTokens:   100,
					ImageTokens:  11,
					CachedTokens: 9,
				},
				CompletionTokenDetails: dto.OutputTokenDetails{
					TextTokens:      200,
					ReasoningTokens: 22,
				},
			},
		},
	}

	adminDto := TaskModel2Dto(task)
	assert.Equal(t, "model-upstream", adminDto.UpstreamModelName)
	require.IsType(t, model.Properties{}, adminDto.Properties)
	assert.Equal(t, "model-upstream", adminDto.Properties.(model.Properties).UpstreamModelName)
	require.Equal(t, 12, adminDto.ChannelId)
	require.Equal(t, "upstream-a", adminDto.ChannelName)
	require.Equal(t, []string{"12", "18"}, adminDto.ChannelRetryPath)
	require.Equal(t, 23, adminDto.TokenId)
	require.Equal(t, "task-token", adminDto.TokenName)
	require.Equal(t, 111, adminDto.PromptTokens)
	require.Equal(t, 222, adminDto.CompletionTokens)
	require.Equal(t, 333, adminDto.TotalTokens)
	require.NotNil(t, adminDto.UsageDetails)
	require.Equal(t, 100, adminDto.UsageDetails.PromptTokensDetails.TextTokens)
	require.Equal(t, 22, adminDto.UsageDetails.CompletionTokenDetails.ReasoningTokens)

	userDto := UserTaskModel2Dto(task)
	assert.Empty(t, userDto.Platform)
	assert.Empty(t, userDto.Data)
	assert.Empty(t, userDto.ResultURL)
	assert.Equal(t, "generation_failed", userDto.ErrorCode)
	assert.Contains(t, userDto.FailReason, "[generation_failed]")
	assert.NotContains(t, userDto.FailReason, "model-upstream")
	assert.Equal(t, task.FailReason, adminDto.FailReason)
	require.Zero(t, userDto.ChannelId)
	require.Empty(t, userDto.ChannelName)
	require.Empty(t, userDto.ChannelRetryPath)
	require.Equal(t, 23, userDto.TokenId)
	require.Equal(t, "task-token", userDto.TokenName)
	require.False(t, userDto.InternalAsync)
	require.Equal(t, "model-public", userDto.ModelName)
	assert.Empty(t, userDto.UpstreamModelName)
	require.IsType(t, model.Properties{}, userDto.Properties)
	assert.Empty(t, userDto.Properties.(model.Properties).UpstreamModelName)
	assert.Equal(t, "model-public", userDto.Properties.(model.Properties).OriginModelName)
	userListDto := UserTaskModel2ListDto(task)
	assert.Equal(t, "model-public", userListDto.ModelName)
	assert.Empty(t, userListDto.UpstreamModelName)
	require.IsType(t, model.Properties{}, userListDto.Properties)
	assert.Empty(t, userListDto.Properties.(model.Properties).UpstreamModelName)
	assert.Equal(t, "model-upstream", task.Properties.UpstreamModelName)
	require.Equal(t, 111, userDto.PromptTokens)
	require.Equal(t, 222, userDto.CompletionTokens)
	require.Equal(t, 333, userDto.TotalTokens)
	require.NotNil(t, userDto.UsageDetails)
	require.Equal(t, 11, userDto.UsageDetails.PromptTokensDetails.ImageTokens)
	require.Equal(t, 200, userDto.UsageDetails.CompletionTokenDetails.TextTokens)
	task.Properties.OriginModelName = ""
	assert.Empty(t, UserTaskModel2Dto(task).ModelName, "never fall back to an upstream model")
}
