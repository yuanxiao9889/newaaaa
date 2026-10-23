package plugins_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJieAPIAudio(t *testing.T) {
	source, err := builtinplugins.Source("jieapi-audio")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "jieapi-audio"})
	require.NoError(t, err)
	require.NoError(t, jsplugin.ValidateV1Meta(plugin.Meta))

	t.Run("native submission and synchronous result", func(t *testing.T) {
		value, err := plugin.Engine.CallMember(t.Context(), "native", "generate", map[string]any{
			"body": map[string]any{"kind": "json", "value": map[string]any{
				"model": "seed-audio-1.0", "text_prompt": "A thirty-second story",
				"audio_config": map[string]any{"speech_rate": 0, "enable_subtitle": false},
			}},
		})
		require.NoError(t, err)
		intent := alibabaObject(t, value)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelBaseUrl: "https://jieapi.com", UpstreamModelName: "seed-audio-1.0", ApiKey: "fixture-provider-key",
			},
			OriginModelName: "seed-audio-1.0",
			TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public", Action: "audio_generation"},
		}
		adaptor := taskplugin.New(plugin)
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/jieapi/v1/audio/speech", nil)
		c.Set("task_request", intent["requestBody"])
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
		reader, err := adaptor.BuildRequestBody(c, info)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		var request map[string]any
		require.NoError(t, common.Unmarshal(data, &request))
		assert.Equal(t, map[string]any{"format": "mp3", "speech_rate": float64(0), "enable_subtitle": false}, request["audio_config"])
		assert.NotContains(t, string(data), "fixture-provider-key")
		url, err := adaptor.BuildRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://jieapi.com/audios/v1/audio/speech", url)
		usage, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"requestBody": intent["requestBody"]})
		require.NoError(t, err)
		assert.EqualValues(t, 120, alibabaObject(t, usage)["seconds"])
		body := map[string]any{
			"audio": "SUQz", "url": "https://cdn.example/story.mp3", "duration": 15, "original_duration": 30,
			"subtitle": map[string]any{"text": "A story"},
		}
		encoded, err := common.Marshal(body)
		require.NoError(t, err)
		result, taskErr := adaptor.ParseResponse(c, &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(bytes.NewReader(encoded)),
		}, info)
		require.Nil(t, taskErr)
		require.NotNil(t, result.Immediate)
		assert.Equal(t, "SUCCESS", result.Immediate.Status)
		assert.Equal(t, "task_public", result.UpstreamTaskID)
		usage, err = plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{}, map[string]any{"status": "SUCCESS"}, body)
		require.NoError(t, err)
		assert.EqualValues(t, 30, alibabaObject(t, usage)["seconds"], "billing uses original duration, not the 15-second processed audio")
		var returned map[string]any
		require.NoError(t, common.Unmarshal(result.TaskData, &returned))
		assert.Equal(t, "SUQz", returned["audio"])
		assert.Equal(t, map[string]any{"text": "A story"}, returned["subtitle"])
	})

	t.Run("reject invalid inputs before upstream submission", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			field string
			value any
		}{
			{"empty prompt", "text_prompt", ""},
			{"unsupported numeric duration", "duration", 1e20},
			{"wrong reference type", "references", "audio"},
			{"mutually exclusive sources", "references", []any{map[string]any{"speaker": "a", "audio_url": "https://cdn.example/a.mp3"}}},
			{"mixed references", "references", []any{map[string]any{"speaker": "a"}, map[string]any{"image_url": "https://cdn.example/a.png"}}},
			{"invalid base64", "references", []any{map[string]any{"audio_data": "not base64"}}},
			{"invalid rate", "audio_config", map[string]any{"speech_rate": 101}},
			{"fractional pitch", "audio_config", map[string]any{"pitch_rate": 1.5}},
			{"unsupported sample rate", "audio_config", map[string]any{"format": "mp3", "sample_rate": 40000}},
			{"invalid subtitle flag", "audio_config", map[string]any{"enable_subtitle": "false"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req := map[string]any{"model": "seed-audio-1.0", "text_prompt": "A story", tc.field: tc.value}
				_, err := plugin.Engine.CallMember(t.Context(), "native", "generate", map[string]any{
					"body": map[string]any{"kind": "json", "value": req},
				})
				require.Error(t, err)
			})
		}
	})

	t.Run("invalid billing durations never become charges", func(t *testing.T) {
		for _, seconds := range []any{nil, "30", -1, 0, 120.01, 1e20} {
			body := map[string]any{"audio": "SUQz", "url": "https://cdn.example/a.mp3", "duration": 30, "original_duration": seconds}
			_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"publicTaskId": "task_public"}, map[string]any{"body": body})
			require.Error(t, err)
		}
		result, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"publicTaskId": "task_public"}, map[string]any{
			"body": map[string]any{"error": map[string]any{"message": "fixture-secret"}},
		})
		require.NoError(t, err)
		encoded, err := common.Marshal(result)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "fixture-secret")
		usage, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{}, map[string]any{"status": "FAILURE"}, nil)
		require.NoError(t, err)
		assert.EqualValues(t, 0, alibabaObject(t, usage)["seconds"])
	})
}
