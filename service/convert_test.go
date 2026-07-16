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
