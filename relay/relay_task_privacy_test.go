package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestUserTaskModelDtoHidesInternalChannelInfo(t *testing.T) {
	task := &model.Task{
		ID:          1,
		TaskID:      "task_public",
		Platform:    constant.TaskPlatform("suno"),
		UserId:      7,
		Group:       "default",
		ChannelId:   12,
		ChannelName: "upstream-a",
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
	require.Equal(t, 12, adminDto.ChannelId)
	require.Equal(t, "upstream-a", adminDto.ChannelName)
	require.Equal(t, []string{"12", "18"}, adminDto.ChannelRetryPath)
	require.Equal(t, 111, adminDto.PromptTokens)
	require.Equal(t, 222, adminDto.CompletionTokens)
	require.Equal(t, 333, adminDto.TotalTokens)
	require.NotNil(t, adminDto.UsageDetails)
	require.Equal(t, 100, adminDto.UsageDetails.PromptTokensDetails.TextTokens)
	require.Equal(t, 22, adminDto.UsageDetails.CompletionTokenDetails.ReasoningTokens)

	userDto := UserTaskModel2Dto(task)
	require.Zero(t, userDto.ChannelId)
	require.Empty(t, userDto.ChannelName)
	require.Empty(t, userDto.ChannelRetryPath)
	require.True(t, userDto.InternalAsync)
	require.Equal(t, "model-public", userDto.ModelName)
	require.Equal(t, "model-upstream", userDto.UpstreamModelName)
	require.Equal(t, 111, userDto.PromptTokens)
	require.Equal(t, 222, userDto.CompletionTokens)
	require.Equal(t, 333, userDto.TotalTokens)
	require.NotNil(t, userDto.UsageDetails)
	require.Equal(t, 11, userDto.UsageDetails.PromptTokensDetails.ImageTokens)
	require.Equal(t, 200, userDto.UsageDetails.CompletionTokenDetails.TextTokens)
}
