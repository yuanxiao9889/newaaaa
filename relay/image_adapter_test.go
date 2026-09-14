package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageAdapterFixture() *dto.ImageAdapterConfig {
	return &dto.ImageAdapterConfig{Enabled: true,
		Request: dto.ImageAdapterRequestConfig{
			GenerationsPath: "/v1/images/generations", EditsPath: "/v1/images/generations",
			FieldMap:   map[string]string{"quality": "detail_level"},
			ValueMap:   map[string]map[string]string{"quality": {"low": "Low", "medium": "Medium", "high": "High", "xhigh": "Extra High", "max": "Max"}},
			SizeMap:    map[string]map[string]string{"2048x2048": {"quality": "2K", "aspect_ratio": "1:1"}},
			DropFields: []string{"size", "response_format", "output_format"}, RejectFields: []string{"mask", "background"},
		},
		References: &dto.ImageAdapterReferenceConfig{Field: "content", Encoding: "image_url_content", MaxImages: 16, UploadLocal: true},
		Task: &dto.ImageAdapterTaskConfig{IDPaths: []string{"id", "task_id"}, StatusPath: "status", ResultURLPath: "data.0.url", ResultB64Path: "data.0.b64_json", ErrorPath: "error",
			PollURLTemplate: "/v1/images/generations/{task_id}", ContentURLTemplate: "/v1/images/{task_id}/content", PendingStates: []string{"queued", "in_progress"}, SuccessStates: []string{"completed"}, FailureStates: []string{"failed"}, PollIntervalSeconds: 4},
	}
}

func TestConfiguredImageSubmissionAndPolling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	for _, edit := range []bool{false, true} {
		name := "generations"
		if edit {
			name = "edits"
		}
		t.Run(name, func(t *testing.T) {
			config := imageAdapterFixture()
			require.NoError(t, config.Validate())
			storageDir := t.TempDir()
			t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", storageDir)
			previousAddress := system_setting.ServerAddress
			system_setting.ServerAddress = "https://relay.example"
			t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
			var sample bytes.Buffer
			require.NoError(t, png.Encode(&sample, image.NewRGBA(image.Rect(0, 0, 2, 2))))
			posts := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer fixture-upstream-key", r.Header.Get("Authorization"))
				if r.Method == http.MethodGet {
					assert.Equal(t, "/v1/images/generations/task-fixture", r.URL.Path)
					_, _ = io.WriteString(w, `{"status":"completed","data":[{"url":"https://cdn.example/result.png"}]}`)
					return
				}
				posts++
				assert.Equal(t, "/v1/images/generations", r.URL.Path)
				assert.Equal(t, "", r.URL.RawQuery)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				var payload map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &payload))
				assert.Equal(t, "upstream-image-model", payload["model"])
				assert.Equal(t, "Extra High", payload["detail_level"])
				assert.Equal(t, "2K", payload["quality"])
				assert.Equal(t, "1:1", payload["aspect_ratio"])
				assert.Equal(t, true, payload["async"])
				assert.Equal(t, false, payload["stream"])
				assert.Equal(t, float64(1), payload["n"])
				assert.NotContains(t, payload, "size")
				if edit {
					items, ok := payload["content"].([]any)
					require.True(t, ok)
					require.Len(t, items, 1)
					item := items[0].(map[string]any)
					assert.Equal(t, "reference_image", item["role"])
					imageURL := item["image_url"].(map[string]any)["url"].(string)
					assert.True(t, strings.HasPrefix(imageURL, "https://relay.example/mj/upload/"))
					stored, err := os.ReadFile(filepath.Join(storageDir, filepath.Base(imageURL)))
					require.NoError(t, err)
					assert.Equal(t, sample.Bytes(), stored)
				} else {
					assert.NotContains(t, payload, "content")
				}
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, `{"task_id":"task-fixture","status":"queued"}`)
			}))
			defer upstream.Close()
			var body bytes.Buffer
			contentType := "application/json"
			if edit {
				writer := multipart.NewWriter(&body)
				part, err := writer.CreateFormFile("image", "reference.png")
				require.NoError(t, err)
				_, err = part.Write(sample.Bytes())
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				contentType = writer.FormDataContentType()
			} else {
				body.WriteString(`{"model":"public-image-model","prompt":"draw"}`)
			}
			c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/"+name+"?async=true", &body)
			c.Request.Header.Set("Content-Type", contentType)
			if edit {
				require.NoError(t, c.Request.ParseMultipartForm(1<<20))
				defer c.Request.MultipartForm.RemoveAll()
			}
			c.Set("async_image_worker", true)
			c.Set("model_mapping", `{"public-image-model":"upstream-image-model"}`)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "public-image-model")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "fixture-upstream-key")
			common.SetContextKey(c, constant.ContextKeyChannelId, 17)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ImageAdapter: config})
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"async": true, "n": 1, "stream": false})
			mode := relayconstant.RelayModeImagesGenerations
			if edit {
				mode = relayconstant.RelayModeImagesEdits
			}
			request := &dto.ImageRequest{Model: "public-image-model", Prompt: "draw", Size: "2048x2048", Quality: "xhigh"}
			info := &relaycommon.RelayInfo{Request: request, OriginModelName: request.Model, RelayMode: mode, RelayFormat: types.RelayFormatOpenAIImage, RequestURLPath: "/v1/images/" + name}
			require.Nil(t, ImageHelper(c, info))
			value, exists := c.Get(NativeAsyncImageFormat)
			require.True(t, exists)
			snapshot := value.(*NativeAsyncImageSnapshot)
			assert.Equal(t, upstream.URL+"/v1/images/generations/task-fixture", snapshot.PollURL)
			assert.Equal(t, upstream.URL+"/v1/images/task-fixture/content", snapshot.ContentURL)
			storedSnapshot, err := common.Marshal(snapshot)
			require.NoError(t, err)
			assert.NotContains(t, string(storedSnapshot), "fixture-upstream-key")
			var reloaded NativeAsyncImageSnapshot
			require.NoError(t, common.Unmarshal(storedSnapshot, &reloaded))
			result, err := pollNativeAsyncImageResult(context.Background(), upstream.Client(), nativeAsyncImagePollEntry{statusURL: reloaded.PollURL, contentURL: reloaded.ContentURL, apiKey: "fixture-upstream-key", taskConfig: reloaded.Task})
			require.NoError(t, err)
			assert.Contains(t, string(result), "https://cdn.example/result.png")
			assert.Equal(t, 1, posts, "polling a restored task must never submit another generation")
		})
	}
}

