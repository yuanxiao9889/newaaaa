package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	nativeAsyncImagePollTTL              = 30 * time.Minute
	nativeAsyncImagePollMaxResponseBytes = 1 << 20
)

type nativeAsyncImagePollEntry struct {
	statusURL  string
	contentURL string
	apiKey     string
	settings   dto.ChannelSettings
	authType   string
	authName   string
	authValue  string
	baseHost   string
	expiresAt  time.Time
	taskConfig *dto.ImageAdapterTaskConfig
	provider   string
}

var nativeAsyncImagePolls sync.Map

func RegisterNativeAsyncImagePoll(taskID, pollURL, contentURL string, info *relaycommon.RelayInfo) {
	if strings.TrimSpace(taskID) == "" || info == nil || info.ChannelMeta == nil {
		return
	}
	resolved, ok := resolveNativeAsyncImagePollURL(pollURL, info.ChannelBaseUrl)
	if !ok {
		return
	}
	entry := nativeAsyncImagePollEntry{
		statusURL:  resolved,
		contentURL: "",
		apiKey:     info.ApiKey,
		settings:   info.ChannelSetting,
		baseHost:   parsedHostname(info.ChannelBaseUrl),
		expiresAt:  time.Now().Add(nativeAsyncImagePollTTL),
	}
	if resolvedContent, ok := resolveNativeAsyncImagePollURL(contentURL, info.ChannelBaseUrl); ok {
		entry.contentURL = resolvedContent
	}
	if advanced := info.ChannelOtherSettings.AdvancedCustom; advanced != nil {
		if route, matched := advanced.MatchPath("/v1/images/generations"); matched && route.Auth != nil {
			entry.authType = strings.TrimSpace(route.Auth.Type)
			entry.authName = strings.TrimSpace(route.Auth.Name)
			entry.authValue = strings.ReplaceAll(route.Auth.Value, "{api_key}", info.ApiKey)
		}
	}
	nativeAsyncImagePolls.Store(taskID, entry)
}

// GetNativeAsyncImageContent proxies the completed image bytes when a native
// provider omits data[].url from its terminal status response. The client
// already uses this conventional content URL as its fallback.
func GetNativeAsyncImageContent(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	value, ok := nativeAsyncImagePolls.Load(taskID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "task not found", "type": "invalid_request_error"}})
		return
	}
	entry, ok := value.(nativeAsyncImagePollEntry)
	if !ok || time.Now().After(entry.expiresAt) {
		nativeAsyncImagePolls.Delete(taskID)
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "task expired", "type": "invalid_request_error"}})
		return
	}
	contentURL := entry.contentURL
	if contentURL == "" {
		contentURL = nativeAsyncImageContentURL(entry.statusURL)
	}
	client, err := service.GetHttpClientWithProxySettings(entry.settings.Proxy, entry.settings)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": fmt.Sprintf("create upstream client failed: %v", err), "type": "upstream_error"}})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, contentURL, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "upstream_error"}})
		return
	}
	req.Header.Set("Accept", "image/*, application/octet-stream")
	applyNativeAsyncImageAuth(req, entry, contentURL)
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "upstream_error"}})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "upstream_error"}})
		return
	}
	if len(body) > nativeAsyncImagePollMaxResponseBytes {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "upstream response is too large", "type": "upstream_error"}})
		return
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Data(resp.StatusCode, contentType, body)
}

func GetNativeAsyncImageStatus(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	value, ok := nativeAsyncImagePolls.Load(taskID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "task not found", "type": "invalid_request_error"}})
		return
	}
	entry, ok := value.(nativeAsyncImagePollEntry)
	if !ok || time.Now().After(entry.expiresAt) {
		nativeAsyncImagePolls.Delete(taskID)
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "task expired", "type": "invalid_request_error"}})
		return
	}
	client, err := service.GetHttpClientWithProxySettings(entry.settings.Proxy, entry.settings)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": fmt.Sprintf("create upstream client failed: %v", err), "type": "upstream_error"}})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, entry.statusURL, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "upstream_error"}})
		return
	}
	req.Header.Set("Accept", "application/json")
	applyNativeAsyncImageAuth(req, entry, entry.statusURL)
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "upstream_error"}})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "upstream_error"}})
		return
	}
	if len(body) > nativeAsyncImagePollMaxResponseBytes {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "upstream response is too large", "type": "upstream_error"}})
		return
	}
	body = rewriteNativeAsyncImageTaskEnvelope(body, taskID)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)
}

