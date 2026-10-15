package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/ali"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveNativeAsyncImagePollURLKeepsPollOnChannelOrigin(t *testing.T) {
	tests := []struct {
		name string
		poll string
		want string
		ok   bool
	}{
		{name: "relative", poll: "/tasks/123", want: "https://provider.example/tasks/123", ok: true},
		{name: "same origin", poll: "https://provider.example/tasks/123", want: "https://provider.example/tasks/123", ok: true},
		{name: "foreign host", poll: "https://attacker.example/tasks/123", ok: false},
		{name: "userinfo", poll: "https://provider.example@attacker.example/tasks/123", ok: false},
		{name: "foreign scheme", poll: "file:///etc/passwd", ok: false},
		{name: "foreign port", poll: "https://provider.example:8443/tasks/123", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := resolveNativeAsyncImagePollURL(test.poll, "https://provider.example")
			assert.Equal(t, test.ok, ok)
			if test.ok {
				assert.Equal(t, test.want, got)
			}
		})
	}
}

func TestLegacyAliNativeImageTaskConfiguration(t *testing.T) {
	defaults := (&ali.Adaptor{LegacyAsyncImage: true}).ImageTaskConfig()
	t.Run("Wan edit task can return image content after text", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			_, err := io.WriteString(w, `{"output":{"task_status":"SUCCEEDED","choices":[{"message":{"content":[{"text":"result"},{"image":"https://cdn.example/edited.png"}]}}]}}`)
			assert.NoError(t, err)
		}))
		defer server.Close()
		result, err := pollNativeAsyncImageResult(context.Background(), server.Client(), nativeAsyncImagePollEntry{
			statusURL: server.URL + "/api/v1/tasks/wan-edit-job", taskConfig: defaults, provider: "ali_image",
		})
		require.NoError(t, err)
		assert.Contains(t, string(result), "https://cdn.example/edited.png")
	})
	t.Run("explicit channel task configuration takes priority", func(t *testing.T) {
		configured := imageAdapterFixture()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeAli,
			ChannelBaseUrl: "https://provider.example", UpstreamModelName: "wanx2.1-t2i-turbo",
			ChannelSetting: dto.ChannelSettings{ImageAdapter: configured},
		}}
		response := &http.Response{Body: io.NopCloser(strings.NewReader(`{"task_id":"configured-job","status":"queued","output":{"task_id":"native-job"}}`))}
		snapshot, err := parseNativeAsyncImageSnapshot(response, info, defaults)
		require.NoError(t, err)
		assert.Equal(t, "configured-job", snapshot.ID)
		assert.Empty(t, snapshot.Provider, "explicit channel rules must not use the legacy provider parser")
		assert.Equal(t, "https://provider.example/v1/images/generations/configured-job", snapshot.PollURL)
	})
	for _, test := range []struct {
		name        string
		channelType int
		useDefault  bool
		want        string
	}{
		{"Ali default config marks provider", constant.ChannelTypeAli, true, "ali_image"},
		{"other channel cannot use Ali parser", constant.ChannelTypeOpenAI, true, ""},
		{"upstream cannot forge parser", constant.ChannelTypeAli, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: test.channelType, ChannelBaseUrl: "https://provider.example"}}
			response := &http.Response{Body: io.NopCloser(strings.NewReader(`{"id":"job","poll_url":"/api/v1/tasks/job","output":{"task_id":"job"},"native_provider":"ali_image"}`))}
			var config *dto.ImageAdapterTaskConfig
			if test.useDefault {
				config = defaults
			}
			snapshot, err := parseNativeAsyncImageSnapshot(response, info, config)
			require.NoError(t, err)
			assert.Equal(t, test.want, snapshot.Provider)
		})
	}
	for _, status := range []string{"UNKNOWN", "FAILED", "CANCELED"} {
		t.Run(status, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				assert.Equal(t, http.MethodGet, r.Method)
				_, err := io.WriteString(w, `{"output":{"task_status":"`+status+`","code":"TaskError","message":"status detail"}}`)
				assert.NoError(t, err)
			}))
			defer server.Close()
			_, err := pollNativeAsyncImageResult(context.Background(), server.Client(), nativeAsyncImagePollEntry{
				statusURL: server.URL + "/api/v1/tasks/job", taskConfig: defaults,
			})
			require.Error(t, err)
			var confirmed *NativeImageTaskError
			assert.Equal(t, status != "UNKNOWN", errors.As(err, &confirmed), "only confirmed terminal failures can permit another submission")
			assert.Equal(t, 1, requests)
		})
	}
}

