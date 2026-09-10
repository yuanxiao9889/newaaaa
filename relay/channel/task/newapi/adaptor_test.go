package newapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newValidationContext(body string) (*gin.Context, *relaycommon.RelayInfo) {
	return newValidationContextWithSettings(body, dto.ChannelSettings{})
}

func newValidationContextWithSettings(body string, settings dto.ChannelSettings) (*gin.Context, *relaycommon.RelayInfo) {
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	return context, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeNewAPI,
			ChannelSetting: settings,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
}

func validatedRequestBody(t *testing.T, body string) (map[string]any, *TaskAdaptor, *gin.Context, *relaycommon.RelayInfo) {
	return validatedRequestBodyWithSettings(t, body, dto.ChannelSettings{})
}

func validatedRequestBodyWithSettings(t *testing.T, body string, settings dto.ChannelSettings) (map[string]any, *TaskAdaptor, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	context, info := newValidationContextWithSettings(body, settings)
	adaptor := &TaskAdaptor{}
	require.Nil(t, adaptor.ValidateRequestAndSetAction(context, info))

	var input struct {
		Model string `json:"model"`
	}
	require.NoError(t, common.Unmarshal([]byte(body), &input))
	info.OriginModelName = input.Model
	info.UpstreamModelName = input.Model
	reader, err := adaptor.BuildRequestBody(context, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	var request map[string]any
	require.NoError(t, common.Unmarshal(payload, &request))
	return request, adaptor, context, info
}

func TestValidateRequestSetsDefaultsAndBillingRatios(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request, adaptor, context, info := validatedRequestBody(t, `{
		"model":"hj-2.0-fast-480p",
		"prompt":"A cinematic sunrise",
		"reference_videos":[
			"https://media.example.test/reference-1.mp4",
			"https://media.example.test/reference-2.mp4",
			"https://media.example.test/reference-3.mp4"
		]
	}`)

	assert.Equal(t, map[string]float64{
		"seconds":         5,
		"reference_video": 2,
	}, adaptor.EstimateBilling(context, info))
	assert.Equal(t, float64(5), request["duration"])
	assert.Equal(t, "16:9", request["ratio"])
	assert.Equal(t, true, request["generate_audio"])
}

func TestValidateRequestRejectsInvalidBillingAndMediaInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "duration below minimum", body: `{"model":"hj-2.0-480p","prompt":"test","duration":3}`},
		{name: "duration above maximum", body: `{"model":"hj-2.0-480p","prompt":"test","duration":16}`},
		{name: "global duration bound", body: `{"model":"MiniMax-H3","prompt":"test","duration":3601}`},
		{name: "mismatched duration aliases", body: `{"model":"MiniMax-H3","prompt":"test","duration":8,"seconds":"9"}`},
		{name: "unsupported model", body: `{"model":"doubao-seedance-2-0","prompt":"test","duration":4}`},
		{name: "invalid ratio", body: `{"model":"hj-2.0-480p","prompt":"test","duration":4,"ratio":"2:1"}`},
		{name: "images conflict with frames", body: `{"model":"hj-2.0-480p","prompt":"test","images":["https://example.test/a.png"],"start_frame":["https://example.test/b.png"]}`},
		{name: "audio without visual reference", body: `{"model":"hj-2.0-480p","prompt":"test","reference_audios":["https://example.test/a.mp3"]}`},
		{name: "private media URL", body: `{"model":"hj-2.0-480p","prompt":"test","images":["http://127.0.0.1/a.png"]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, info := newValidationContext(test.body)
			taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestMiniMaxH3RequestContract(t *testing.T) {
	request, adaptor, context, info := validatedRequestBody(t, `{
		"model":"MiniMax-H3-933-1440P-GF",
		"prompt":"A tracked subject",
		"seconds":"8",
		"size":"2560x1440",
		"audio":false,
		"reference_images":["https://media.example.test/subject.png"],
		"audio_reference":["https://media.example.test/music.mp3"]
	}`)

	assert.Equal(t, "MiniMax-H3", request["model"])
	assert.Equal(t, float64(8), request["seconds"])
	assert.Equal(t, "2560x1440", request["size"])
	assert.Equal(t, "16:9", request["aspect_ratio"])
	assert.Equal(t, "1440P", request["resolution"])
	assert.Equal(t, false, request["audio"])
	assert.Equal(t, map[string]float64{"seconds": 8}, adaptor.EstimateBilling(context, info))

	url, err := adaptor.BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "/v1/videos", strings.TrimPrefix(url, adaptor.baseURL))
}

func TestSecureMiniMaxH3RequestContract(t *testing.T) {
	request, _, _, _ := validatedRequestBody(t, `{
		"model":"minimax-h3",
		"prompt":"A cinematic sunrise",
		"duration":6,
		"ratio":"9:16",
		"resolution":"720p",
		"prompt_optimize":false,
		"seed":0,
		"image_urls":["https://media.example.test/subject.png"],
		"audio_urls":["https://media.example.test/music.mp3"]
	}`)

	assert.Equal(t, "minimax-h3", request["model"])
	assert.Equal(t, float64(6), request["duration"])
	assert.Equal(t, "9:16", request["ratio"])
	assert.Equal(t, "720p", request["resolution"])
	assert.Equal(t, false, request["prompt_optimize"])
	assert.Equal(t, float64(0), request["seed"])
	assert.Equal(t, []any{"https://media.example.test/subject.png"}, request["image_urls"])
	assert.Equal(t, []any{"https://media.example.test/music.mp3"}, request["audio_urls"])
	assert.NotContains(t, request, "seconds")
	assert.NotContains(t, request, "reference_images")
}

func TestSecureMiniMaxH3DefaultsAndDurationBounds(t *testing.T) {
	request, _, _, _ := validatedRequestBody(t, `{
		"model":"minimax-h3",
		"prompt":"Audio-led scene",
		"audio_urls":"https://media.example.test/music.mp3"
	}`)
	assert.Equal(t, float64(15), request["duration"])
	assert.Equal(t, "16:9", request["ratio"])
	assert.Equal(t, []any{"https://media.example.test/music.mp3"}, request["audio_urls"])

	for _, duration := range []int{3, 16} {
		context, info := newValidationContext(fmt.Sprintf(`{"model":"minimax-h3","prompt":"test","duration":%d}`, duration))
		taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	}
	context, info := newValidationContext(`{"model":"minimax-h3","prompt":"test","image_urls":["http://media.example.test/image.png"]}`)
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	context, info = newValidationContext(`{"model":"minimax-h3","prompt":"test","stream":true}`)
	taskErr = (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestMiniMaxH3Validation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "prompt over rune limit", body: `{"model":"MiniMax-H3","prompt":"` + strings.Repeat("界", 2001) + `","duration":8}`},
		{name: "too many images", body: `{"model":"MiniMax-H3","prompt":"test","duration":8,"reference_images":["https://e.test/1","https://e.test/2","https://e.test/3","https://e.test/4","https://e.test/5","https://e.test/6"]}`},
		{name: "video unsupported", body: `{"model":"MiniMax-H3","prompt":"test","duration":8,"reference_videos":["https://e.test/video.mp4"]}`},
		{name: "too many audios", body: `{"model":"MiniMax-H3","prompt":"test","duration":8,"reference_images":["https://e.test/image.png"],"audio_reference":["https://e.test/1.mp3","https://e.test/2.mp3","https://e.test/3.mp3","https://e.test/4.mp3"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, info := newValidationContext(test.body)
			taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestMiniMaxH3CRequestModes(t *testing.T) {
	firstFrame, _, _, _ := validatedRequestBody(t, `{
		"model":"MiniMax-H3-C",
		"prompt":"Animate this frame",
		"duration":10,
		"aspect_ratio":"9:16",
		"input_mode":"firstFrame",
		"start_frame":"https://media.example.test/start.png"
	}`)
	assert.Equal(t, "MiniMax H3", firstFrame["model"])
	assert.Equal(t, float64(10), firstFrame["duration"])
	assert.Equal(t, "2k", firstFrame["resolution"])
	assert.Equal(t, "https://media.example.test/start.png", firstFrame["input_reference"])

	reference, _, _, _ := validatedRequestBodyWithSettings(t, `{
		"model":"MiniMax-H3-C",
		"prompt":"Keep both subjects",
		"duration":"15",
		"ratio":"16:9",
		"input_mode":"reference",
		"input_reference":["https://media.example.test/one.png","https://media.example.test/two.png"]
	}`, dto.ChannelSettings{MultiImageReferenceEnabled: true})
	assert.Equal(t, []any{"https://media.example.test/one.png", "https://media.example.test/two.png"}, reference["input_reference"])
}

func TestMultiImageReferenceSettingControlsMiniMaxH3C(t *testing.T) {
	body := `{
		"model":"MiniMax-H3-C",
		"prompt":"Keep both subjects",
		"duration":10,
		"input_mode":"reference",
		"input_reference":["https://media.example.test/one.png","https://media.example.test/two.png"]
	}`

	disabledContext, disabledInfo := newValidationContext(body)
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(disabledContext, disabledInfo)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "multi_reference_disabled", taskErr.Code)

	enabledContext, enabledInfo := newValidationContextWithSettings(body, dto.ChannelSettings{
		MultiImageReferenceEnabled: true,
	})
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(enabledContext, enabledInfo))
}

func TestMiniMaxH3CRejectsInvalidReferences(t *testing.T) {
	tests := []string{
		`{"model":"MiniMax-H3-C","prompt":"test","duration":12,"input_mode":"firstFrame","image_url":"https://e.test/1.png"}`,
		`{"model":"MiniMax-H3-C","prompt":"test","duration":10,"input_mode":"firstFrame","reference_images":["https://e.test/1.png","https://e.test/2.png"]}`,
		`{"model":"MiniMax-H3-C","prompt":"test","duration":10,"input_mode":"reference","reference_images":["https://e.test/1.png"]}`,
		`{"model":"MiniMax-H3-C","prompt":"test","duration":10,"input_mode":"reference","reference_images":["https://e.test/1.png","https://e.test/2.png"],"audio_reference":["https://e.test/a.mp3"]}`,
	}
	for _, body := range tests {
		context, info := newValidationContext(body)
		require.NotNil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info))
	}
}

