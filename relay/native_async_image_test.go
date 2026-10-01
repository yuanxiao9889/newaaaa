package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/ali"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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