func TestNativeAsyncImageContentURLPreservesQuery(t *testing.T) {
	assert.Equal(t,
		"https://provider.example/v1/images/generations/task-123/content?token=abc",
		nativeAsyncImageContentURL("https://provider.example/v1/images/generations/task-123?token=abc"),
	)
}

func TestRewriteNativeAsyncImageTaskEnvelopeUsesRelayLocalURLs(t *testing.T) {
	body := rewriteNativeAsyncImageTaskEnvelope([]byte(`{"id":"task-123","status":"succeeded","poll_url":"https://upstream.example/task-123","content_url":"https://upstream.example/task-123/content"}`), "task-123")
	assert.Contains(t, string(body), `"poll_url":"/v1/images/generations/task-123"`)
	assert.Contains(t, string(body), `"content_url":"/v1/images/generations/task-123/content"`)
}

func TestNativeAsyncImagePublicEndpointsProtectOwnershipAndProviderDetails(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.WriteHeader(http.StatusBadRequest)
		}
		_, err := io.WriteString(w, `{"id":"upstream-secret-id","status":"failed","model":"real-provider-model","platform":"secret-platform","error":{"message":"channel=163 https://provider.invalid token=secret"}}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)
	info := &relaycommon.RelayInfo{UserId: 7, OriginModelName: "public-model", ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL}}
	RegisterNativeAsyncImagePoll("task_privacy", upstream.URL+"/status", upstream.URL+"/content", info)
	t.Cleanup(func() { nativeAsyncImagePolls.Delete("task_privacy") })
	for _, tc := range []struct {
		name    string
		userID  int
		content bool
		code    int
	}{
		{"owner status", 7, false, http.StatusOK},
		{"owner content error", 7, true, http.StatusBadRequest},
		{"foreign status", 8, false, http.StatusNotFound},
		{"foreign content", 8, true, http.StatusNotFound},
		{"anonymous status", 0, false, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/generations/task_privacy", nil)
			c.Params = gin.Params{{Key: "task_id", Value: "task_privacy"}}
			c.Set("id", tc.userID)
			if tc.content {
				GetNativeAsyncImageContent(c)
			} else {
				GetNativeAsyncImageStatus(c)
			}
			require.Equal(t, tc.code, recorder.Code)
			for _, secret := range []string{"upstream-secret-id", "real-provider-model", "secret-platform", "provider.invalid", "channel=163"} {
				assert.NotContains(t, recorder.Body.String(), secret)
			}
			if tc.userID != 7 {
				assert.Equal(t, before, requests)
				assert.Contains(t, recorder.Body.String(), `"code":"task_not_found"`)
			}
			if tc.code == http.StatusOK {
				var body map[string]any
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
				assert.Equal(t, "public-model", body["model"])
				assert.Equal(t, "failed", body["status"])
			}
		})
	}
	// Expired entries cannot be fetched even by the owner.
	value, _ := nativeAsyncImagePolls.Load("task_privacy")
	entry := value.(nativeAsyncImagePollEntry)
	entry.expiresAt = time.Now().Add(-time.Minute)
	nativeAsyncImagePolls.Store("task_privacy", entry)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "task_id", Value: "task_privacy"}}
	c.Set("id", 7)
	GetNativeAsyncImageStatus(c)
	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"task_expired"`)
}