func TestPreviewVideoRequestContract(t *testing.T) {
	request, _, _, _ := validatedRequestBody(t, `{
		"model":"ok-video-1.5-preview",
		"prompt":"Camera orbit",
		"seconds":"15",
		"aspect_ratio":"9:16",
		"resolution":"720p",
		"reference_images":["https://media.example.test/reference.png"]
	}`)
	assert.Equal(t, "15", request["seconds"])
	assert.Equal(t, "720x1280", request["size"])
	assert.Equal(t, "720p", request["resolution"])
	assert.Equal(t, "https://media.example.test/reference.png", request["input_reference"])
	assert.Equal(t, "https://media.example.test/reference.png", request["image_url"])
}

func TestPreviewVideoRejectsUnsupportedOptions(t *testing.T) {
	tests := []string{
		`{"model":"ok-video-1.5-preview","prompt":"test","seconds":10}`,
		`{"model":"ok-video-1.5-preview","prompt":"test","seconds":12,"image_url":"https://e.test/a.png"}`,
		`{"model":"ok-video-1.5-preview","prompt":"test","seconds":10,"ratio":"1:1","image_url":"https://e.test/a.png"}`,
		`{"model":"ok-video-1.5-preview","prompt":"test","seconds":10,"resolution":"1080p","image_url":"https://e.test/a.png"}`,
	}
	for _, body := range tests {
		context, info := newValidationContext(body)
		require.NotNil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info))
	}
}

