package controller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const imageTaskStatusCacheTTL = 2 * time.Second

type imageTaskStatusCacheEntry struct {
	response dto.ImageAsyncTaskResponse
	expires  time.Time
}

var imageTaskStatusCache = struct {
	sync.RWMutex
	items map[string]imageTaskStatusCacheEntry
}{
	items: make(map[string]imageTaskStatusCacheEntry),
}

// Retry metadata is computed on each poll, separately from the status cache,
// so an expired or unavailable input is never advertised from a cached response.
func writeImageTaskStatus(c *gin.Context, userID int, resp dto.ImageAsyncTaskResponse) {
	var retry any
	if input, _, err := service.ReadImageTaskInput(userID, resp.TaskID, false); err == nil {
		retry = gin.H{"expires_at": input.ExpiresAt, "request_path": input.Path,
			"input_url": service.BuildAsyncImageStatusURL(resp.TaskID) + "/retry-input",
			"header":    service.ImageRetryTaskHeader}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, struct {
		dto.ImageAsyncTaskResponse
		RetryInput any `json:"retry_input,omitempty"`
	}{ImageAsyncTaskResponse: resp, RetryInput: retry})
}

func imageTaskError(c *gin.Context, status int, message string) {
	code := ""
	switch status {
	case http.StatusNotFound:
		code = "task_not_found"
	case http.StatusGone:
		code = "task_expired"
	case http.StatusUnauthorized:
		code = "invalid_token"
	case http.StatusBadRequest:
		code = "task_not_ready"
	}
	publicError := common.ClassifyPublicError(code, message, status, true)
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": common.MessageWithRequestId(publicError.Summary(), c.GetString(common.RequestIdKey)),
			"code":    publicError.Code,
			"type":    "invalid_request_error",
		},
	})
}

func imageTaskStatusCacheKey(userID int, taskID string) string {
	return fmt.Sprintf("%d:%s", userID, taskID)
}

func getCachedImageTaskStatus(userID int, taskID string) (dto.ImageAsyncTaskResponse, bool) {
	key := imageTaskStatusCacheKey(userID, taskID)
	now := time.Now()
	imageTaskStatusCache.RLock()
	entry, ok := imageTaskStatusCache.items[key]
	imageTaskStatusCache.RUnlock()
	if !ok || now.After(entry.expires) {
		if ok {
			imageTaskStatusCache.Lock()
			delete(imageTaskStatusCache.items, key)
			imageTaskStatusCache.Unlock()
		}
		return dto.ImageAsyncTaskResponse{}, false
	}
	return entry.response, true
}

func setCachedImageTaskStatus(userID int, taskID string, resp dto.ImageAsyncTaskResponse) {
	now := time.Now()
	expires := now.Add(imageTaskStatusCacheTTL)
	if resp.Status == dto.ImageAsyncStatusSucceeded && resp.ExpiresAt > 0 {
		contentExpires := time.Unix(resp.ExpiresAt, 0)
		if contentExpires.Before(expires) {
			expires = contentExpires
		}
	}
	if !expires.After(now) {
		return
	}
	imageTaskStatusCache.Lock()
	if len(imageTaskStatusCache.items) > 10000 {
		for key, entry := range imageTaskStatusCache.items {
			if now.After(entry.expires) {
				delete(imageTaskStatusCache.items, key)
			}
		}
	}
	imageTaskStatusCache.items[imageTaskStatusCacheKey(userID, taskID)] = imageTaskStatusCacheEntry{
		response: resp,
		expires:  expires,
	}
	imageTaskStatusCache.Unlock()
}

func buildImageTaskResponse(task *model.Task) dto.ImageAsyncTaskResponse {
	resp := dto.ImageAsyncTaskResponse{
		TaskID:     task.TaskID,
		Status:     service.GetAsyncImageTaskStatus(task),
		StatusURL:  service.BuildAsyncImageStatusURL(task.TaskID),
		ContentURL: service.BuildAsyncImageContentURL(task.TaskID),
		ExpiresAt:  service.GetAsyncImageExpiresAt(task),
	}
	if resp.Status == dto.ImageAsyncStatusSucceeded {
		for i, asset := range service.GetAsyncImageAssets(task) {
			imageURL, expiresAt := service.BuildSignedAsyncImageContentURLForIndex(task, i)
			resp.Data = append(resp.Data, dto.ImageData{Url: imageURL, RevisedPrompt: asset.RevisedPrompt})
			if i == 0 {
				resp.URL, resp.URLExpiresAt = imageURL, expiresAt
			}
		}
	}
	if resp.Status == dto.ImageAsyncStatusFailed {
		publicError := task.UserError()
		resp.Error = publicError.Summary()
		resp.ErrorCode = publicError.Code
	}
	if resp.Status != dto.ImageAsyncStatusSucceeded && resp.Status != dto.ImageAsyncStatusExpired {
		resp.ExpiresAt = 0
	}
	return resp
}

