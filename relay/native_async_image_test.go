package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
