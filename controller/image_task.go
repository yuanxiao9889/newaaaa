package controller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

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

func imageTaskError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
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
		resp.URL, resp.URLExpiresAt = service.BuildSignedAsyncImageContentURL(task)
	}
	if resp.Status == dto.ImageAsyncStatusFailed {
		resp.Error = task.FailReason
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
		c.JSON(http.StatusOK, resp)
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

	c.JSON(http.StatusOK, resp)
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
	if !service.VerifyAsyncImageToken(c.Query("token"), task) {
		imageTaskError(c, http.StatusUnauthorized, "invalid image token")
		return
	}

	serveImageTaskContent(c, task)
}

func serveImageTaskContent(c *gin.Context, task *model.Task) {
	status := service.GetAsyncImageTaskStatus(task)
	switch status {
	case dto.ImageAsyncStatusExpired:
		imageTaskError(c, http.StatusGone, "image content has expired")
		return
	case dto.ImageAsyncStatusSucceeded:
	default:
		imageTaskError(c, http.StatusBadRequest, fmt.Sprintf("task is not completed yet, current status: %s", status))
		return
	}

	if task.PrivateData.LocalPath == "" {
		imageTaskError(c, http.StatusGone, "image content is unavailable")
		return
	}

	file, err := os.Open(task.PrivateData.LocalPath)
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

	mimeType := task.PrivateData.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	c.Header("Content-Type", mimeType)
	c.Header("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s%s\"", task.TaskID, filepath.Ext(task.PrivateData.LocalPath)))
	http.ServeContent(c.Writer, c.Request, filepath.Base(task.PrivateData.LocalPath), fileInfo.ModTime(), file)
}
