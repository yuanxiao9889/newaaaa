package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeToOpenAIRequestNormalizesInvalidEmptyToolSchemaFields(t *testing.T) {
	request := dto.ClaudeRequest{
		Model: "gpt-test",
		Tools: []any{
			map[string]any{
				"name": "CronList",
				"input_schema": map[string]any{
					"type":                 "object",
					"properties":           []any{},
					"additionalProperties": []any{},
					"required":             []any{},
				},
			},
			map[string]any{
				"name": "RemoteTrigger",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"options": map[string]any{
							"type":       "object",
							"properties": []any{},
						},
					},
				},
			},
		},
	}

	converted, err := ClaudeToOpenAIRequest(request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	})
	require.NoError(t, err)
	require.Len(t, converted.Tools, 2)

	cronSchema, ok := converted.Tools[0].Function.Parameters.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}{}, cronSchema["properties"])
	assert.Equal(t, false, cronSchema["additionalProperties"])
	assert.Equal(t, []interface{}{}, cronSchema["required"])

	remoteSchema, ok := converted.Tools[1].Function.Parameters.(map[string]interface{})
	require.True(t, ok)
	properties, ok := remoteSchema["properties"].(map[string]interface{})
	require.True(t, ok)
	options, ok := properties["options"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}{}, options["properties"])
}

func TestStreamResponseOpenAI2ClaudeRemapsSparseToolIndexes(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone},
		SendResponseCount: 1,
	}
	text := "I will read the file."
	firstChunk := &dto.ChatCompletionsStreamResponse{
		Id:    "msg_test",
		Model: "claude-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text},
		}},
	}

	firstEvents := StreamResponseOpenAI2Claude(firstChunk, info)
	require.Len(t, firstEvents, 3)
	assert.Equal(t, []string{"message_start", "content_block_start", "content_block_delta"}, []string{
		firstEvents[0].Type,
		firstEvents[1].Type,
		firstEvents[2].Type,
	})
	assert.Equal(t, 0, firstEvents[1].GetIndex())
	assert.Equal(t, 0, firstEvents[2].GetIndex())

	upstreamToolIndex := 1
	argumentsStart := `{"file_path":"`
	info.SendResponseCount = 2
	toolStartChunk := &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
				ToolCalls: []dto.ToolCallResponse{{
					Index: &upstreamToolIndex,
					ID:    "toolu_read",
					Function: dto.FunctionResponse{
						Name:      "Read",
						Arguments: argumentsStart,
					},
				}},
			},
		}},
	}

	toolStartEvents := StreamResponseOpenAI2Claude(toolStartChunk, info)
	require.Len(t, toolStartEvents, 3)
	assert.Equal(t, "content_block_stop", toolStartEvents[0].Type)
	assert.Equal(t, 0, toolStartEvents[0].GetIndex())
	assert.Equal(t, "content_block_start", toolStartEvents[1].Type)
	assert.Equal(t, 1, toolStartEvents[1].GetIndex())
	require.NotNil(t, toolStartEvents[1].ContentBlock)
	assert.Equal(t, "tool_use", toolStartEvents[1].ContentBlock.Type)
	assert.Equal(t, "Read", toolStartEvents[1].ContentBlock.Name)
	assert.Equal(t, "content_block_delta", toolStartEvents[2].Type)
	assert.Equal(t, 1, toolStartEvents[2].GetIndex())

	argumentsEnd := `test.txt"}`
	info.SendResponseCount = 3
	toolDeltaChunk := &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
				ToolCalls: []dto.ToolCallResponse{{
					Index: &upstreamToolIndex,
					Function: dto.FunctionResponse{
						Arguments: argumentsEnd,
					},
				}},
			},
		}},
	}

	toolDeltaEvents := StreamResponseOpenAI2Claude(toolDeltaChunk, info)
	require.Len(t, toolDeltaEvents, 1)
	assert.Equal(t, "content_block_delta", toolDeltaEvents[0].Type)
	assert.Equal(t, 1, toolDeltaEvents[0].GetIndex())

	finishReason := "tool_calls"
	info.SendResponseCount = 4
	finishEvents := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finishReason}},
		Usage: &dto.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}, info)
	require.Len(t, finishEvents, 3)
	assert.Equal(t, "content_block_stop", finishEvents[0].Type)
	assert.Equal(t, 1, finishEvents[0].GetIndex())
	assert.Equal(t, "message_delta", finishEvents[1].Type)
	assert.Equal(t, "message_stop", finishEvents[2].Type)
}

func TestStreamResponseOpenAI2ClaudeDropsOrphanToolEvents(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeText,
			Index:            0,
		},
		SendResponseCount: 2,
	}
	upstreamToolIndex := 7
	arguments := `{"path":"test.txt"}`

	events := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
				ToolCalls: []dto.ToolCallResponse{{
					Index: &upstreamToolIndex,
					Function: dto.FunctionResponse{
						Arguments: arguments,
					},
				}},
			},
		}},
	}, info)

	require.Len(t, events, 1)
	assert.Equal(t, "content_block_stop", events[0].Type)
	assert.Equal(t, 0, events[0].GetIndex())
	assert.Equal(t, 0, info.ClaudeConvertInfo.ToolCallCount)

	finishReason := "tool_calls"
	info.SendResponseCount = 3
	finishEvents := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finishReason}},
		Usage: &dto.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}, info)

	require.Len(t, finishEvents, 2)
	assert.Equal(t, "message_delta", finishEvents[0].Type)
	assert.Equal(t, "message_stop", finishEvents[1].Type)
}
