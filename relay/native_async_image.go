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
	"github.com/QuantumNous/new-api/model"
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
	userID     int
	modelName  string
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

func nativeAsyncImageErrorDetails(public common.PublicError, local bool) gin.H {
	errorType := "upstream_error"
	if local {
		errorType = "invalid_request_error"
	}
	return gin.H{
		"code": public.Code, "message": public.Summary(), "type": errorType,
		"hint": public.Hint, "retryable": public.Retryable,
	}
}

func writeNativeAsyncImageError(c *gin.Context, status int, code, message string, local bool) {
	public := common.ClassifyPublicError(code, message, status, local)
	if !local && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		status = public.StatusCode
	}
	c.JSON(status, gin.H{"error": nativeAsyncImageErrorDetails(public, local)})
}

func RegisterNativeAsyncImagePoll(taskID, pollURL, contentURL string, info *relaycommon.RelayInfo) {
	if strings.TrimSpace(taskID) == "" || info == nil || info.ChannelMeta == nil || info.UserId <= 0 {
		return
	}
	resolved, ok := resolveNativeAsyncImagePollURL(pollURL, info.ChannelBaseUrl)
	if !ok {
		return
	}
	entry := nativeAsyncImagePollEntry{
		userID:     info.UserId,
		modelName:  info.OriginModelName,
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
		writeNativeAsyncImageError(c, http.StatusNotFound, "task_not_found", "", true)
		return
	}
	entry, ok := value.(nativeAsyncImagePollEntry)
	if !ok || entry.userID <= 0 || entry.userID != c.GetInt("id") {
		writeNativeAsyncImageError(c, http.StatusNotFound, "task_not_found", "", true)
		return
	}
	if time.Now().After(entry.expiresAt) {
		nativeAsyncImagePolls.Delete(taskID)
		writeNativeAsyncImageError(c, http.StatusNotFound, "task_expired", "", true)
		return
	}
	contentURL := entry.contentURL
	if contentURL == "" {
		contentURL = nativeAsyncImageContentURL(entry.statusURL)
	}
	client, err := service.GetHttpClientWithProxySettings(entry.settings.Proxy, entry.settings)
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, contentURL, nil)
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	req.Header.Set("Accept", "image/*, application/octet-stream")
	applyNativeAsyncImageAuth(req, entry, contentURL)
	resp, err := client.Do(req)
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	if len(body) > nativeAsyncImagePollMaxResponseBytes {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "invalid_response", "", false)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeNativeAsyncImageError(c, resp.StatusCode, "", string(body), false)
		return
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") && contentType != "application/octet-stream" {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "", string(body), false)
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Data(resp.StatusCode, contentType, body)
}

func GetNativeAsyncImageStatus(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	value, ok := nativeAsyncImagePolls.Load(taskID)
	if !ok {
		writeNativeAsyncImageError(c, http.StatusNotFound, "task_not_found", "", true)
		return
	}
	entry, ok := value.(nativeAsyncImagePollEntry)
	if !ok || entry.userID <= 0 || entry.userID != c.GetInt("id") {
		writeNativeAsyncImageError(c, http.StatusNotFound, "task_not_found", "", true)
		return
	}
	if time.Now().After(entry.expiresAt) {
		nativeAsyncImagePolls.Delete(taskID)
		writeNativeAsyncImageError(c, http.StatusNotFound, "task_expired", "", true)
		return
	}
	client, err := service.GetHttpClientWithProxySettings(entry.settings.Proxy, entry.settings)
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, entry.statusURL, nil)
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	req.Header.Set("Accept", "application/json")
	applyNativeAsyncImageAuth(req, entry, entry.statusURL)
	resp, err := client.Do(req)
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeAsyncImagePollMaxResponseBytes+1))
	if err != nil {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "service_unavailable", err.Error(), false)
		return
	}
	if len(body) > nativeAsyncImagePollMaxResponseBytes {
		writeNativeAsyncImageError(c, http.StatusBadGateway, "invalid_response", "", false)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeNativeAsyncImageError(c, resp.StatusCode, "", string(body), false)
		return
	}
	body = rewriteNativeAsyncImageTaskEnvelope(body, taskID)
	c.Data(resp.StatusCode, "application/json", body)
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
		publicID := model.GenerateTaskID()
		RegisterNativeAsyncImagePoll(publicID, payload.PollURL, payload.ContentURL, info)
		body = rewriteNativeAsyncImageTaskEnvelope(body, publicID)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return true
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return false
}

func rewriteNativeAsyncImageTaskEnvelope(body []byte, taskID string) []byte {
	taskID = strings.TrimSpace(taskID)
	var envelope map[string]json.RawMessage
	if taskID == "" || common.Unmarshal(body, &envelope) != nil {
		result, _ := common.Marshal(gin.H{"error": nativeAsyncImageErrorDetails(common.ClassifyPublicError("invalid_response", "", http.StatusBadGateway, false), false)})
		return result
	}
	var status string
	_ = common.Unmarshal(envelope["status"], &status)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "submitted", "queued":
		status = "queued"
	case "processing", "running", "in_progress":
		status = "in_progress"
	case "succeeded", "success", "completed":
		status = "succeeded"
	default:
		status = "failed"
	}
	public := map[string]any{"id": taskID, "status": status}
	if value, ok := nativeAsyncImagePolls.Load(taskID); ok {
		if entry, ok := value.(nativeAsyncImagePollEntry); ok {
			public["model"] = entry.modelName
		}
	}
	if status == "failed" {
		public["error"] = nativeAsyncImageErrorDetails(common.ClassifyPublicError("", string(body), 0, false), false)
	}
	pollPath := "/v1/images/generations/" + url.PathEscape(taskID)
	contentPath := pollPath + "/content"
	public["poll_url"] = pollPath
	public["content_url"] = contentPath
	rewritten, err := common.Marshal(public)
	if err != nil {
		result, _ := common.Marshal(gin.H{"error": nativeAsyncImageErrorDetails(common.ClassifyPublicError("invalid_response", "", http.StatusBadGateway, false), false)})
		return result
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
