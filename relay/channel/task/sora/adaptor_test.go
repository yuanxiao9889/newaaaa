package sora

import (
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

func TestOpenAIChannelPassesEnabledMultiImageReferenceArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"MiniMax-H3-C","prompt":"Keep both subjects","duration":10,"input_reference":["https://example.com/one.png","https://example.com/two.png"]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "MiniMax H3",
			ChannelSetting: dto.ChannelSettings{
				MultiImageReferenceEnabled: true,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(context, info))
	requestBody, err := adaptor.BuildRequestBody(context, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)

	var upstream map[string]any
	require.NoError(t, common.Unmarshal(encoded, &upstream))
	assert.Equal(t, "MiniMax H3", upstream["model"])
	assert.Equal(t, []any{
		"https://example.com/one.png",
		"https://example.com/two.png",
	}, upstream["input_reference"])
}

func TestParseTaskResultAcceptsStringErrorAndRunningStatus(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"id":"grok-deb7f9869d32",
		"task_id":"grok-deb7f9869d32",
		"status":"running",
		"progress":0,
		"error":""
	}`))
	if err != nil {
		t.Fatalf("ParseTaskResult returned error: %v", err)
	}
	if result.Status != model.TaskStatusInProgress {
		t.Fatalf("expected status %q, got %q", model.TaskStatusInProgress, result.Status)
	}
	if result.Reason != "" {
		t.Fatalf("expected empty reason, got %q", result.Reason)
	}
}

func TestParseTaskResultAcceptsCompletedWithoutURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"id":"video_33",
		"status":"completed",
		"progress":100
	}`))
	if err != nil {
		t.Fatalf("ParseTaskResult returned error: %v", err)
	}
	if result.Status != model.TaskStatusSuccess {
		t.Fatalf("expected status %q, got %q", model.TaskStatusSuccess, result.Status)
	}
	if result.Url != "" {
		t.Fatalf("expected empty url, got %q", result.Url)
	}
}

func TestParseTaskResultAcceptsSucceededAndExtractsVideoURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"id":"grok-58b24b68819e",
		"status":"succeeded",
		"progress":100,
		"error":"",
		"data":{
			"video_url":"https://dl.example.test/result.mp4",
			"video_urls":["https://dl.example.test/result.mp4"]
		},
		"result":{
			"final_url":"https://dl.example.test/result-from-result.mp4"
		}
	}`))
	if err != nil {
		t.Fatalf("ParseTaskResult returned error: %v", err)
	}
	if result.Status != model.TaskStatusSuccess {
		t.Fatalf("expected status %q, got %q", model.TaskStatusSuccess, result.Status)
	}
	if result.Url != "https://dl.example.test/result.mp4" {
		t.Fatalf("expected data video url, got %q", result.Url)
	}
}
