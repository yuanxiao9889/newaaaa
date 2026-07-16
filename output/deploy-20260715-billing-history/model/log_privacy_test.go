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
			Other: common.MapToJsonStr(map[string]interface{}{
				"admin_info":               map[string]interface{}{"use_channel": []int{12}},
				"audit_info":               map[string]interface{}{"path": "/api/channel"},
				"channel_id":               12,
				"channel_name":             "upstream-a",
				"channel_type":             1,
				"request_path":             "/v1/images/edits",
				"request_conversion":       []string{"openai-image"},
				"status_code":              502,
				"error_type":               "openai_error",
				"error_code":               "upstream_error",
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

	FormatUserLogsForResponse(logs, 20)

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
	require.NotContains(t, other, "status_code")
	require.NotContains(t, other, "error_type")
	require.NotContains(t, other, "error_code")
	require.NotContains(t, other, "stream_status")
	require.Equal(t, "task_public", other["task_id"])
}