func GetImageTask(c *gin.Context) {
	taskID := c.Param("task_id")
	userID := c.GetInt("id")

	if resp, ok := getCachedImageTaskStatus(userID, taskID); ok {
		writeImageTaskStatus(c, userID, resp)
		return
	}

	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		imageTaskError(c, http.StatusInternalServerError, "failed to query task")
		return
	}
	if !exists || task == nil || !service.IsImageTaskAction(task.Action) {
		imageTaskError(c, http.StatusNotFound, "task not found")
		return
	}

	resp := buildImageTaskResponse(task)
	setCachedImageTaskStatus(userID, taskID, resp)

	writeImageTaskStatus(c, userID, resp)
}

// GetImageTaskRetryInput lets the owner recover and edit the original request,
// for example reducing reference images after an upstream count-limit error.
func GetImageTaskRetryInput(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	userID := c.GetInt("id")
	task, exists, err := model.GetByTaskId(userID, c.Param("task_id"))
	if err != nil {
		imageTaskError(c, http.StatusInternalServerError, "failed to query task")
		return
	}
	if userID <= 0 || !exists || task == nil || !service.IsImageTaskAction(task.Action) {
		imageTaskError(c, http.StatusNotFound, "task not found")
		return
	}
	input, body, err := service.ReadImageTaskInput(userID, task.TaskID, true)
	if err != nil {
		imageTaskError(c, http.StatusGone, "image retry input is unavailable or expired")
		return
	}
	c.Header("X-New-Api-Retry-Expires-At", strconv.FormatInt(input.ExpiresAt, 10))
	c.Header("Content-Disposition", `attachment; filename="image-request.body"`)
	c.Data(http.StatusOK, input.ContentType, body)
}

func GetImageTaskContent(c *gin.Context) {
	taskID := c.Param("task_id")
	userID := c.GetInt("id")

	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		imageTaskError(c, http.StatusInternalServerError, "failed to query task")
		return
	}
	if !exists || task == nil || !service.IsImageTaskAction(task.Action) {
		imageTaskError(c, http.StatusNotFound, "task not found")
		return
	}

	serveImageTaskContent(c, task)
}

func GetSignedImageTaskContent(c *gin.Context) {
	taskID := c.Param("task_id")
	task, exists, err := model.GetByOnlyTaskId(taskID)
	if err != nil {
		imageTaskError(c, http.StatusInternalServerError, "failed to query task")
		return
	}
	if !exists || task == nil || !service.IsImageTaskAction(task.Action) {
		imageTaskError(c, http.StatusNotFound, "task not found")
		return
	}
	index, indexErr := strconv.Atoi(c.DefaultQuery("index", "0"))
	if indexErr != nil || !service.VerifyAsyncImageTokenForIndex(c.Query("token"), task, index) {
		imageTaskError(c, http.StatusUnauthorized, "invalid image token")
		return
	}

	serveImageTaskContent(c, task)
}

func serveImageTaskContent(c *gin.Context, task *model.Task) {
	index, err := strconv.Atoi(c.DefaultQuery("index", "0"))
	if err != nil {
		imageTaskError(c, http.StatusNotFound, "image content is unavailable")
		return
	}
	serveImageTaskContentAtIndex(c, task, index)
}

func serveImageTaskContentAtIndex(c *gin.Context, task *model.Task, index int) {
	status := service.GetAsyncImageTaskStatus(task)
	switch status {
	case dto.ImageAsyncStatusFailed:
		publicError := task.UserError()
		c.JSON(publicError.StatusCode, gin.H{"error": gin.H{
			"code": publicError.Code, "message": publicError.Summary(), "type": "task_error",
		}, "task_id": task.TaskID})
		return
	case dto.ImageAsyncStatusExpired:
		imageTaskError(c, http.StatusGone, "image content has expired")
		return
	case dto.ImageAsyncStatusSucceeded:
	default:
		imageTaskError(c, http.StatusBadRequest, fmt.Sprintf("task is not completed yet, current status: %s", status))
		return
	}

	assets := service.GetAsyncImageAssets(task)
	if index < 0 || index >= len(assets) {
		imageTaskError(c, http.StatusNotFound, "image content is unavailable")
		return
	}
	asset := assets[index]

	file, err := os.Open(asset.LocalPath)
	if err != nil {
		if os.IsNotExist(err) {
			imageTaskError(c, http.StatusGone, "image content has expired")
			return
		}
		imageTaskError(c, http.StatusInternalServerError, "failed to open image content")
		return
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		imageTaskError(c, http.StatusInternalServerError, "failed to stat image content")
		return
	}

	mimeType := asset.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	version, err := service.TransferFileVersion(file)
	if err != nil {
		imageTaskError(c, http.StatusInternalServerError, "failed to validate image content")
		return
	}

	c.Header("Content-Type", mimeType)
	c.Header("ETag", `"`+version+`"`)
	c.Header("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	if c.Writer.Header().Get("Cache-Control") == "" {
		c.Header("Cache-Control", "private, max-age=3600")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s%s\"", task.TaskID, filepath.Ext(asset.LocalPath)))
	http.ServeContent(c.Writer, c.Request, filepath.Base(asset.LocalPath), fileInfo.ModTime(), file)
}