func applyNativeAsyncImageAuth(req *http.Request, entry nativeAsyncImagePollEntry, targetURL string) {
	switch entry.authType {
	case dto.AdvancedCustomAuthTypeNone:
	case dto.AdvancedCustomAuthTypeHeader:
		if entry.authName != "" {
			req.Header.Set(entry.authName, entry.authValue)
		}
	case dto.AdvancedCustomAuthTypeQuery:
		if entry.authName != "" {
			if parsed, err := url.Parse(targetURL); err == nil {
				query := parsed.Query()
				query.Set(entry.authName, entry.authValue)
				parsed.RawQuery = query.Encode()
				req.URL = parsed
			}
		}
	default:
		req.Header.Set("Authorization", "Bearer "+entry.apiKey)
	}
	if entry.authType == "" {
		req.Header.Set("Authorization", "Bearer "+entry.apiKey)
		req.Header.Set("X-API-Key", entry.apiKey)
	}
}

func registerNativeAsyncImageResponse(resp *http.Response, info *relaycommon.RelayInfo) bool {
	if resp == nil || resp.StatusCode != http.StatusAccepted || resp.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil || len(body) > nativeAsyncImagePollMaxResponseBytes {
		return false
	}
	_ = resp.Body.Close()
	var payload struct {
		ID         string `json:"id"`
		PollURL    string `json:"poll_url"`
		ContentURL string `json:"content_url"`
	}
	if common.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.ID) != "" && strings.TrimSpace(payload.PollURL) != "" {
		RegisterNativeAsyncImagePoll(payload.ID, payload.PollURL, payload.ContentURL, info)
		body = rewriteNativeAsyncImageTaskEnvelope(body, payload.ID)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return true
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return false
}

func rewriteNativeAsyncImageTaskEnvelope(body []byte, taskID string) []byte {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return body
	}
	var envelope map[string]json.RawMessage
	if common.Unmarshal(body, &envelope) != nil {
		return body
	}
	pollPath := "/v1/images/generations/" + url.PathEscape(taskID)
	contentPath := pollPath + "/content"
	if encoded, err := common.Marshal(pollPath); err == nil {
		envelope["poll_url"] = encoded
	}
	if encoded, err := common.Marshal(contentPath); err == nil {
		envelope["content_url"] = encoded
	}
	rewritten, err := common.Marshal(envelope)
	if err != nil {
		return body
	}
	return rewritten
}

func forwardNativeAsyncImageResponse(c *gin.Context, resp *http.Response) error {
	if c == nil || resp == nil || resp.Body == nil {
		return fmt.Errorf("invalid native async image response")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > nativeAsyncImagePollMaxResponseBytes {
		return fmt.Errorf("native async image response is too large")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)
	return nil
}

func resolveNativeAsyncImagePollURL(rawURL, baseURL string) (string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Scheme == "" || base.Hostname() == "" {
		return "", false
	}
	poll, err := url.Parse(rawURL)
	if err != nil || poll.IsAbs() && (poll.Scheme != "http" && poll.Scheme != "https") {
		return "", false
	}
	if !poll.IsAbs() {
		poll = base.ResolveReference(poll)
	}
	if poll.Scheme != "http" && poll.Scheme != "https" || poll.Hostname() == "" || poll.User != nil {
		return "", false
	}
	if !strings.EqualFold(poll.Hostname(), base.Hostname()) || effectivePort(poll) != effectivePort(base) {
		return "", false
	}
	return poll.String(), true
}

func nativeAsyncImageContentURL(statusURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(statusURL))
	if err != nil || parsed.Path == "" {
		return strings.TrimRight(strings.TrimSpace(statusURL), "/") + "/content"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/content"
	return parsed.String()
}

func parsedHostname(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func effectivePort(parsed *url.URL) string {
	if parsed.Port() != "" {
		return parsed.Port()
	}
	if parsed.Scheme == "https" {
		return "443"
	}
	return "80"
}
