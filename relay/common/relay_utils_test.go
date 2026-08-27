package common

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeURLForLogMasksSensitiveQueryValues(t *testing.T) {
	rawURL := "https://example.test/v1beta/models/gemini:streamGenerateContent?alt=sse&key=sk-secret&access_token=ya29-secret&api-version=2024-02-01"

	got := SanitizeURLForLog(rawURL)

	assert.NotContains(t, got, "sk-secret")
	assert.NotContains(t, got, "ya29-secret")
	parsedURL, err := url.Parse(got)
	require.NoError(t, err)
	query := parsedURL.Query()
	assert.Equal(t, "***masked***", query.Get("key"))
	assert.Equal(t, "***masked***", query.Get("access_token"))
	assert.Equal(t, "sse", query.Get("alt"))
	assert.Equal(t, "2024-02-01", query.Get("api-version"))
}

func TestSanitizeURLForLogMasksAWSAndSecretLikeQueryKeys(t *testing.T) {
	rawURL := "https://example.test/path?X-Amz-Credential=credential&X-Amz-Signature=signature&session_token=session&client_secret=secret&model=gpt-test"

	got := SanitizeURLForLog(rawURL)

	assert.NotContains(t, got, "X-Amz-Credential=credential")
	assert.NotContains(t, got, "X-Amz-Signature=signature")
	assert.NotContains(t, got, "session_token=session")
	assert.NotContains(t, got, "client_secret=secret")
	parsedURL, err := url.Parse(got)
	require.NoError(t, err)
	query := parsedURL.Query()
	assert.Equal(t, "***masked***", query.Get("X-Amz-Credential"))
	assert.Equal(t, "***masked***", query.Get("X-Amz-Signature"))
	assert.Equal(t, "***masked***", query.Get("session_token"))
	assert.Equal(t, "***masked***", query.Get("client_secret"))
	assert.Equal(t, "gpt-test", query.Get("model"))
}

func TestSanitizeURLForLogKeepsURLWithoutSensitiveQuery(t *testing.T) {
	rawURL := "https://example.test/v1/chat/completions?api-version=2024-02-01&alt=sse"

	got := SanitizeURLForLog(rawURL)

	assert.Equal(t, rawURL, got)
}

func TestValidateMultipartDirectNormalizesImageField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.NewReader(`{"model":"wan2.7-i2v","prompt":"animate","image":" https://example.com/first.png "}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	info := &RelayInfo{
		TaskRelayInfo: &TaskRelayInfo{},
	}

	taskErr := ValidateMultipartDirect(context, info)

	require.Nil(t, taskErr)
	storedReq, err := GetTaskRequest(context)
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.com/first.png"}, storedReq.Images)
	require.Equal(t, constant.TaskActionGenerate, info.Action)
}

func TestValidateMultipartDirectKeepsSingleInputReferenceEnabledByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.NewReader(`{"model":"MiniMax-H3-C","prompt":"animate","input_reference":"https://example.com/first.png"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", body)
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	info := &RelayInfo{
		ChannelMeta:   &ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		TaskRelayInfo: &TaskRelayInfo{},
	}

	require.Nil(t, ValidateMultipartDirect(context, info))
	storedReq, err := GetTaskRequest(context)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://example.com/first.png"}, storedReq.Images)
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
}

func TestValidateMultipartDirectControlsMultiImageReferencesByChannelTypeAndSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"MiniMax-H3-C","prompt":"animate","input_reference":["https://example.com/one.png","https://example.com/two.png"]}`

	tests := []struct {
		name        string
		channelType int
		enabled     bool
		wantCode    string
	}{
		{
			name:        "OpenAI channel disabled",
			channelType: constant.ChannelTypeOpenAI,
			wantCode:    "multi_reference_disabled",
		},
		{
			name:        "OpenAI channel enabled",
			channelType: constant.ChannelTypeOpenAI,
			enabled:     true,
		},
		{
			name:        "unsupported channel type",
			channelType: constant.ChannelTypeSora,
			enabled:     true,
			wantCode:    "multi_reference_disabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = request
			info := &RelayInfo{
				ChannelMeta: &ChannelMeta{
					ChannelType: tt.channelType,
					ChannelSetting: dto.ChannelSettings{
						MultiImageReferenceEnabled: tt.enabled,
					},
				},
				TaskRelayInfo: &TaskRelayInfo{},
			}

			taskErr := ValidateMultipartDirect(context, info)
			if tt.wantCode != "" {
				require.NotNil(t, taskErr)
				assert.Equal(t, tt.wantCode, taskErr.Code)
				return
			}

			require.Nil(t, taskErr)
			storedReq, err := GetTaskRequest(context)
			require.NoError(t, err)
			assert.Equal(t, []string{
				"https://example.com/one.png",
				"https://example.com/two.png",
			}, storedReq.Images)
			assert.Equal(t, constant.TaskActionGenerate, info.Action)
		})
	}
}

// TestTaskDurationBounds guards the billing invariant that user-supplied
// video duration (a quota multiplier via OtherRatio "seconds") is bounded, so
// it can never overflow quota calculation into a negative charge.
func TestTaskDurationBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(t *testing.T, body string) (*gin.Context, *RelayInfo) {
		request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = request
		return context, &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	}

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "huge duration is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","duration":9999999999}`,
			wantErr: true,
		},
		{
			name:    "huge seconds string is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","seconds":"9999999999"}`,
			wantErr: true,
		},
		{
			name:    "negative duration is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","duration":-8}`,
			wantErr: true,
		},
		{
			name: "normal duration is accepted",
			body: `{"model":"sora-2","prompt":"a cat","seconds":"8"}`,
		},
		{
			name: "numeric seconds is accepted",
			body: `{"model":"ok-video-1.5-preview","prompt":"a cat","seconds":8}`,
		},
		{
			name:    "decimal seconds is rejected",
			body:    `{"model":"ok-video-1.5-preview","prompt":"a cat","seconds":8.5}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" (multipart direct)", func(t *testing.T) {
			context, info := newContext(t, tt.body)
			taskErr := ValidateMultipartDirect(context, info)
			if tt.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, "invalid_seconds", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
			}
		})
		t.Run(tt.name+" (basic task request)", func(t *testing.T) {
			context, info := newContext(t, tt.body)
			taskErr := ValidateBasicTaskRequest(context, info, constant.TaskActionGenerate)
			if tt.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, "invalid_seconds", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
			}
		})
	}
}
