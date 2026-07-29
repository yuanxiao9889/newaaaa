package sora

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeGrokVideoRequest(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var request map[string]any
	require.NoError(t, common.Unmarshal(payload, &request))
	return request
}

func TestBuildGrokVideoGenerationsRequestConvertsSingleImageModel(t *testing.T) {
	payload, err := buildGrokVideoGenerationsRequest([]byte(`{
		"model":"ok-video-1.5-preview",
		"prompt":"A woman turns toward the camera",
		"seconds":"10",
		"size":"1280x720",
		"resolution":"720p",
		"input_reference":"data:image/jpeg;base64,AAAA",
		"image_url":"data:image/jpeg;base64,AAAA"
	}`), "grok-api-video-1.5", "ok-video-1.5-preview")
	require.NoError(t, err)

	request := decodeGrokVideoRequest(t, payload)
	assert.Equal(t, "grok-api-video-1.5-10s", request["model"])
	assert.Equal(t, "A woman turns toward the camera", request["prompt"])
	assert.NotContains(t, request, "seconds")
	assert.Equal(t, "16:9", request["aspect_ratio"])
	assert.Equal(t, "720P", request["resolution"])
	assert.Equal(t, "data:image/jpeg;base64,AAAA", request["image"])
	assert.NotContains(t, request, "size")
	assert.NotContains(t, request, "input_reference")
	assert.NotContains(t, request, "image_url")
}

func TestBuildGrokVideoGenerationsRequestConvertsMessageImagesForVideo3(t *testing.T) {
	payload, err := buildGrokVideoGenerationsRequest([]byte(`{
		"model":"ok-imagine-video",
		"prompt":"The two characters walk forward",
		"seconds":"20",
		"ratio":"9:16",
		"resolution_name":"720p",
		"messages":[{
			"role":"user",
			"content":[
				{"type":"text","text":"The two characters walk forward"},
				{"type":"image_url","image_url":{"url":"https://example.test/one.jpg"}},
				{"type":"image_url","image_url":{"url":"https://example.test/two.jpg"}}
			]
		}]
	}`), "grok-video-3-20s", "ok-imagine-video")
	require.NoError(t, err)

	request := decodeGrokVideoRequest(t, payload)
	assert.Equal(t, "grok-video-3-20s", request["model"])
	assert.Equal(t, "9:16", request["aspect_ratio"])
	assert.Equal(t, "720P", request["quality"])
	assert.Equal(t, []any{
		"https://example.test/one.jpg",
		"https://example.test/two.jpg",
	}, request["images"])
	assert.NotContains(t, request, "seconds")
	assert.NotContains(t, request, "messages")
}

func TestBuildGrokVideoGenerationsRequestRoutesVideo3DurationsToFixedModels(t *testing.T) {
	tests := []struct {
		seconds int
		model   string
	}{
		{seconds: 10, model: "grok-video-3-10s"},
		{seconds: 16, model: "grok-video-3-16s"},
		{seconds: 20, model: "grok-video-3-20s"},
	}

	for _, test := range tests {
		body := []byte(fmt.Sprintf(
			`{"model":"ok-imagine-video","prompt":"test","seconds":%d,"images":["https://example.test/reference.jpg"]}`,
			test.seconds,
		))
		payload, err := buildGrokVideoGenerationsRequest(
			body,
			"grok-video-3",
			"ok-imagine-video",
		)
		require.NoError(t, err)

		request := decodeGrokVideoRequest(t, payload)
		assert.Equal(t, test.model, request["model"])
		assert.Equal(t, []any{"https://example.test/reference.jpg"}, request["images"])
		assert.NotContains(t, request, "seconds")
	}
}

