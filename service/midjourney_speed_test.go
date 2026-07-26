package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMjRequestModelUsesSpeedModeForRouting(t *testing.T) {
	tests := []struct {
		name      string
		relayMode int
		request   dto.MidjourneyRequest
		wantModel string
		wantError string
	}{
		{
			name:      "legacy imagine keeps canonical model",
			relayMode: relayconstant.RelayModeMidjourneyImagine,
			request:   dto.MidjourneyRequest{},
			wantModel: "mj_imagine",
		},
		{
			name:      "fast imagine",
			relayMode: relayconstant.RelayModeMidjourneyImagine,
			request:   dto.MidjourneyRequest{SpeedMode: " FAST "},
			wantModel: "mj_fast_imagine",
		},
		{
			name:      "relax upscale action",
			relayMode: relayconstant.RelayModeMidjourneyAction,
			request: dto.MidjourneyRequest{
				SpeedMode: "relax",
				CustomId:  "MJ::JOB::upsample::2::task",
			},
			wantModel: "mj_relax_upscale",
		},
		{
			name:      "invalid speed mode",
			relayMode: relayconstant.RelayModeMidjourneyImagine,
			request:   dto.MidjourneyRequest{SpeedMode: "turbo"},
			wantError: "invalid_speed_mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelName, mjError, success := GetMjRequestModel(tt.relayMode, &tt.request)
			if tt.wantError != "" {
				require.NotNil(t, mjError)
				assert.Equal(t, tt.wantError, mjError.Description)
				assert.False(t, success)
				return
			}

			require.Nil(t, mjError)
			assert.True(t, success)
			assert.Equal(t, tt.wantModel, modelName)
		})
	}
}

func TestDoMidjourneyHttpRequestRemovesSpeedModeBeforeForwarding(t *testing.T) {
	var forwarded map[string]interface{}
	var forwardedAuthorization string
	var forwardedMjSecret string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(body, &forwarded))
		forwardedAuthorization = r.Header.Get("Authorization")
		forwardedMjSecret = r.Header.Get("mj-api-secret")
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(`{"code":1,"description":"ok","result":"task-id"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)
	originalHTTPClient := httpClient
	httpClient = upstream.Client()
	t.Cleanup(func() {
		httpClient = originalHTTPClient
	})

	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/mj/submit/imagine",
		strings.NewReader(`{"prompt":"cat","speedMode":"relax","base64Array":[]}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("channel_key", "upstream-key")

	response, _, err := DoMidjourneyHttpRequest(context, time.Second, upstream.URL)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, 1, response.Response.Code)
	assert.Equal(t, "cat", forwarded["prompt"])
	assert.NotContains(t, forwarded, "speedMode")
	assert.Equal(t, "Bearer upstream-key", forwardedAuthorization)
	assert.Equal(t, "upstream-key", forwardedMjSecret)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDoMidjourneyHttpRequestForwardsSpeedModeToT8Star(t *testing.T) {
	var forwarded map[string]interface{}
	originalHTTPClient := httpClient
	httpClient = &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.NoError(t, common.Unmarshal(body, &forwarded))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"code":1,"description":"ok","result":"task-id"}`)),
				Request:    request,
			}, nil
		}),
	}
	t.Cleanup(func() {
		httpClient = originalHTTPClient
	})

	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/mj/submit/imagine",
		strings.NewReader(`{"prompt":"cat","speedMode":"relax","base64Array":[]}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("channel_key", "upstream-key")

	response, _, err := DoMidjourneyHttpRequest(
		context,
		time.Second,
		"https://ai.t8star.org/mj/submit/imagine",
	)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, 1, response.Response.Code)
	assert.Equal(t, "relax", forwarded["speedMode"])
}

func TestShouldForwardMidjourneySpeedModeOnlyMatchesT8StarAPIHost(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "https://ai.t8star.org/mj/submit/imagine", want: true},
		{url: "https://AI.T8STAR.ORG:443/mj/submit/imagine", want: true},
		{url: "https://example.com/ai.t8star.org/mj/submit/imagine", want: false},
		{url: "https://ai.t8star.org.example.com/mj/submit/imagine", want: false},
		{url: "://invalid", want: false},
	}

	for _, test := range tests {
		assert.Equal(t, test.want, shouldForwardMidjourneySpeedMode(test.url), test.url)
	}
}
