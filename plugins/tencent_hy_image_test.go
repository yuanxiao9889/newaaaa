package plugins_test

import (
	"bytes"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hyModel = "hy-image-v3.5-preview"

func newHyImagePlugin(t *testing.T) (*jsplugin.Registry, *jsplugin.LoadedPlugin) {
	t.Helper()
	source, err := builtinplugins.Source("tencent-hy-image")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "tencent-hy-image"})
	require.NoError(t, err)
	return registry, plugin
}

func hyImageResponse() map[string]any {
	return map[string]any{
		"id": "trace-only-not-a-queryable-task", "model": "HY-Image-3.5-preview-internal-version",
		"choices": []any{map[string]any{
			"delta":         map[string]any{"type": "image", "image": map[string]any{"url": "https://cdn.example/final.png?signature=fixture"}},
			"finish_reason": nil,
		}},
		"usage": map[string]any{"total_tokens": 19000}, "tokenhub_usage": map[string]any{"total_tokens": 20000},
		"assembled_history": []any{
			map[string]any{"role": "assistant", "reasoning": "edit context", "tool_calls": []any{map[string]any{
				"id": "generate@call_0", "type": "function", "function": map[string]any{"name": "generate", "arguments": "{}"},
			}}},
			map[string]any{"role": "tool", "tool_call_id": "generate@call_0", "content": []any{map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example/intermediate.png"}, "is_generate": true,
			}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Done"}}},
		},
	}
}

func TestTencentHyImageSubmission(t *testing.T) {
	registry, plugin := newHyImagePlugin(t)
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits"} {
		binding, found := registry.Generation().LookupEndpoint(http.MethodPost, path, hyModel)
		require.True(t, found)
		assert.Same(t, plugin, binding.Plugin)
	}
	assert.Empty(t, plugin.Meta.ChannelTypes, "binding must not replace the existing Tencent chat adaptor")

	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{
		"model": "hy-alias", "operation": "generate", "body": map[string]any{"kind": "json", "value": map[string]any{
			"model": "hy-alias", "prompt": "a cat", "size": "4096x4096", "seed": 0, "n": 1,
			"use_search_tool": map[string]any{"value": false}, "response_format": "b64_json",
		}},
	})
	require.NoError(t, err)
	intent := alibabaObject(t, value)
	assert.Equal(t, "hy-alias", intent["model"])
	assert.Equal(t, "text_to_image", intent["action"])
	request := intent["requestBody"].(map[string]any)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: plugin.Meta.BaseURL, UpstreamModelName: hyModel, ApiKey: "fixture-provider-key"},
		OriginModelName: "hy-alias", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public", Action: "text_to_image"},
	}
	adaptor := taskplugin.New(plugin)
	adaptor.Init(info)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	c.Set("task_request", request)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	var upstream map[string]any
	require.NoError(t, common.Unmarshal(encoded, &upstream))
	assert.Equal(t, hyModel, upstream["model"])
	assert.Equal(t, "4096x4096", upstream["size"])
	assert.Equal(t, float64(0), upstream["seed"])
	assert.Equal(t, map[string]any{"value": false}, upstream["use_search_tool"])
	assert.NotContains(t, upstream, "n")
	assert.NotContains(t, upstream, "response_format")
	assert.NotContains(t, string(encoded), "fixture-provider-key")
	url, err := adaptor.BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://tokenhub.tencentmaas.com/v1/wand/hunyuan-image/v35-generation", url)
	httpRequest, err := http.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, err)
	require.NoError(t, adaptor.BuildRequestHeader(c, httpRequest, info))
	assert.Equal(t, "Bearer fixture-provider-key", httpRequest.Header.Get("Authorization"))
	facts, err := adaptor.ExtractUsageFactsValidated(c, info)
	require.NoError(t, err)
	assert.Equal(t, float64(1), facts["image_count"])
	assert.Greater(t, facts["tokens"].(float64), float64(0))

	body := hyImageResponse()
	encoded, err = common.Marshal(body)
	require.NoError(t, err)
	parsed, taskErr := adaptor.ParseResponse(c, &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(encoded))}, info)
	require.Nil(t, taskErr)
	require.NotNil(t, parsed.Immediate)
	assert.Equal(t, "task_public", parsed.UpstreamTaskID)
	assert.Equal(t, "SUCCESS", parsed.Immediate.Status)
	assert.Equal(t, "https://cdn.example/final.png?signature=fixture", parsed.Immediate.Url)
	assert.Equal(t, map[string]any{"image_count": float64(1), "tokens": float64(20000)}, parsed.Immediate.UsageFacts)
	require.NotNil(t, parsed.Immediate.TokenUsage)
	assert.Equal(t, 20000, parsed.Immediate.TokenUsage.TotalTokens)
	assert.Zero(t, parsed.Immediate.TokenUsage.PromptTokens)
	assert.Zero(t, parsed.Immediate.TokenUsage.CompletionTokens)
	// Task expressions produce dollars, with an explicit /1M for token prices.
	cost, _, err := billingexpr.RunExprWithRequest(`tier("image", u("tokens") * 5 / 1000000)`, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: parsed.Immediate.UsageFacts})
	require.NoError(t, err)
	assert.InDelta(t, 0.1, cost, 0.0000001)
	value, err = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "render"}, map[string]any{}, map[string]any{"status": "SUCCESS", "data": body})
	require.NoError(t, err)
	rendered := alibabaObject(t, value)
	assert.Equal(t, []any{map[string]any{"url": "https://cdn.example/final.png?signature=fixture"}}, rendered["data"])
	assert.Equal(t, map[string]any{"total_tokens": float64(20000)}, rendered["usage"])
}