func TestBuildGrokVideoGenerationsRequestRejectsUnsupportedVideo3Duration(t *testing.T) {
	_, err := buildGrokVideoGenerationsRequest(
		[]byte(`{"model":"ok-imagine-video","prompt":"test","seconds":12}`),
		"grok-video-3",
		"ok-imagine-video",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only supports fixed durations 10, 16, or 20 seconds")
}

func TestTaskAdaptorRoutesOnlyExplicitGrokVideoChannelToGenerationsEndpoint(t *testing.T) {
	adaptor := &TaskAdaptor{baseURL: "https://api.bjhuiqu.net"}

	grokURL, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeGrokVideo,
			UpstreamModelName: "grok-video-3-20s",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api.bjhuiqu.net/v1/videos/generations", grokURL)

	legacyURL, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "grok-video-3-20s",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api.bjhuiqu.net/v1/videos", legacyURL)
}

func TestBuildGrokVideoGenerationsRequestRejectsUnsupportedModel(t *testing.T) {
	_, err := buildGrokVideoGenerationsRequest(
		[]byte(`{"prompt":"test"}`),
		"same-model-id-from-another-protocol",
		"same-model-id-from-another-protocol",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported model")
}

func TestTaskAdaptorAcceptsNestedGenerationsSubmitResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(bytes.NewBufferString(`{
			"created":1784994030,
			"data":{"id":"task_upstream","status":"processing"}
		}`)),
	}
	adaptor := &TaskAdaptor{}

	upstreamID, taskData, taskErr := adaptor.DoResponse(context, response, &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID: "task_public",
		},
	})
	require.Nil(t, taskErr)
	assert.Equal(t, "task_upstream", upstreamID)
	assert.JSONEq(t, `{
		"created":1784994030,
		"data":{"id":"task_upstream","status":"processing"}
	}`, string(taskData))

	var clientResponse map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &clientResponse))
	assert.Equal(t, "task_public", clientResponse["id"])
	assert.Equal(t, "task_public", clientResponse["task_id"])
	assert.Equal(t, "processing", clientResponse["status"])
	assert.Equal(t, float64(1784994030), clientResponse["created"])
	data, ok := clientResponse["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "task_public", data["id"])
}

func TestBuildGrokVideoGenerationsRequestRejectsUnsafeSeconds(t *testing.T) {
	tests := []string{
		`{"prompt":"test","seconds":1.5}`,
		`{"prompt":"test","seconds":999999999999999999999}`,
		`{"prompt":"test","seconds":"not-a-number"}`,
	}

	for _, body := range tests {
		_, err := buildGrokVideoGenerationsRequest(
			[]byte(body),
			"grok-api-video-1.5",
			"grok-api-video-1.5",
		)
		require.Error(t, err)
	}
}

func TestBuildGrokVideoGenerationsRequestRoutesPreviewDurationsToFixedModels(t *testing.T) {
	tests := []struct {
		seconds int
		model   string
	}{
		{seconds: 10, model: "grok-api-video-1.5-10s"},
		{seconds: 15, model: "grok-api-video-1.5-15s"},
	}

	for _, test := range tests {
		body := []byte(fmt.Sprintf(
			`{"model":"ok-video-1.5-preview","prompt":"test","seconds":%d}`,
			test.seconds,
		))
		payload, err := buildGrokVideoGenerationsRequest(
			body,
			"grok-api-video-1.5",
			"ok-video-1.5-preview",
		)
		require.NoError(t, err)

		request := decodeGrokVideoRequest(t, payload)
		assert.Equal(t, test.model, request["model"])
		assert.NotContains(t, request, "seconds")
	}
}

func TestBuildGrokVideoGenerationsRequestRejectsRemovedPreviewDuration(t *testing.T) {
	_, err := buildGrokVideoGenerationsRequest(
		[]byte(`{"model":"ok-video-1.5-preview","prompt":"test","seconds":20}`),
		"grok-api-video-1.5",
		"ok-video-1.5-preview",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "between 1 and 15")
}

func TestBuildGrokVideoGenerationsRequestKeepsDirectFlexibleModel(t *testing.T) {
	payload, err := buildGrokVideoGenerationsRequest(
		[]byte(`{"model":"grok-api-video-1.5","prompt":"test","seconds":8}`),
		"grok-api-video-1.5",
		"grok-api-video-1.5",
	)
	require.NoError(t, err)

	request := decodeGrokVideoRequest(t, payload)
	assert.Equal(t, "grok-api-video-1.5", request["model"])
	assert.Equal(t, "8", request["seconds"])
}

func TestConvertToOpenAIVideoExposesDirectAndProxyURLsForGrokGenerations(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		Status: model.TaskStatusSuccess,
		Platform: constant.TaskPlatform(
			"59",
		),
		Properties: model.Properties{
			UpstreamModelName: "grok-video-3-20s",
		},
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://media.example.test/result.mp4",
		},
		Data: []byte(`{
			"id":"task_upstream",
			"status":"completed",
			"url":"https://api.example.test/v1/videos/task_upstream/content"
		}`),
	}

	payload, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	var response map[string]any
	require.NoError(t, common.Unmarshal(payload, &response))
	assert.Equal(t, "task_public", response["id"])
	assert.Equal(t, "task_public", response["task_id"])
	assert.Equal(t, "completed", response["status"])
	assert.Equal(t, "https://media.example.test/result.mp4", response["video_url"])
	assert.Contains(t, response["url"], "/v1/videos/task_public/content")
}
