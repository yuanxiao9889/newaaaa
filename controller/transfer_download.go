package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type transferDownloadRequest struct {
	Qiniu       bool   `json:"qiniu,omitempty"`
	TaskID      string `json:"task_id"`
	RequestID   string `json:"request_id"`
	ArtifactKey string `json:"artifact_key"`
	ResultIndex *int   `json:"result_index"`
	SourceURL   string `json:"source_url"`
}

type transferDownloadSource struct {
	resource      string
	bridge        string
	direct        string
	headers       map[string]string
	version       string
	mediaKind     string
	route         string
	redirectHosts []string
	expiresAt     int64
}

func CreateTransferDownloadTicket(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	var request transferDownloadRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 12<<10)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil ||
		(request.ResultIndex != nil && (*request.ResultIndex < 0 || *request.ResultIndex > 63)) || len(request.TaskID) > 191 ||
		len(request.SourceURL) > 4096 || len(request.ArtifactKey) > 128 {
		c.JSON(400, gin.H{"error": gin.H{"code": "transfer_invalid_resource"}})
		return
	}
	policy, err := model.LoadTransferPolicy()
	if err != nil || !bridgeTicketsConfigured() {
		c.JSON(503, gin.H{"error": gin.H{"code": "transfer_download_unavailable"}})
		return
	}
	if !policy.Policy.Enabled {
		c.JSON(409, gin.H{"error": gin.H{"code": "transfer_bridge_paused"}})
		return
	}
	source, err := resolveTransferDownload(c, request)
	if err != nil {
		// Same answer for another account, missing task and mismatched URL.
		c.JSON(404, gin.H{"error": gin.H{"code": "transfer_resource_not_available"}})
		return
	}
	if request.Qiniu {
		createQiniuDownload(c, source)
		return
	}
	parsed, err := url.Parse(source.bridge)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" ||
		strings.EqualFold(parsed.Hostname(), "bridge.opics.top") ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		(parsed.Port() != "" && parsed.Port() != "443" && parsed.Port() != "80") {
		c.JSON(409, gin.H{"error": gin.H{"code": "transfer_resource_not_supported"}})
		return
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", c.GetInt("id"), source.resource)))
	resourceID := hex.EncodeToString(hash[:])
	now := time.Now().Unix()
	owner, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil || owner.Status != common.UserStatusEnabled {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	tokenDigest := sha256.Sum256([]byte(c.GetString("token_key")))
	expires := now + 300
	if source.expiresAt > 0 && source.expiresAt < expires {
		expires = source.expiresAt
	}
	ticket, err := service.SealTransferDownload(service.TransferDownloadClaim{
		AuthVersion: owner.AuthVersion, TokenID: c.GetInt("token_id"), TokenDigest: hex.EncodeToString(tokenDigest[:]),
		Version: 1, UserID: c.GetInt("id"), ResourceID: resourceID,
		URL: source.bridge, Headers: source.headers, IssuedAt: now, ExpiresAt: expires,
		ResourceVersion: source.version, MediaKind: source.mediaKind, Route: source.route,
		RedirectHosts: source.redirectHosts,
	})
	if err != nil {
		c.JSON(503, gin.H{"error": gin.H{"code": "transfer_download_unavailable"}})
		return
	}
	c.JSON(200, gin.H{"protocol_version": 1, "resource_id": resourceID,
		"bridge_url": model.TransferBridge + "/v1/transfer/download", "origin_url": source.direct,
		"expires_at": expires, "origin_headers": gin.H{}, "media_kind": source.mediaKind,
		"qiniu_fallback": service.QiniuTransferOrigin(false) != "",
		"bridge_route":   source.route, "resource_version": source.version,
		"bridge_headers": gin.H{"X-Opics-Download-Ticket": ticket}})
}

func resolveTransferDownload(c *gin.Context, request transferDownloadRequest) (*transferDownloadSource, error) {
	userID := c.GetInt("id")
	missing := errors.New("resource not available")
	if request.TaskID != "" {
		task, exists, err := model.GetByTaskId(userID, request.TaskID)
		if err != nil {
			return nil, err
		}
		if exists && task != nil {
			if task.Status != model.TaskStatusSuccess || !task.ResultRetrievable() {
				return nil, missing
			}
			artifacts, err := projectTaskArtifacts(task)
			if err != nil {
				return nil, err
			}
			index := 0
			if request.ResultIndex != nil {
				index = *request.ResultIndex
			}
			key, err := transferTaskArtifactKey(task, artifacts, request)
			if err != nil {
				return nil, err
			}
			if len(artifacts) == 0 {
				if !legacyVideoAvailable(task) || index != 0 || (key != "" && key != "video") {
					return nil, missing
				}
				key = "video"
			}
			if key == "" || !taskArtifactKeyPattern.MatchString(key) ||
				len(artifacts) > 0 && !slices.ContainsFunc(artifacts, func(a relaychannel.TaskArtifact) bool { return a.Key == key }) {
				return nil, missing
			}
			access, err := service.IssueTaskArtifactAccess(task.TaskID, key)
			if err != nil {
				return nil, err
			}
			contentURL := model.TransferOrigin + "/v1/tasks/" + url.PathEscape(task.TaskID) +
				"/artifacts/" + url.PathEscape(key) + "/content?access=" + url.QueryEscape(access)
			result := &transferDownloadSource{resource: "task:" + task.TaskID + ":" + key,
				bridge: contentURL, direct: contentURL, route: "relay", mediaKind: "video"}
			for _, artifact := range artifacts {
				if artifact.Key == key {
					result.mediaKind = artifact.Type
				}
			}
			if result.mediaKind != "image" && result.mediaKind != "video" {
				result.mediaKind = "other"
			}
			if task.Platform == constant.TaskPlatformInternalImage {
				if service.IsAsyncImageExpired(task) {
					return nil, missing
				}
				result.expiresAt = service.GetAsyncImageExpiresAt(task)
				assets := service.GetAsyncImageAssets(task)
				for i, artifact := range artifacts {
					if artifact.Key != key || i >= len(assets) {
						continue
					}
					file, err := os.Open(assets[i].LocalPath)
					if err != nil {
						return nil, missing
					}
					result.version, err = service.TransferFileVersion(file)
					file.Close()
					if err != nil {
						return nil, missing
					}
					result.version = "sha256:" + result.version
				}
				return result, nil
			}
			if !taskHasPluginExecution(task) {
				if raw := task.GetResultURL(); raw != "" && !isTaskMediaFallbackLoop(raw, task.TaskID) {
					if request.SourceURL != "" && request.SourceURL != raw && !isOwnedTransferContentURL(task.TaskID, request.SourceURL) {
						return nil, missing
					}
					result.bridge, result.direct = raw, raw
					result.route = "upstream"
				}
				return result, nil
			}
			adaptor, err := initTaskArtifactAdaptor(task)
			if err != nil {
				return nil, err
			}
			provider, ok := adaptor.(relaychannel.TaskContentRequestProvider)
			if !ok {
				return result, nil
			}
			descriptor, err := provider.BuildContentRequest(task, key, relaychannel.TaskArtifactClientRequest{Method: "GET"})
			if err != nil {
				return nil, err
			}
			if descriptor != nil && (descriptor.Method == "" || descriptor.Method == "GET") &&
				len(descriptor.Body) == 0 && strings.HasPrefix(descriptor.URL, "https://") {
				if !transferSourceMatchesTask(task, artifacts, key, descriptor.URL, request.SourceURL) {
					return nil, missing
				}
				headers := make(map[string]string)
				for name, value := range descriptor.Headers {
					if len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
						return nil, missing
					}
					switch http.CanonicalHeaderKey(name) {
					case "Authorization", "X-Api-Key", "X-Goog-Api-Key", "Referer", "Accept":
						headers[http.CanonicalHeaderKey(name)] = value
					default:
						// A descriptor with other requirements uses the existing
						// authenticated origin content proxy, never partial headers.
						return result, nil
					}
				}
				result.bridge, result.headers, result.route = descriptor.URL, headers, "upstream"
				// The plugin's administrator-approved host list is the only source
				// of cross-origin credential-stripping redirect authority. Never
				// derive that authority from a caller URL or returned Location.
				if task.PrivateData.Execution != nil && task.PrivateData.Execution.TaskPlugin != nil {
					if plugin, ok := pluginruntime.DefaultRegistry.Get(task.PrivateData.Execution.TaskPlugin.Key); ok {
						for _, host := range plugin.Meta.AllowedHosts {
							u, err := url.Parse("https://" + host)
							if err == nil && u.User == nil && u.Host != "" && u.Path == "" &&
								(u.Port() == "" || u.Port() == "443") && !strings.ContainsAny(host, "*?#\\") &&
								net.ParseIP(u.Hostname()) == nil && !strings.HasSuffix(u.Hostname(), ".") {
								result.redirectHosts = append(result.redirectHosts, strings.ToLower(u.Hostname()))
								if len(result.redirectHosts) == 16 {
									break
								}
							}
						}
					}
				}
				if descriptor.Credentialless && len(result.headers) == 0 {
					result.direct = descriptor.URL
				}
			}
			return result, nil
		}
		var tasks []model.Midjourney
		err = model.DB.Where("user_id = ? AND mj_id = ? AND status = ?", userID, request.TaskID, "SUCCESS").
			Limit(2).Find(&tasks).Error
		if err != nil || len(tasks) != 1 {
			return nil, missing
		}
		sources := []string{}
		if tasks[0].ImageUrl != "" {
			sources = append(sources, tasks[0].ImageUrl)
			// Relay task responses may replace the upstream image URL with the
			// fixed relay image endpoint. The owned task authorizes that exact
			// mapping, never a caller-supplied path or query string.
			proxyURL := model.TransferOrigin + "/mj/image/" + url.PathEscape(tasks[0].MjId)
			requested, parseErr := url.Parse(request.SourceURL)
			if request.SourceURL == proxyURL || (parseErr == nil && requested != nil &&
				requested.Scheme+"://"+requested.Host+requested.EscapedPath() == proxyURL &&
				requested.User == nil && requested.Fragment == "") {
				return &transferDownloadSource{resource: "mj:" + request.TaskID + ":0",
					bridge: tasks[0].ImageUrl, direct: service.BuildMidjourneyImageURL(&tasks[0]), route: "upstream", mediaKind: "image"}, nil
			}
		}
		if tasks[0].VideoUrl != "" {
			sources = append(sources, tasks[0].VideoUrl)
		}
		var videos []string
		_ = common.UnmarshalJsonStr(tasks[0].VideoUrls, &videos)
		sources = append(sources, videos...)
		// Only output fields of persisted upstream task properties are eligible.
		// Never recursively collect arbitrary URLs (which may be input assets).
		var properties struct {
			ImageURLs      []string `json:"imageUrls"`
			ImageURLsSnake []string `json:"image_urls"`
		}
		_ = common.UnmarshalJsonStr(tasks[0].Properties, &properties)
		sources = append(sources, properties.ImageURLs...)
		sources = append(sources, properties.ImageURLsSnake...)
		return selectTransferSource("mj:"+request.TaskID, sources, request)
	}
	if model.ValidTransferRequestKey(request.RequestID) {
		record, err := model.FindTransferRequest(userID, request.RequestID)
		if err != nil || record == nil || record.State != "succeeded" ||
			record.FinishedAt < time.Now().Add(-model.TransferResponseRetention).Unix() {
			return nil, missing
		}
		sources, err := transferReceiptURLs(record.ResponseBody)
		if err != nil {
			return nil, missing
		}
		return selectTransferSource("request:"+request.RequestID, sources, request)
	}
	return nil, missing
}

func selectTransferSource(identity string, sources []string, request transferDownloadRequest) (*transferDownloadSource, error) {
	index := 0
	if request.ResultIndex != nil {
		index = *request.ResultIndex
	}
	if request.SourceURL != "" {
		index = slices.Index(sources, request.SourceURL)
	}
	if index < 0 || index >= len(sources) || sources[index] == "" {
		return nil, errors.New("resource not available")
	}
	return &transferDownloadSource{resource: fmt.Sprintf("%s:%d", identity, index), bridge: sources[index],
		direct: sources[index], route: "upstream", mediaKind: "other"}, nil
}