func TestTencentHyImageNativeHistoryAndUploads(t *testing.T) {
	registry, plugin := newHyImagePlugin(t)
	path := "/tencent-hy-image/v1/wand/hunyuan-image/v35-generation"
	binding, found := registry.Generation().LookupDeclaredRoute(http.MethodPost, path)
	require.True(t, found)
	assert.Same(t, plugin, binding.Plugin)
	messages := []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "draw a cat"}}}}
	messages = append(messages, hyImageResponse()["assembled_history"].([]any)...)
	messages = append(messages, map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "make it white"}}})
	req := map[string]any{"model": hyModel, "messages": messages, "session": "conversation-1"}
	value, err := plugin.Engine.CallPath(t.Context(), "native", []string{binding.Route.Decode}, map[string]any{"body": map[string]any{"kind": "json", "value": req}})
	require.NoError(t, err)
	intent := alibabaObject(t, value)
	assert.Equal(t, "image_to_image", intent["action"])
	value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
		"requestBody": intent["requestBody"], "model": hyModel, "baseUrl": "https://tokenhub.tencentmaas.com/",
		"apiKey": "fixture-provider-key",
	})
	require.NoError(t, err)
	descriptor := alibabaObject(t, value)
	assert.Equal(t, "Bearer fixture-provider-key", descriptor["headers"].(map[string]any)["Authorization"])
	assert.Equal(t, alibabaObject(t, req), descriptor["body"])
	value, err = plugin.Engine.CallPath(t.Context(), "native", []string{binding.Route.Render}, map[string]any{}, map[string]any{"status": "SUCCESS", "data": hyImageResponse()})
	require.NoError(t, err)
	assert.Equal(t, alibabaObject(t, hyImageResponse()), alibabaObject(t, value), "preserve the exact history for the next edit")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"URL references", map[string]any{"kind": "json", "value": map[string]any{"prompt": "edit", "image": []string{"https://cdn.example/ref.png", "data:image/png;base64,YQ=="}}}},
		{"repeated multipart image fields", map[string]any{"kind": "multipart", "fields": map[string][]string{"prompt": {"edit"}, "seed": {"0"}, "use_search_tool": {`{"value":false}`}}, "files": []any{
			map[string]any{"ref": "request_file:image[]", "field": "image[]", "mimeType": "image/png", "size": 20},
			map[string]any{"ref": "request_file:image[]#1", "field": "image[]", "mimeType": "image/jpeg", "size": 30},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{"model": hyModel, "operation": "edit", "body": tc.body})
			require.NoError(t, err)
			intent := alibabaObject(t, value)
			assert.Equal(t, "image_to_image", intent["action"])
			request := intent["requestBody"].(map[string]any)
			content := request["messages"].([]any)[0].(map[string]any)["content"].([]any)
			require.Len(t, content, 3)
			if tc.body["kind"] == "multipart" {
				assert.Equal(t, float64(0), request["seed"])
				image := content[2].(map[string]any)["image_url"].(map[string]any)["url"].(map[string]any)
				assert.Equal(t, "request_file:image[]#1", image["__fileRef"])
				assert.Equal(t, float64(20*1024*1024), image["maxBytes"])
			}
		})
	}
}

