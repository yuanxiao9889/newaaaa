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
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
)

const NativeAsyncImageFormat = "native_async_image"

// NativeAsyncImageSnapshot stores resumable request provenance, never credentials.
type NativeAsyncImageSnapshot struct {
	ID             string                         `json:"id"`
	PollURL        string                         `json:"poll_url"`
	ContentURL     string                         `json:"content_url"`
	ChannelID      int                            `json:"channel_id"`
	KeyFingerprint string                         `json:"key_fingerprint"`
	Task           *kitdto.ImageAdapterTaskConfig `json:"image_adapter_task,omitempty"`
}

func parseNativeAsyncImageSnapshot(resp *http.Response, info *relaycommon.RelayInfo) (*NativeAsyncImageSnapshot, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > nativeAsyncImagePollMaxResponseBytes {
		return nil, fmt.Errorf("native image task response too large")
	}
	var snapshot NativeAsyncImageSnapshot
	// Task rules come only from channel configuration, never upstream JSON.
	if adapter := info.ChannelSetting.ImageAdapter; adapter.Matches(info.UpstreamModelName) && adapter.Task != nil {
		configured, err := configuredImageTaskSnapshot(body, adapter.Task)
		if err != nil {
			return nil, err
		}
		snapshot = *configured
	} else {
		if err = common.Unmarshal(body, &snapshot); err != nil {
			return nil, fmt.Errorf("invalid native image task response")
		}
		snapshot.Task = nil
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

// SaveNativeAsyncImageSnapshot replaces the replayable POST with a GET-only resume
// snapshot. A resumed worker must never submit an already accepted task again.
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
	entry := nativeAsyncImagePollEntry{settings: channel.GetSetting(), taskConfig: snapshot.Task}
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
					body, err = normalizeConfiguredImageTaskResponse(body, entry.taskConfig)
					if err != nil {
						return nil, err
					}
				}
				var payload struct {
					Status string          `json:"status"`
					Data   []dto.ImageData `json:"data"`
					Error  struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if common.Unmarshal(body, &payload) != nil {
					return nil, fmt.Errorf("invalid native image task status response")
				}
				switch strings.ToLower(strings.TrimSpace(payload.Status)) {
				case "failed", "failure", "error", "expired", "cancelled", "canceled":
					message := payload.Error.Message
					if message == "" {
						message = "native image task failed"
					}
					// Channel credentials must never leak through an upstream error message.
					message = strings.ReplaceAll(message, entry.apiKey, "[redacted]")
					return nil, fmt.Errorf("%s", message)
				case "succeeded", "success", "completed":
					for _, item := range payload.Data {
						if strings.TrimSpace(item.Url) != "" || strings.TrimSpace(item.B64Json) != "" {
							return body, nil
						}
					}
					return fetchNativeAsyncImageResult(ctx, client, entry)
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
