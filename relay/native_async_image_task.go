package relay

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const NativeAsyncImageFormat = "native_async_image"

// NativeImageTaskError is a confirmed upstream terminal failure, distinct from
// polling transport errors and failures downloading an already generated image.
type NativeImageTaskError struct {
	Message    string
	Code       string
	Type       string
	StatusCode int
	Retryable  bool
}

func (e *NativeImageTaskError) Error() string { return e.Message }

func nativeImageTaskFailure(status string, body []byte, apiKey string) *NativeImageTaskError {
	value := gjson.GetBytes(body, "error")
	message := value.Get("message").String()
	if value.Type == gjson.String {
		message = value.String()
	}
	if message == "" {
		message = "native image task failed"
	}
	failure := &NativeImageTaskError{
		Message: message, Code: value.Get("code").String(),
		Type: value.Get("type").String(), StatusCode: int(value.Get("status_code").Int()),
	}
	if apiKey != "" {
		for _, field := range []*string{&failure.Message, &failure.Code, &failure.Type} {
			*field = strings.ReplaceAll(*field, apiKey, "[redacted]")
		}
	}
	if failure.StatusCode == 0 {
		failure.StatusCode = int(value.Get("status").Int())
	}
	if status != "failed" && status != "failure" && status != "error" {
		return failure
	}
	// Permanent errors take precedence over broad upstream server-error codes.
	description := strings.ToLower(failure.Code + " " + failure.Type + " " + failure.Message)
	for _, permanent := range []string{"content_filter", "content_policy", "moderation", "safety", "unsafe", "policy", "sensitive", "nsfw", "审核", "违规", "敏感", "invalid", "bad_request", "parameter", "参数", "suspended", "violat"} {
		if strings.Contains(description, permanent) {
			return failure
		}
	}
	if value.Get("retryable").Type == gjson.False {
		return failure
	}
	classification := failure.Code
	if classification == "" {
		classification = failure.Type
	}
	switch strings.ToLower(classification) {
	case "rate_limit_exceeded", "rate_limit_error", "too_many_requests", "429":
		failure.Retryable, failure.StatusCode = true, http.StatusTooManyRequests
	case "server_error", "internal_error", "internal_server_error", "service_unavailable", "overloaded_error", "500", "502", "503":
		failure.Retryable = true
	}
	if failure.StatusCode == 429 || failure.StatusCode == 500 || failure.StatusCode == 502 || failure.StatusCode == 503 {
		failure.Retryable = true
	}
	// Compatibility for providers returning a message without an error code.
	if failure.Code == "" && failure.StatusCode == 0 {
		switch strings.ToLower(strings.TrimSpace(failure.Message)) {
		case "请求过于频繁,请稍后重试", "请求过于频繁，请稍后重试", "too many requests", "rate limit exceeded":
			failure.Code, failure.StatusCode, failure.Retryable = "rate_limit_exceeded", http.StatusTooManyRequests, true
		case "generation failed: generate error: an error occurred.", "service unavailable", "internal server error":
			failure.Code, failure.Retryable = "upstream_generation_failed", true
		}
	}
	if failure.Retryable && failure.StatusCode == 0 {
		failure.StatusCode = http.StatusBadGateway
	}
	return failure
}

// NativeAsyncImageSnapshot stores resumable request provenance, never credentials.
type NativeAsyncImageSnapshot struct {
	ID             string                         `json:"id"`
	PollURL        string                         `json:"poll_url"`
	ContentURL     string                         `json:"content_url"`
	ChannelID      int                            `json:"channel_id"`
	KeyFingerprint string                         `json:"key_fingerprint"`
	Task           *kitdto.ImageAdapterTaskConfig `json:"image_adapter_task,omitempty"`
	Provider       string                         `json:"native_provider,omitempty"`
}