func TestTencentHyImageValidation(t *testing.T) {
	_, plugin := newHyImagePlugin(t)
	tooManyImages := make([]string, 21)
	for i := range tooManyImages {
		tooManyImages[i] = "https://cdn.example/ref.png"
	}
	for _, tc := range []struct {
		name   string
		fields map[string]any
	}{
		{"missing prompt", map[string]any{"prompt": ""}},
		{"oversized count", map[string]any{"n": 18446744073686646784.0}},
		{"multiple outputs", map[string]any{"n": 2}},
		{"fractional count", map[string]any{"n": 1.5}},
		{"streaming", map[string]any{"stream": true}},
		{"oversized area", map[string]any{"size": "8192x8192"}},
		{"undersized dimension", map[string]any{"size": "255x1024"}},
		{"oversized dimension", map[string]any{"size": "8193x256"}},
		{"negative seed", map[string]any{"seed": -1}},
		{"unsafe seed", map[string]any{"seed": 9007199254740992.0}},
		{"negative area", map[string]any{"generate_max_pixels": -1}},
		{"zero resize area", map[string]any{"resize_max_pixels": 0}},
		{"search scalar", map[string]any{"use_search_tool": false}},
		{"watermark length", map[string]any{"footnote": "一二三四五六七八九十一二三四五六七"}},
		{"unsupported mask", map[string]any{"mask": "https://cdn.example/mask.png"}},
		{"bad reference", map[string]any{"image": "file:///private/file.png"}},
		{"too many references", map[string]any{"image": tooManyImages}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := map[string]any{"prompt": "a cat"}
			maps.Copy(req, tc.fields)
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{"model": hyModel, "operation": "generate", "body": map[string]any{"kind": "json", "value": req}})
			require.Error(t, err)
		})
	}
	_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{"model": hyModel, "operation": "edit", "body": map[string]any{"kind": "json", "value": map[string]any{"prompt": "edit"}}})
	require.ErrorContains(t, err, "image is required")
	for _, tc := range []struct {
		name   string
		fields map[string][]string
		files  []any
	}{
		{"repeated scalar", map[string][]string{"n": {"1", "2"}}, nil},
		{"oversized multipart count", map[string][]string{"n": {"18446744073686646784"}}, nil},
		{"oversized upload", nil, []any{map[string]any{"field": "image", "ref": "request_file:image", "mimeType": "image/png", "size": 20*1024*1024 + 1}}},
		{"unsupported upload", nil, []any{map[string]any{"field": "mask", "ref": "request_file:mask", "mimeType": "image/png", "size": 10}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string][]string{"prompt": {"edit"}}
			maps.Copy(fields, tc.fields)
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{
				"model": hyModel, "operation": "edit", "body": map[string]any{"kind": "multipart", "fields": fields, "files": tc.files},
			})
			require.Error(t, err)
		})
	}
}

func TestTencentHyImageCompletionUsageAndFailures(t *testing.T) {
	_, plugin := newHyImagePlugin(t)
	for _, tc := range []struct {
		name     string
		usage    any
		fallback any
		want     any
	}{
		{"billing counter wins", 1234, 999, float64(1234)},
		{"explicit zero is retained", 0, 999, float64(0)},
		{"fallback to vendor count", nil, 999, float64(999)},
		{"missing counts", nil, nil, nil},
		{"negative counts", -1, -2, nil},
		{"fractional counts", 1.5, nil, nil},
		{"overflow counts", 2147483648, nil, nil},
		{"maximum valid count", 2147483647, nil, float64(2147483647)},
		{"string counts", "1234", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := hyImageResponse()
			body["tokenhub_usage"] = map[string]any{"total_tokens": tc.usage}
			body["usage"] = map[string]any{"total_tokens": tc.fallback}
			value, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{}, map[string]any{"status": "SUCCESS"}, body)
			require.NoError(t, err)
			facts := alibabaObject(t, value)
			assert.Equal(t, float64(1), facts["image_count"])
			assert.Equal(t, tc.want, facts["tokens"])
			value, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"publicTaskId": "task_public"}, map[string]any{"body": body})
			require.NoError(t, err)
			immediate := alibabaObject(t, value)["immediate"].(map[string]any)
			assert.Equal(t, "SUCCESS", immediate["status"])
			if tc.want == nil {
				assert.NotContains(t, immediate, "tokenUsage")
			}
		})
	}
	body := hyImageResponse()
	body["error"] = map[string]any{"code": "content_filter", "message": "fixture-secret must not be exposed"}
	value, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"publicTaskId": "task_public"}, map[string]any{"body": body})
	require.NoError(t, err)
	encodedFailure, err := common.Marshal(value)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedFailure), "fixture-secret", "do not persist reflected credentials from upstream errors")
	immediate := alibabaObject(t, value)["immediate"].(map[string]any)
	assert.Equal(t, "FAILURE", immediate["status"])
	assert.NotContains(t, immediate["reason"], "fixture-secret")
	rendered, renderErr := plugin.Engine.CallPath(t.Context(), "native", []string{"imageCreated"}, map[string]any{}, map[string]any{"status": "FAILURE", "fail_reason": immediate["reason"], "data": body})
	require.NoError(t, renderErr)
	renderedBytes, err := common.Marshal(rendered)
	require.NoError(t, err)
	assert.NotContains(t, string(renderedBytes), "fixture-secret")
	value, err = plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{}, immediate, body)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"image_count": float64(0), "tokens": float64(0)}, alibabaObject(t, value))
	body = hyImageResponse()
	delete(body, "choices")
	_, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"publicTaskId": "task_public"}, map[string]any{"body": body})
	require.ErrorContains(t, err, "no final image URL", "history images must not be delivered as the result")
}