func TestConfiguredImageRejectsUnmappedCapabilitiesAndChangedCounts(t *testing.T) {
	for _, test := range []struct{ field, value string }{{"size", "1232x1232"}, {"quality", "auto"}, {"background", "transparent"}, {"mask", "https://cdn.example/mask.png"}} {
		t.Run(test.field, func(t *testing.T) {
			payload := map[string]any{"model": "model", "prompt": "draw", "size": "2048x2048", "quality": "high", test.field: test.value}
			raw, err := common.Marshal(payload)
			require.NoError(t, err)
			var request dto.ImageRequest
			require.NoError(t, common.Unmarshal(raw, &request))
			c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json")
			_, err = openai.ConvertConfiguredImageRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{ImageAdapter: imageAdapterFixture()}}}, request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.field)
		})
	}
	n := uint(2)
	require.Error(t, openai.ValidateConfiguredImageCount([]byte(`{"n":1}`), &dto.ImageRequest{N: &n}))
	require.Error(t, openai.ValidateConfiguredImageCount([]byte(`{"n":2,"create_count":1}`), &dto.ImageRequest{N: &n}))
	require.NoError(t, openai.ValidateConfiguredImageCount([]byte(`{"n":2,"create_count":2}`), &dto.ImageRequest{N: &n}))
}