func TestOkImagineVideoModes(t *testing.T) {
	textRequest, _, _, _ := validatedRequestBody(t, `{
		"model":"ok-imagine-video","prompt":"Clouds moving","seconds":10,
		"ratio":"1:1","resolution_name":"1080p","input_mode":"textToVideo"
	}`)
	assert.Equal(t, false, textRequest["stream"])
	assert.Equal(t, "10", textRequest["duration"])
	assert.Equal(t, "1024x1024", textRequest["size"])
	assert.NotContains(t, textRequest, "messages")

	firstFrame, _, _, _ := validatedRequestBody(t, `{
		"model":"ok-imagine-video","prompt":"Animate portrait","duration":"16",
		"ratio":"9:16","resolution":"720p","input_mode":"firstFrame",
		"image_url":"https://media.example.test/frame.png"
	}`)
	assert.Equal(t, "https://media.example.test/frame.png", firstFrame["image_url"])
	context, info := newValidationContext(`{"model":"ok-imagine-video","prompt":"Animate portrait","duration":"16","ratio":"9:16","resolution":"720p","input_mode":"firstFrame","image_url":"https://media.example.test/frame.png","messages":[{"role":"user","content":[{"type":"text","text":"Animate portrait"},{"type":"image_url","image_url":{"url":"https://media.example.test/frame.png"}}]}]}`)
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(context, info))

	messages := firstFrame["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	assert.Len(t, content, 2)

	reference, _, _, _ := validatedRequestBody(t, `{
		"model":"ok-imagine-video","prompt":"Use both references","seconds":"20",
		"ratio":"16:9","resolution":"480p","input_mode":"reference",
		"input_reference":["https://media.example.test/a.png","https://media.example.test/b.png"]
	}`)
	messages = reference["messages"].([]any)
	content = messages[0].(map[string]any)["content"].([]any)
	assert.Len(t, content, 3)
}

func TestDoResponseHidesUpstreamTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(bytes.NewBufferString(`{
			"task":{"id":"task_upstream","status":"queued","created_at":1785137119}
		}`)),
	}

	parsed, taskErr := (&TaskAdaptor{}).ParseResponse(context, response, &relaycommon.RelayInfo{
		OriginModelName: "MiniMax-H3",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	})

	require.Nil(t, taskErr)
	assert.Equal(t, "task_upstream", parsed.UpstreamTaskID)
	assert.Contains(t, string(parsed.TaskData), "task_upstream")

	assert.Empty(t, recorder.Body.Bytes(), "parsing must not write before durable task persistence")
	clientBody, marshalErr := common.Marshal(parsed.ClientResponse)
	require.NoError(t, marshalErr)
	var clientResponse submitResponse
	require.NoError(t, common.Unmarshal(clientBody, &clientResponse))
	assert.Equal(t, "task_public", clientResponse.ID)
	assert.Equal(t, "task_public", clientResponse.TaskID)
	assert.Equal(t, "MiniMax-H3", clientResponse.Model)
}

func TestFetchTaskSelectsEndpointByModel(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer server.Close()

	adaptor := &TaskAdaptor{}
	for _, test := range []struct {
		model string
		path  string
	}{
		{model: "hj-2.0-fast-720p", path: "/v1/video/generations/task_1"},
		{model: "MiniMax-H3", path: "/v1/videos/task_2"},
		{model: "minimax-h3", path: "/v1/videos/task_3"},
		{model: "ok-imagine-video", path: "/v1/videos/task_4"},
	} {
		response, err := adaptor.FetchTask(server.URL, "secret", &model.Task{PrivateData: model.TaskPrivateData{UpstreamTaskID: strings.TrimPrefix(test.path[strings.LastIndex(test.path, "/"):], "/"), BillingContext: &model.TaskBillingContext{OriginModelName: test.model}}}, "")
		require.NoError(t, err)
		_ = response.Body.Close()
	}
	assert.Equal(t, []string{"/v1/video/generations/task_1", "/v1/videos/task_2", "/v1/videos/task_3", "/v1/videos/task_4"}, paths)
}

