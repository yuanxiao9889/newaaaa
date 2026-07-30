package newapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newValidationContext(body string) (*gin.Context, *relaycommon.RelayInfo) {
	request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	return context, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
}

func TestValidateRequestSetsDefaultsAndBillingRatios(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, info := newValidationContext(`{
		"model":"hj-2.0-fast-480p",
		"prompt":"A cinematic sunrise",
		"reference_videos":[
			"https://media.example.test/reference-1.mp4",
			"https://media.example.test/reference-2.mp4",
			"https://media.example.test/reference-3.mp4"
		]
	}`)
	adaptor := &TaskAdaptor{}

	taskErr := adaptor.ValidateRequestAndSetAction(context, info)

	require.Nil(t, taskErr)
	assert.Equal(t, map[string]float64{
		"seconds":         5,
		"reference_video": 2,
	}, adaptor.EstimateBilling(context, info))

	bodyReader, err := adaptor.BuildRequestBody(context, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "hj-2.0-fast-480p",
		},
	})
	require.NoError(t, err)
	body, err := io.ReadAll(bodyReader)
	require.NoError(t, err)

	var request generationRequest
	require.NoError(t, common.Unmarshal(body, &request))
	require.NotNil(t, request.Duration)
	assert.Equal(t, 5, *request.Duration)
	assert.Equal(t, "16:9", request.Ratio)
	require.NotNil(t, request.GenerateAudio)
	assert.True(t, *request.GenerateAudio)
}

func TestValidateRequestRejectsInvalidBillingAndMediaInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "duration below minimum", body: `{"model":"hj-2.0-480p","prompt":"test","duration":3}`},
		{name: "duration above maximum", body: `{"model":"hj-2.0-480p","prompt":"test","duration":16}`},
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

func TestDoResponseHidesUpstreamTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(bytes.NewBufferString(`{
			"id":"task_upstream",
			"task_id":"task_upstream",
			"object":"video",
			"model":"hj-2.0-fast-480p",
			"status":"queued",
			"created_at":1785137119
		}`)),
	}

	upstreamID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(context, response, &relaycommon.RelayInfo{
		OriginModelName: "hj-2.0-fast-480p",
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID: "task_public",
		},
	})

	require.Nil(t, taskErr)
	assert.Equal(t, "task_upstream", upstreamID)
	assert.Contains(t, string(taskData), "task_upstream")

	var clientResponse submitResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &clientResponse))
	assert.Equal(t, "task_public", clientResponse.ID)
	assert.Equal(t, "task_public", clientResponse.TaskID)
	assert.Equal(t, "hj-2.0-fast-480p", clientResponse.Model)
}

func TestParseAndConvertTaskResponsePreservesVerificationData(t *testing.T) {
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
				"seed":91277,
				"framespersecond":24,
				"usage":{"total_tokens":40594}
			}
		},
		"new_api_task_id":"task_upstream",
		"new_api_status":"SUCCESS"
	}`)
	adaptor := &TaskAdaptor{}

	result, err := adaptor.ParseTaskResult(body)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusSuccess), result.Status)
	assert.Equal(t, "https://ark.example.test/result.mp4", result.Url)
	assert.Equal(t, 40594, result.TotalTokens)

	task := &model.Task{
		TaskID:     "task_local",
		Status:     model.TaskStatusSuccess,
		Progress:   "100%",
		FailReason: "",
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://ark.example.test/result.mp4",
		},
		Data: body,
	}
	payload, err := adaptor.ConvertToVideoGeneration(task)
	require.NoError(t, err)

	var response queryResponse
	require.NoError(t, common.Unmarshal(payload, &response))
	assert.Equal(t, "task_local", response.Data.TaskID)
	assert.Equal(t, "task_local", response.NewAPITaskID)
	assert.Equal(t, "SUCCESS", response.NewAPIStatus)
	assert.NotContains(t, string(payload), `"task_id":"task_upstream"`)
	assert.Contains(t, string(response.Data.Data), "cgt-20260727152519-8c599")
	assert.Contains(t, string(response.Data.Data), "doubao-seedance-2-0-fast-260128")
}
