package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOaiBufferedStreamHandlerAcceptsEmptyArrayDeltaOnFinish(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1784273780,"model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"**Refining image reference replacements"},"finish_reason":null}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1784273780,"model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"reasoning_content":"\n\n"},"finish_reason":null}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1784273780,"model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"content":"@"},"finish_reason":null}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1784273780,"model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"content":"\u56fe"},"finish_reason":null}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1784273780,"model":"gpt-5.6-sol","choices":[{"index":0,"delta":[],"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":7,"total_tokens":9}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "buffered-stream-test")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"},
		RelayFormat: types.RelayFormatOpenAI,
	}

	usage, err := OaiBufferedStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 9, usage.TotalTokens)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	var got dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &got))
	require.Len(t, got.Choices, 1)
	require.Equal(t, "@\u56fe", got.Choices[0].Message.Content)
	require.Equal(t, "**Refining image reference replacements\n\n", got.Choices[0].Message.GetReasoningContent())
	require.Equal(t, "stop", got.Choices[0].FinishReason)
	require.NotContains(t, recorder.Body.String(), "data:")
}

func TestAdaptorBuffersUnexpectedSSEForNonStreamingRequest(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1784273780,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	requestStream := false
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true, // compatible_handler may set this from the upstream Content-Type.
		Request:     &dto.GeneralOpenAIRequest{Stream: &requestStream},
		RelayFormat: types.RelayFormatOpenAI,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	usage, err := (&Adaptor{}).DoResponse(c, resp, info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), `"content":"hello"`)
	require.NotContains(t, recorder.Body.String(), "data:")
}

func TestOpenaiHandlerDetectsSSEBodyWithIncorrectJSONContentType(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1784273780,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	usage, err := OpenaiHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), `"content":"hello"`)
	require.NotContains(t, recorder.Body.String(), "data:")
}