func TestConfiguredImageTaskNestedResponsesAndIDEncoding(t *testing.T) {
	config := imageAdapterFixture().Task
	config.IDPaths = []string{"data.job_id"}
	config.PollURLTemplate = "/jobs?job={task_id}"
	snapshot, err := configuredImageTaskSnapshot([]byte(`{"data":{"job_id":"job+a&b"}}`), config)
	require.NoError(t, err)
	parsed, err := url.Parse(snapshot.PollURL)
	require.NoError(t, err)
	assert.Equal(t, "job+a&b", parsed.Query().Get("job"))
	for _, payload := range []string{`{}`, `{"data":{"job_id":"../secret"}}`, `{"data":{"job_id":".."}}`} {
		_, err = configuredImageTaskSnapshot([]byte(payload), config)
		require.Error(t, err)
	}
	config.StatusPath = "job.state"
	config.ResultURLPath = "job.output.url"
	config.ErrorPath = "job.error"
	result, err := normalizeConfiguredImageTaskResponse([]byte(`{"job":{"state":"completed","output":{"url":"https://cdn.example/a.png"}}}`), config)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"completed","data":[{"url":"https://cdn.example/a.png","b64_json":"","revised_prompt":""}]}`, string(result))
	result, err = normalizeConfiguredImageTaskResponse([]byte(`{"job":{"state":"failed","error":"provider unavailable"}}`), config)
	require.NoError(t, err)
	assert.Contains(t, string(result), "provider unavailable")
	_, err = normalizeConfiguredImageTaskResponse([]byte(`{"job":{"state":"unknown"}}`), config)
	require.Error(t, err)
}

func TestImageAdapterReferenceOrderAndLimits(t *testing.T) {
	config := imageAdapterFixture().References
	var request dto.ImageRequest
	require.NoError(t, common.Unmarshal([]byte(`{"input_reference":["https://cdn.example/second.png","https://cdn.example/first.png"]}`), &request))
	urls, err := service.PrepareImageAdapterReferences(nil, &request, config)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://cdn.example/second.png", "https://cdn.example/first.png"}, urls)
	for _, raw := range []string{`{"image":"https://cdn.example/a.png","images":["https://cdn.example/b.png"]}`, `{"image":"http://127.0.0.1/a.png"}`, `{"image":"http://user:pass@cdn.example/a.png"}`} {
		require.NoError(t, common.Unmarshal([]byte(raw), &request))
		_, err = service.PrepareImageAdapterReferences(nil, &request, config)
		require.Error(t, err)
	}
	refs := make([]string, 17)
	for i := range refs {
		refs[i] = "https://cdn.example/a.png"
	}
	raw, err := common.Marshal(map[string]any{"images": refs})
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(raw, &request))
	_, err = service.PrepareImageAdapterReferences(nil, &request, config)
	require.Error(t, err)
	var img bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.White)
	require.NoError(t, png.Encode(&img, canvas))
	raw, err = common.Marshal(map[string]any{"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(img.Bytes())})
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(raw, &request))
	config.UploadLocal = false
	_, err = service.PrepareImageAdapterReferences(nil, &request, config)
	require.ErrorContains(t, err, "upload_local")
}

func TestImageAdapterMultipartValidationAndChannelRetry(t *testing.T) {
	service.InitHttpClient()
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	for _, background := range []string{"transparent", ""} {
		t.Run("background="+background, func(t *testing.T) {
			previous := system_setting.ServerAddress
			system_setting.ServerAddress = "https://relay.example"
			t.Cleanup(func() { system_setting.ServerAddress = previous })
			t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", t.TempDir())
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for key, value := range map[string]string{"model": "image-model", "prompt": "draw", "size": "2048x2048", "quality": "high", "seed": "42", "background": background} {
				require.NoError(t, writer.WriteField(key, value))
			}
			part, err := writer.CreateFormFile("image[]", "source.png")
			require.NoError(t, err)
			_, err = part.Write(pngBytes.Bytes())
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			originalContentType := writer.FormDataContentType()
			c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
			c.Request.Header.Set("Content-Type", originalContentType)
			request, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
			require.NoError(t, err)
			defer c.Request.MultipartForm.RemoveAll()
			posts := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				if posts == 1 {
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
					var wire map[string]any
					require.NoError(t, common.DecodeJson(r.Body, &wire))
					assert.Equal(t, "42", wire["random_seed"])
					w.WriteHeader(503)
					_, _ = io.WriteString(w, `{"error":{"message":"temporarily unavailable"}}`)
					return
				}
				assert.Equal(t, "/v1/images/edits", r.URL.Path)
				require.NoError(t, r.ParseMultipartForm(1<<20))
				defer r.MultipartForm.RemoveAll()
				require.Len(t, r.MultipartForm.File["image"], 1)
				file, err := r.MultipartForm.File["image"][0].Open()
				require.NoError(t, err)
				defer file.Close()
				data, err := io.ReadAll(file)
				require.NoError(t, err)
				assert.Equal(t, pngBytes.Bytes(), data)
				w.WriteHeader(202)
				_, _ = io.WriteString(w, `{"id":"normal-task","poll_url":"/normal-task"}`)
			}))
			defer upstream.Close()
			config := imageAdapterFixture()
			config.Request.FieldMap["seed"] = "random_seed"
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "image-model")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "fixture-key")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ImageAdapter: config})
			info := &relaycommon.RelayInfo{Request: request, OriginModelName: request.Model, RelayMode: relayconstant.RelayModeImagesEdits, RelayFormat: types.RelayFormatOpenAIImage, RequestURLPath: "/v1/images/edits"}
			apiErr := ImageHelper(c, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, originalContentType, c.Request.Header.Get("Content-Type"))
			if background != "" {
				assert.Equal(t, 400, apiErr.StatusCode)
				assert.Zero(t, posts)
				return
			}
			assert.Equal(t, 503, apiErr.StatusCode)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
			c.Set("async_image_worker", true)
			require.Nil(t, ImageHelper(c, info))
			assert.Equal(t, 2, posts)
		})
	}
}

func TestImageAdapterTaskCountRejectedBeforeSubmission(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; w.WriteHeader(500) }))
	defer server.Close()
	n := uint(2)
	request := &dto.ImageRequest{Model: "model", Prompt: "draw", N: &n}
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "model")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ImageAdapter: imageAdapterFixture()})
	apiErr := ImageHelper(c, &relaycommon.RelayInfo{Request: request, OriginModelName: "model", RelayMode: relayconstant.RelayModeImagesGenerations, RelayFormat: types.RelayFormatOpenAIImage})
	require.NotNil(t, apiErr)
	assert.Equal(t, 400, apiErr.StatusCode)
	assert.Zero(t, posts)
}

func TestImageAdapterIndexedMultipartReferenceOrder(t *testing.T) {
	previous := system_setting.ServerAddress
	system_setting.ServerAddress = "https://relay.example"
	t.Cleanup(func() { system_setting.ServerAddress = previous })
	directory := t.TempDir()
	t.Setenv("MIDJOURNEY_UPLOAD_STORAGE_PATH", directory)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	images := map[string][]byte{}
	for _, name := range []string{"image[2]", "image[0]", "image[1]"} {
		var data bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: name[6], A: 255})
		require.NoError(t, png.Encode(&data, img))
		images[name] = data.Bytes()
		part, err := writer.CreateFormFile(name, "source.png")
		require.NoError(t, err)
		_, err = part.Write(data.Bytes())
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	require.NoError(t, c.Request.ParseMultipartForm(1<<20))
	defer c.Request.MultipartForm.RemoveAll()
	urls, err := service.PrepareImageAdapterReferences(c, &dto.ImageRequest{}, imageAdapterFixture().References)
	require.NoError(t, err)
	require.Len(t, urls, 3)
	for i, name := range []string{"image[0]", "image[1]", "image[2]"} {
		stored, err := os.ReadFile(filepath.Join(directory, filepath.Base(urls[i])))
		require.NoError(t, err)
		assert.Equal(t, images[name], stored)
	}
}

func TestImageAdapterLargeBase64AndCredentialFreeRedirect(t *testing.T) {
	service.InitHttpClient()
	t.Run("base64 above one MiB", func(t *testing.T) {
		var pngBytes bytes.Buffer
		encoder := png.Encoder{CompressionLevel: png.NoCompression}
		require.NoError(t, encoder.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 600, 600))))
		encoded := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
		require.Greater(t, len(encoded), 1<<20)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := common.Marshal(map[string]any{"status": "completed", "data": []map[string]string{{"b64_json": encoded}}})
			require.NoError(t, err)
			_, _ = w.Write(body)
		}))
		defer server.Close()
		result, err := pollNativeAsyncImageResult(context.Background(), server.Client(), nativeAsyncImagePollEntry{statusURL: server.URL, taskConfig: imageAdapterFixture().Task})
		require.NoError(t, err)
		assert.Contains(t, string(result), encoded)
	})
	t.Run("redirect credentials", func(t *testing.T) {
		fetch := system_setting.GetFetchSetting()
		original := *fetch
		fetch.EnableSSRFProtection = false
		t.Cleanup(func() { *fetch = original })
		var pngBytes bytes.Buffer
		require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
		download := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, r.Header.Get("Authorization"))
			assert.Empty(t, r.Header.Get("X-API-Key"))
			assert.Empty(t, r.Header.Get("Cookie"))
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes.Bytes())
		}))
		defer download.Close()
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer fixture-secret", r.Header.Get("Authorization"))
			w.Header().Set("Set-Cookie", "session=private")
			http.Redirect(w, r, download.URL, http.StatusFound)
		}))
		defer upstream.Close()
		client := *upstream.Client()
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
		result, err := fetchNativeAsyncImageResult(context.Background(), &client, nativeAsyncImagePollEntry{contentURL: upstream.URL, apiKey: "fixture-secret", taskConfig: imageAdapterFixture().Task})
		require.NoError(t, err)
		assert.Contains(t, string(result), base64.StdEncoding.EncodeToString(pngBytes.Bytes()))
	})
}

func TestImageAdapterPresetOnlyAppliesToMatchedServerContext(t *testing.T) {
	config := imageAdapterFixture()
	config.Models = []string{"configured-model"}
	override := map[string]any{"operations": []any{map[string]any{"path": "async", "mode": "set", "value": true, "conditions": []any{map[string]any{"path": "image_adapter_enabled", "mode": "full", "value": true}}}}}
	for _, test := range []struct {
		name, model string
		enabled     bool
		mode        int
		want        bool
	}{
		{"enabled", "configured-model", true, relayconstant.RelayModeImagesGenerations, true},
		{"disabled", "configured-model", false, relayconstant.RelayModeImagesGenerations, false},
		{"different model", "other-model", true, relayconstant.RelayModeImagesGenerations, false},
		{"chat", "configured-model", true, relayconstant.RelayModeChatCompletions, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config.Enabled = test.enabled
			info := &relaycommon.RelayInfo{RelayMode: test.mode, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model, ChannelSetting: dto.ChannelSettings{ImageAdapter: config}, ParamOverride: override}}
			out, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"image_adapter_enabled":true}`), info)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, common.Unmarshal(out, &body))
			assert.Equal(t, test.want, body["async"] == true)
		})
	}
}