func parseNativeAsyncImageSnapshot(resp *http.Response, info *relaycommon.RelayInfo, nativeConfig ...*kitdto.ImageAdapterTaskConfig) (*NativeAsyncImageSnapshot, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > nativeAsyncImagePollMaxResponseBytes {
		return nil, fmt.Errorf("native image task response too large")
	}
	var snapshot NativeAsyncImageSnapshot
	// Task rules come only from channel configuration or the native adaptor,
	// never from upstream JSON.
	var taskConfig *kitdto.ImageAdapterTaskConfig
	if adapter := info.ChannelSetting.ImageAdapter; adapter.Matches(info.UpstreamModelName) {
		taskConfig = adapter.Task
	}
	if taskConfig == nil && len(nativeConfig) > 0 && nativeConfig[0] != nil {
		taskConfig = nativeConfig[0]
		if info.ChannelType == constant.ChannelTypeAli {
			snapshot.Provider = "ali_image"
		}
	}
	if taskConfig != nil {
		configured, err := configuredImageTaskSnapshot(body, taskConfig)
		if err != nil {
			return nil, err
		}
		configured.Provider = snapshot.Provider
		snapshot = *configured
	} else {
		if err = common.Unmarshal(body, &snapshot); err != nil {
			return nil, fmt.Errorf("invalid native image task response")
		}
		snapshot.Task = nil
		// Provider parsers are host-selected; an upstream task envelope cannot
		// opt itself into a different protocol interpretation.
		snapshot.Provider = ""
	}
	if strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(snapshot.PollURL) == "" {
		return nil, fmt.Errorf("native image task response missing id or poll_url")
	}
	var ok bool
	snapshot.PollURL, ok = resolveNativeAsyncImagePollURL(snapshot.PollURL, info.ChannelBaseUrl)
	if !ok {
		return nil, fmt.Errorf("invalid native image task poll origin")
	}
	if snapshot.ContentURL == "" && snapshot.Task == nil {
		snapshot.ContentURL = nativeAsyncImageContentURL(snapshot.PollURL)
	}
	if snapshot.ContentURL != "" {
		snapshot.ContentURL, ok = resolveNativeAsyncImagePollURL(snapshot.ContentURL, info.ChannelBaseUrl)
		if !ok {
			return nil, fmt.Errorf("invalid native image task content origin")
		}
	}
	snapshot.ChannelID = info.ChannelId
	snapshot.KeyFingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(info.ApiKey)))
	return &snapshot, nil
}

// SaveNativeAsyncImageSnapshot selects a GET-only resume snapshot. The original
// request, when retained in ImageRetry, may only be used after a confirmed
// retryable terminal failure, never to replay an already accepted task.
func SaveNativeAsyncImageSnapshot(task *model.Task, snapshot *NativeAsyncImageSnapshot) error {
	body, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	dir := filepath.Clean(common.GetAsyncImageRequestStoragePath())
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, task.TaskID+".native")
	if err = os.WriteFile(path+".tmp", body, 0600); err != nil {
		return err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return err
	}
	task.PrivateData.RequestBodyPath = path
	task.PrivateData.RequestBodySize = int64(len(body))
	task.PrivateData.RequestRelayFormat = NativeAsyncImageFormat
	task.PrivateData.UpstreamTaskID = snapshot.ID
	return nil
}

