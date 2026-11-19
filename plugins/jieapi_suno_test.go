package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJieAPISuno(t *testing.T) {
	source, err := builtinplugins.Source("jieapi-suno")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "jieapi-suno"})
	require.NoError(t, err)
	require.NoError(t, jsplugin.ValidateV1Meta(plugin.Meta))
	ctx := map[string]any{"baseUrl": "https://jieapi.com", "apiKey": "fixture-key", "action": "music", "taskId": "upstream-1",
		"requestBody": map[string]any{"mv": "chirp-crow", "prompt": "[Verse] Hello", "tags": "pop", "make_instrumental": false}}
	request, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://jieapi.com/suno/submit/music", alibabaObject(t, request)["url"])
	query, err := plugin.Engine.Call(t.Context(), "buildQueryRequest", ctx)
	require.NoError(t, err)
	assert.Equal(t, "GET", alibabaObject(t, query)["method"])
	assert.Equal(t, "https://jieapi.com/suno/fetch/upstream-1", alibabaObject(t, query)["url"])
	_, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"body": map[string]any{"code": "success", "data": "../invalid"}})
	require.Error(t, err)
	clips := []any{map[string]any{"id": "clip-1", "audio_url": "https://cdn.example/a.mp3"}, map[string]any{"id": "clip-2", "audio_url": "https://cdn.example/b.mp3"}}
	result, err := plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, map[string]any{"code": "success", "data": map[string]any{"task_id": "upstream-1", "status": "SUCCESS", "data": clips}})
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", alibabaObject(t, result)["status"])
	_, err = plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, map[string]any{"code": "success", "data": map[string]any{"task_id": "other", "status": "SUCCESS", "data": clips}})
	require.Error(t, err)
	usage, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", ctx, result, clips)
	require.NoError(t, err)
	assert.EqualValues(t, 2, alibabaObject(t, usage)["clips"])
	task := map[string]any{"status": "SUCCESS", "data": clips}
	artifacts, err := plugin.Engine.Call(t.Context(), "listArtifacts", task)
	require.NoError(t, err)
	list := artifacts.([]any)
	require.Len(t, list, 2)
	task["artifactKey"] = alibabaObject(t, list[1])["key"]
	task["clientRequest"] = map[string]any{"method": "GET"}
	content, err := plugin.Engine.Call(t.Context(), "buildContentRequest", task)
	require.NoError(t, err)
	assert.Equal(t, true, alibabaObject(t, content)["credentialless"])
	assert.Equal(t, "https://cdn.example/b.mp3", alibabaObject(t, content)["url"])
	assert.NotContains(t, alibabaObject(t, content), "headers")
}