func TestNativeAsyncImageErrorsIncludeSafeCodesAndGuidance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		content    bool
		status     int
		body       string
		code       string
		wantStatus int
	}{
		{"failed image input", false, 200, `{"status":"failed","error":{"code":"invalid_image_input","message":"invalid input at https://provider.invalid, channel=163, model=private-model"}}`, "invalid_image_input", 200},
		{"failed moderation", false, 200, `{"status":"failed","error":{"code":"generation_failed","message":"图片审核失败，请重试 channel=163"}}`, "content_policy_violation", 200},
		{"failed timeout", false, 200, `{"status":"failed","error":"context deadline exceeded https://provider.invalid"}`, "request_timeout", 200},
		{"upstream quota is not user quota", false, 200, `{"status":"failed","error":{"code":"insufficient_token_quota","message":"secret"}}`, "service_quota_exceeded", 200},
		{"failed unknown detail", false, 200, `{"status":"failed","error":{"code":"private-model","message":"channel=163 https://provider.invalid token=secret"}}`, "generation_failed", 200},
		{"HTTP error body", true, 400, `{"error":{"code":"invalid_image_input","message":"channel=163"}}`, "invalid_image_input", 400},
		{"HTTP rate limit", false, 429, `{"error":{"message":"secret"}}`, "rate_limit_exceeded", 429},
		{"HTTP provider auth", true, 401, `{"error":{"code":"invalid_token","message":"secret"}}`, "service_auth_error", 502},
		{"HTTP provider access", false, 403, `{"error":{"message":"secret"}}`, "service_access_error", 502},
		{"non JSON error", false, 503, `<html>https://provider.invalid channel=163 secret</html>`, "service_unavailable", 503},
		{"malformed status", false, 200, `<html>https://provider.invalid channel=163 secret</html>`, "generation_failed", 200},
		{"content contains JSON error", true, 200, `{"error":{"code":"invalid_image_input","message":"channel=163"}}`, "invalid_image_input", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, err := io.WriteString(w, tc.body)
				assert.NoError(t, err)
			}))
			t.Cleanup(upstream.Close)
			RegisterNativeAsyncImagePoll("task_error", upstream.URL+"/status", upstream.URL+"/content", &relaycommon.RelayInfo{
				UserId: 7, OriginModelName: "public-model", ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL},
			})
			t.Cleanup(func() { nativeAsyncImagePolls.Delete("task_error") })
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/generations/task_error", nil)
			c.Params = gin.Params{{Key: "task_id", Value: "task_error"}}
			c.Set("id", 7)
			if tc.content {
				GetNativeAsyncImageContent(c)
			} else {
				GetNativeAsyncImageStatus(c)
			}
			require.Equal(t, tc.wantStatus, recorder.Code)
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
					Hint    string `json:"hint"`
				} `json:"error"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
			assert.Equal(t, tc.code, body.Error.Code)
			assert.Contains(t, body.Error.Message, "["+tc.code+"]")
			assert.NotEmpty(t, body.Error.Hint)
			for _, secret := range []string{"provider.invalid", "channel=163", "private-model", "secret"} {
				assert.NotContains(t, recorder.Body.String(), secret)
			}
		})
	}
	t.Run("transport error", func(t *testing.T) {
		upstream := httptest.NewServer(http.NotFoundHandler())
		upstream.Close()
		RegisterNativeAsyncImagePoll("task_offline", upstream.URL, upstream.URL+"/content", &relaycommon.RelayInfo{
			UserId: 7, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL},
		})
		t.Cleanup(func() { nativeAsyncImagePolls.Delete("task_offline") })
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/generations/task_offline", nil)
		c.Params = gin.Params{{Key: "task_id", Value: "task_offline"}}
		c.Set("id", 7)
		GetNativeAsyncImageStatus(c)
		assert.Equal(t, http.StatusBadGateway, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"code":"service_unavailable"`)
		assert.NotContains(t, recorder.Body.String(), upstream.URL)
	})
}