// ResumeNativeAsyncImageTask runs independently of the caller's polling requests.
// It produces image bytes for the existing store-before-settle worker pipeline.
func ResumeNativeAsyncImageTask(ctx context.Context, task *model.Task) ([]byte, int, string, error) {
	body, err := os.ReadFile(task.PrivateData.RequestBodyPath)
	if err != nil {
		return nil, 0, "", err
	}
	var snapshot NativeAsyncImageSnapshot
	if err = common.Unmarshal(body, &snapshot); err != nil {
		return nil, 0, "", err
	}
	channel, err := model.GetChannelById(snapshot.ChannelID, true)
	if err != nil {
		return nil, 0, "", fmt.Errorf("native image task channel unavailable")
	}
	if snapshot.Provider != "" && (snapshot.Provider != "ali_image" || channel.Type != constant.ChannelTypeAli || snapshot.Task == nil) {
		return nil, snapshot.ChannelID, "", fmt.Errorf("stored native image provider does not match channel")
	}
	entry := nativeAsyncImagePollEntry{settings: channel.GetSetting(), taskConfig: snapshot.Task, provider: snapshot.Provider}
	if snapshot.Task != nil {
		if err := (&kitdto.ImageAdapterConfig{Enabled: true, Task: snapshot.Task}).Validate(); err != nil {
			return nil, snapshot.ChannelID, "", fmt.Errorf("invalid stored image task configuration")
		}
	}
	for _, key := range channel.GetKeys() {
		if fmt.Sprintf("%x", sha256.Sum256([]byte(key))) == snapshot.KeyFingerprint {
			entry.apiKey = key
			break
		}
	}
	if entry.apiKey == "" {
		return nil, snapshot.ChannelID, "", fmt.Errorf("native image task credential no longer available")
	}
	var ok bool
	entry.statusURL, ok = resolveNativeAsyncImagePollURL(snapshot.PollURL, channel.GetBaseURL())
	if !ok {
		return nil, snapshot.ChannelID, "", fmt.Errorf("native image task poll origin changed")
	}
	if snapshot.ContentURL != "" {
		entry.contentURL, ok = resolveNativeAsyncImagePollURL(snapshot.ContentURL, channel.GetBaseURL())
		if !ok {
			return nil, snapshot.ChannelID, "", fmt.Errorf("native image task content origin changed")
		}
	}
	if advanced := channel.GetOtherSettings().AdvancedCustom; advanced != nil {
		if route, matched := advanced.MatchPath("/v1/images/generations"); matched && route.Auth != nil {
			entry.authType = strings.TrimSpace(route.Auth.Type)
			entry.authName = strings.TrimSpace(route.Auth.Name)
			entry.authValue = strings.ReplaceAll(route.Auth.Value, "{api_key}", entry.apiKey)
		}
	}
	client, err := service.GetHttpClientWithProxySettings(entry.settings.Proxy, entry.settings)
	if err != nil {
		return nil, snapshot.ChannelID, entry.settings.Proxy, err
	}
	// Never forward channel credentials to a redirected origin.
	boundedClient := *client
	boundedClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, nativeAsyncImagePollTTL)
	defer cancel()
	result, err := pollNativeAsyncImageResult(ctx, &boundedClient, entry)
	return result, snapshot.ChannelID, entry.settings.Proxy, err
}

