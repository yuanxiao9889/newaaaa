package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestFormatUserLogsForResponseHidesChannelInfo(t *testing.T) {
	logs := []*Log{
		{
			Id:          99,
			ChannelId:   12,
			ChannelName: "upstream-a",
			Other: common.MapToJsonStr(map[string]any{
				"future_diagnostic":        map[string]any{"model": "private-model"},
				"model_price":              "private-model",
				"actual_quota":             123,
				"admin_info":               map[string]interface{}{"use_channel": []int{12}},
				"audit_info":               map[string]interface{}{"path": "/api/channel"},
				"channel_id":               12,
				"channel_name":             "upstream-a",
				"channel_type":             1,
				"request_path":             "/v1/images/edits",
				"request_conversion":       []string{"openai-image"},
				"status_code":              400,
				"error_type":               "openai_error",
				"error_code":               "invalid_image_input",
				"async_channel_retry_path": []string{"12", "18"},
				"async_channel_retry_details": []map[string]interface{}{
					{
						"attempt":      1,
						"channel_id":   12,
						"channel_name": "upstream-a",
						"status_code":  502,
						"error_code":   "upstream_error",
						"error":        "provider failed",
					},
				},
				"stream_status": map[string]interface{}{"status": "done"},
				"task_id":       "task_public",
			}),
		},
	}

	// Exercise the formatter used by both self and token log endpoints.
	logs[0].Type = LogTypeError
	logs[0].Content = "provider-private model-private; retry: 12"
	logs[0].UpstreamRequestId = "upstream-private"
	formatUserLogs(logs, 20)
	require.Contains(t, logs[0].Content, "[invalid_image_input]")
	require.NotContains(t, logs[0].Content, "provider-private")
	require.Empty(t, logs[0].UpstreamRequestId)

	require.Equal(t, 0, logs[0].ChannelId)
	require.Empty(t, logs[0].ChannelName)
	require.Equal(t, 21, logs[0].Id)

	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
	require.NotContains(t, other, "admin_info")
	require.NotContains(t, other, "audit_info")
	require.NotContains(t, other, "channel_id")
	require.NotContains(t, other, "channel_name")
	require.NotContains(t, other, "channel_type")
	require.NotContains(t, other, "async_channel_retry_path")
	require.NotContains(t, other, "async_channel_retry_details")
	require.NotContains(t, other, "request_path")
	require.NotContains(t, other, "request_conversion")
	require.Equal(t, float64(400), other["status_code"])
	require.NotContains(t, other, "error_type")
	require.Equal(t, "invalid_image_input", other["error_code"])
	require.NotContains(t, other, "stream_status")
	require.Equal(t, "task_public", other["task_id"])
	require.NotContains(t, other, "future_diagnostic")
	require.NotContains(t, other, "model_price")
	require.Equal(t, float64(123), other["actual_quota"])
}