func TestParseTaskResultSupportsOopiiEnvelopes(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status model.TaskStatus
		url    string
		tokens int
	}{
		{
			name:   "task envelope",
			body:   `{"task":{"id":"task_h3","status":"completed","content":{"url":"https://media.example.test/h3.mp4"}}}`,
			status: model.TaskStatusSuccess,
			url:    "https://media.example.test/h3.mp4",
		},
		{
			name:   "openai envelope",
			body:   `{"id":"video_1","status":"processing","output":{"video_url":"https://media.example.test/pending.mp4"}}`,
			status: model.TaskStatusInProgress,
			url:    "https://media.example.test/pending.mp4",
		},
		{
			name:   "nested data and array output",
			body:   `{"data":{"status":"SUCCESS","data":{"task_id":"task_nested","usage":{"total_tokens":40594},"output":[{"url":"https://media.example.test/nested.mp4"}]}}}`,
			status: model.TaskStatusSuccess,
			url:    "https://media.example.test/nested.mp4",
			tokens: 40594,
		},
		{
			name:   "failure reason",
			body:   `{"task_id":"task_failed","state":"canceled","error":{"message":"provider canceled"}}`,
			status: model.TaskStatusFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (&TaskAdaptor{}).ParseTaskResult(nil, nil, []byte(test.body))
			require.NoError(t, err)
			assert.Equal(t, string(test.status), result.Status)
			assert.Equal(t, test.url, result.Url)
			assert.Equal(t, test.tokens, result.TotalTokens)
		})
	}
}

func TestParseAndConvertColomiResponsePreservesVerificationData(t *testing.T) {
	body := []byte(`{
		"code":"success",
		"data":{
			"task_id":"task_upstream",
			"status":"SUCCESS",
			"progress":"100%",
			"result_url":"https://ark.example.test/result.mp4",
			"data":{
				"id":"cgt-20260727152519-8c599",
				"model":"doubao-seedance-2-0-fast-260128",
				"usage":{"total_tokens":40594}
			}
		},
		"new_api_task_id":"task_upstream",
		"new_api_status":"SUCCESS"
	}`)
	adaptor := &TaskAdaptor{}

	result, err := adaptor.ParseTaskResult(nil, nil, body)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusSuccess), result.Status)
	assert.Equal(t, "https://ark.example.test/result.mp4", result.Url)
	assert.Equal(t, 40594, result.TotalTokens)

	task := &model.Task{
		TaskID:      "task_local",
		Status:      model.TaskStatusSuccess,
		Progress:    "100%",
		PrivateData: model.TaskPrivateData{ResultURL: "https://ark.example.test/result.mp4"},
		Data:        body,
	}
	payload, err := adaptor.ConvertToVideoGeneration(task)
	require.NoError(t, err)

	var response queryResponse
	require.NoError(t, common.Unmarshal(payload, &response))
	assert.Equal(t, "task_local", response.Data.TaskID)
	assert.Equal(t, "task_local", response.NewAPITaskID)
	assert.NotContains(t, string(payload), `"task_id":"task_upstream"`)
	assert.Contains(t, string(response.Data.Data), "cgt-20260727152519-8c599")
}

func TestConvertToOpenAIVideoUsesPublicTaskID(t *testing.T) {
	task := &model.Task{
		TaskID:      "task_public",
		Status:      model.TaskStatusFailure,
		Progress:    "60%",
		FailReason:  "upstream failed",
		Properties:  model.Properties{OriginModelName: "ok-imagine-video"},
		PrivateData: model.TaskPrivateData{UpstreamTaskID: "task_upstream"},
	}
	payload, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "task_upstream")

	var response struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, common.Unmarshal(payload, &response))
	assert.Equal(t, "task_public", response.ID)
	assert.Equal(t, "task_public", response.TaskID)
	assert.Equal(t, "failed", response.Status)
	require.NotNil(t, response.Error)
	assert.Equal(t, "upstream failed", response.Error.Message)
}