func pollNativeAsyncImageResult(ctx context.Context, client *http.Client, entry nativeAsyncImagePollEntry) ([]byte, error) {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.statusURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		applyNativeAsyncImageAuth(req, entry, entry.statusURL)
		resp, err := client.Do(req)
		if err == nil {
			responseLimit := int64(nativeAsyncImagePollMaxResponseBytes)
			if entry.taskConfig != nil && entry.taskConfig.ResultB64Path != "" {
				// A 64 MiB image expands to roughly 86 MiB as base64 JSON.
				responseLimit = ((64<<20)+2)/3*4 + (1 << 20)
			}
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
			resp.Body.Close()
			if int64(len(body)) > responseLimit {
				return nil, fmt.Errorf("native image task status response exceeds the configured image size limit")
			}
			if readErr == nil && (resp.StatusCode == 200 || resp.StatusCode == 202) {
				if entry.taskConfig != nil {
					if entry.provider == "ali_image" {
						body, err = normalizeLegacyAliImageTaskResponse(body, entry.taskConfig)
					} else {
						body, err = normalizeConfiguredImageTaskResponse(body, entry.taskConfig)
					}
					if err != nil {
						return nil, err
					}
				}
				var payload struct {
					Status string          `json:"status"`
					Data   []dto.ImageData `json:"data"`
				}
				if common.Unmarshal(body, &payload) != nil {
					return nil, fmt.Errorf("invalid native image task status response")
				}
				switch strings.ToLower(strings.TrimSpace(payload.Status)) {
				case "failed", "failure", "error", "expired", "cancelled", "canceled":
					return nil, nativeImageTaskFailure(strings.ToLower(strings.TrimSpace(payload.Status)), body, entry.apiKey)
				case "succeeded", "success", "completed":
					for _, item := range payload.Data {
						if strings.TrimSpace(item.Url) != "" || strings.TrimSpace(item.B64Json) != "" {
							return body, nil
						}
					}
					result, err := fetchNativeAsyncImageResult(ctx, client, entry)
					if err != nil {
						return nil, err
					}
					// The content endpoint returns image bytes; billing usage belongs
					// to the completed status response and must survive the download.
					if usage := gjson.GetBytes(body, "usage"); usage.Exists() {
						return sjson.SetRawBytes(result, "usage", []byte(usage.Raw))
					}
					return result, nil
				case "pending", "submitted", "queued", "processing", "running", "in_progress":
				default:
					return nil, fmt.Errorf("unrecognized native image task status")
				}
			} else if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
				return nil, fmt.Errorf("native image task query failed with HTTP %d", resp.StatusCode)
			}
		}
		interval := 2 * time.Second
		if entry.taskConfig != nil {
			seconds := entry.taskConfig.PollIntervalSeconds
			if seconds == 0 {
				seconds = 4
			}
			interval = time.Duration(seconds) * time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Legacy Ali image models return either results[] or multimodal choices[].
// The durable queue stores one image, so text-only parts and empty entries do
// not prevent it from finding the first real image in either response shape.
func normalizeLegacyAliImageTaskResponse(body []byte, config *kitdto.ImageAdapterTaskConfig) ([]byte, error) {
	normalized, err := normalizeConfiguredImageTaskResponse(body, config)
	if err != nil || gjson.GetBytes(normalized, "status").String() != "completed" {
		return normalized, err
	}
	var image dto.ImageData
	for _, result := range gjson.GetBytes(body, "output.results").Array() {
		if value := result.Get("url"); value.Type == gjson.String {
			image.Url = strings.TrimSpace(value.String())
		}
		if value := result.Get("b64_image"); value.Type == gjson.String {
			image.B64Json = strings.TrimSpace(value.String())
		}
		if image.Url != "" || image.B64Json != "" {
			break
		}
	}
	if image.Url == "" && image.B64Json == "" {
	choices:
		for _, choice := range gjson.GetBytes(body, "output.choices").Array() {
			for _, part := range choice.Get("message.content").Array() {
				value := part.Get("image")
				if value.Type != gjson.String || strings.TrimSpace(value.String()) == "" {
					continue
				}
				content := strings.TrimSpace(value.String())
				if strings.HasPrefix(content, "http://") || strings.HasPrefix(content, "https://") {
					image.Url = content
				} else {
					image.B64Json = content
				}
				break choices
			}
		}
	}
	if image.Url == "" && image.B64Json == "" {
		return normalized, nil
	}
	data, err := common.Marshal([]dto.ImageData{image})
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(normalized, "data", data)
}

func fetchNativeAsyncImageResult(ctx context.Context, client *http.Client, entry nativeAsyncImagePollEntry) ([]byte, error) {
	if entry.contentURL == "" {
		return nil, fmt.Errorf("completed image task returned no image or configured content URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.contentURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*, application/octet-stream")
	applyNativeAsyncImageAuth(req, entry, entry.contentURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("native image content download failed")
	}
	if entry.taskConfig != nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location, err := resp.Location()
		resp.Body.Close()
		if err != nil || location.User != nil || (location.Scheme != "http" && location.Scheme != "https") || location.Fragment != "" {
			return nil, fmt.Errorf("invalid image content redirect")
		}
		// Public artifact downloads use the existing SSRF-protected client and
		// a fresh request, so no upstream credential or cookie follows redirects.
		if err = service.ValidateSSRFProtectedFetchURL(location.String()); err != nil {
			return nil, fmt.Errorf("image content redirect is not an allowed download URL")
		}
		publicRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("invalid image content download request")
		}
		resp, err = service.GetSSRFProtectedHTTPClient().Do(publicRequest)
		if err != nil {
			return nil, fmt.Errorf("image content redirect download failed")
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("native image content download failed with HTTP %d", resp.StatusCode)
	}
	const maxBytes = 64 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("native image content read failed")
	}
	if len(data) == 0 || len(data) > maxBytes {
		return nil, fmt.Errorf("native image content has invalid size")
	}
	return common.Marshal(dto.ImageResponse{Created: time.Now().Unix(), Data: []dto.ImageData{{B64Json: base64.StdEncoding.EncodeToString(data)}}})
}
