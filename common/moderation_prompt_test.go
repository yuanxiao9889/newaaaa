package common

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureModerationPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"chat", `{"messages":[{"role":"user","content":[{"type":"text","text":"画一朵花"},{"type":"image_url","image_url":{"url":"secret-media"}}]},{"role":"tool","content":"secret-tool"}],"api_key":"secret-key","metadata":{"prompt":"secret-metadata"}}`, "[messages[0] (user)]\n画一朵花\n\n"},
		{"responses", `{"instructions":"Keep it simple","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"A flower"}]},{"type":"function_call_output","output":"secret-tool"}]}`, "[instructions]\nKeep it simple\n\n[input[0] (user)]\nA flower\n\n"},
		{"responses string", `{"input":"A flower"}`, "[input]\nA flower\n\n"},
		{"gemini", `{"contents":[{"role":"user","parts":[{"text":"花"},{"inlineData":{"data":"secret-media"}}]}],"systemInstruction":{"parts":[{"text":"简洁"}]}}`, "[contents[0] (user)]\n花\n\n[systemInstruction]\n简洁\n\n"},
		{"claude", `{"system":[{"type":"text","text":"简洁"}],"messages":[{"role":"user","content":[{"type":"text","text":"花"},{"type":"tool_result","content":"secret-tool"}]}]}`, "[system]\n简洁\n\n[messages[0] (user)]\n花\n\n"},
		{"image", `{"prompt":"A flower","negative_prompt":"Blur","image":"secret-media"}`, "[prompt]\nA flower\n\n[negative_prompt]\nBlur\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/test", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			storage, err := GetBodyStorage(c)
			require.NoError(t, err)
			t.Cleanup(func() { CleanupBodyStorage(c) })
			_, err = storage.Seek(3, io.SeekStart)
			require.NoError(t, err)
			snapshot := CaptureModerationPrompt(c)
			assert.Equal(t, "captured", snapshot.Status)
			assert.Equal(t, tc.want, snapshot.Text)
			assert.NotContains(t, snapshot.Text, "secret-")
			position, err := storage.Seek(0, io.SeekCurrent)
			require.NoError(t, err)
			assert.Equal(t, int64(3), position, "audit must not disturb retries")
		})
	}
}

func TestModerationPromptBoundsAndRedaction(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)
	c.Request.Header.Set("Authorization", "Bearer actual-credential-123")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("image", "private.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("secret-media"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("api_key", "secret-field"))
	require.NoError(t, writer.WriteField("prompt", "花 actual-credential-123 sk-123456789012345678 password=secret-password "+strings.Repeat("中", 12000)))
	require.NoError(t, writer.Close())
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Set(KeyRequestBody, body.Bytes())
	snapshot := CaptureModerationPrompt(c)
	assert.Equal(t, "captured", snapshot.Status)
	assert.True(t, snapshot.Truncated)
	assert.True(t, snapshot.Redacted)
	assert.True(t, utf8.ValidString(snapshot.Text))
	assert.LessOrEqual(t, len(snapshot.Text), 32*1024)
	assert.Contains(t, snapshot.Text, "花")
	for _, value := range []string{"actual-credential-123", "123456789012345678", "secret-password", "secret-media", "secret-field"} {
		assert.NotContains(t, snapshot.Text, value)
	}

	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(KeyRequestBody, []byte(strings.Repeat("x", 16*1024*1024+1)))
	assert.Equal(t, "body_too_large", CaptureModerationPrompt(c).Status)
	c.Set(KeyRequestBody, []byte(`{"prompt":`))
	assert.Equal(t, "invalid_body", CaptureModerationPrompt(c).Status)
	c.Set(KeyRequestBody, []byte(`{"image":"secret-media"}`))
	assert.Equal(t, "no_text", CaptureModerationPrompt(c).Status)
	c.Set(KeyRequestBody, nil)
	c.Request.Body = io.NopCloser(strings.NewReader(`{"prompt":"must not consume live body"}`))
	assert.Equal(t, "body_unavailable", CaptureModerationPrompt(c).Status)
	live, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	assert.Contains(t, string(live), "must not consume live body")
}
