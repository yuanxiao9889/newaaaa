package service

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const AsyncImageAssetType = "image"

func IsImageTaskAction(action string) bool {
	return action == constant.TaskActionImageGenerate ||
		action == constant.TaskActionImageEdit ||
		action == constant.TaskActionGeminiImage
}

func BuildAsyncImageStatusURL(taskID string) string {
	return joinAsyncImageURL(fmt.Sprintf("/v1/images/tasks/%s", taskID))
}

func BuildAsyncImageContentURL(taskID string) string {
	return joinAsyncImageURL(fmt.Sprintf("/v1/images/tasks/%s/content", taskID))
}

func GetAsyncImageTaskStatus(task *model.Task) string {
	if task == nil {
		return dto.ImageAsyncStatusFailed
	}
	if IsAsyncImageExpired(task) {
		return dto.ImageAsyncStatusExpired
	}
	switch task.Status {
	case model.TaskStatusSubmitted, model.TaskStatusNotStart:
		return dto.ImageAsyncStatusSubmitted
	case model.TaskStatusQueued:
		return dto.ImageAsyncStatusQueued
	case model.TaskStatusInProgress:
		return dto.ImageAsyncStatusProcessing
	case model.TaskStatusSuccess:
		return dto.ImageAsyncStatusSucceeded
	case model.TaskStatusFailure:
		return dto.ImageAsyncStatusFailed
	default:
		return dto.ImageAsyncStatusProcessing
	}
}

func GetAsyncImageExpiresAt(task *model.Task) int64 {
	if task == nil || task.PrivateData.AssetType != AsyncImageAssetType {
		return 0
	}
	return task.PrivateData.ExpiresAt
}

func IsAsyncImageExpired(task *model.Task) bool {
	if task == nil || task.Status != model.TaskStatusSuccess {
		return false
	}
	if task.PrivateData.AssetType != AsyncImageAssetType {
		return false
	}
	if task.PrivateData.ExpiresAt <= 0 {
		return false
	}
	if time.Now().Unix() >= task.PrivateData.ExpiresAt {
		return true
	}
	if task.PrivateData.LocalPath == "" {
		return true
	}
	_, err := os.Stat(task.PrivateData.LocalPath)
	return err != nil
}

func GetAsyncImageStoragePath() string {
	return filepath.Clean(common.GetEnvOrDefaultString("ASYNC_IMAGE_STORAGE_PATH", "./data/async-images"))
}

func GetAsyncImageRetention() time.Duration {
	return time.Duration(common.GetAsyncImageRetentionHours()) * time.Hour
}

func GetAsyncImageCleanupInterval() time.Duration {
	return time.Duration(common.GetEnvOrDefault("ASYNC_IMAGE_CLEANUP_INTERVAL_MINUTES", 10)) * time.Minute
}

func StoreAsyncImageResult(task *model.Task, proxy string, assetRef string) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	assetRef = strings.TrimSpace(assetRef)
	if assetRef == "" {
		return fmt.Errorf("image asset is empty")
	}

	var (
		mimeType string
		data     []byte
		err      error
	)

	if isLikelyRawBase64Image(assetRef) {
		assetRef = "data:image/*;base64," + assetRef
	}

	if strings.HasPrefix(assetRef, "http://") || strings.HasPrefix(assetRef, "https://") {
		mimeType, data, err = downloadAsyncImageBytesWithRetry(assetRef, proxy)
	} else {
		mimeType, data, err = decodeAsyncImageBytes(assetRef)
	}
	if err != nil {
		return err
	}

	if err = os.MkdirAll(GetAsyncImageStoragePath(), 0755); err != nil {
		return fmt.Errorf("create async image storage path failed: %w", err)
	}

	fileExt := imageExtensionByMime(mimeType)
	localPath := filepath.Join(GetAsyncImageStoragePath(), task.TaskID+fileExt)
	tmpPath := localPath + ".tmp"
	if err = os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write async image temp file failed: %w", err)
	}
	if err = os.Rename(tmpPath, localPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("move async image file failed: %w", err)
	}

	now := time.Now()
	task.PrivateData.AssetType = AsyncImageAssetType
	task.PrivateData.LocalPath = localPath
	task.PrivateData.MimeType = mimeType
	task.PrivateData.FileSize = int64(len(data))
	task.PrivateData.StoredAt = now.Unix()
	task.PrivateData.ExpiresAt = now.Add(GetAsyncImageRetention()).Unix()
	if strings.HasPrefix(assetRef, "http://") || strings.HasPrefix(assetRef, "https://") {
		task.PrivateData.SourceURL = assetRef
	} else {
		task.PrivateData.SourceURL = ""
	}
	task.PrivateData.ResultURL = BuildAsyncImageContentURL(task.TaskID)
	return nil
}

func CleanupExpiredAsyncImages() error {
	now := time.Now().Unix()
	const batchSize = 500

	for offset := 0; ; offset += batchSize {
		tasks := model.GetSuccessfulImageTasksForCleanup(offset, batchSize)
		if len(tasks) == 0 {
			break
		}
		for _, task := range tasks {
			if task == nil || task.PrivateData.AssetType != AsyncImageAssetType {
				continue
			}
			if task.PrivateData.ExpiresAt <= 0 || task.PrivateData.ExpiresAt > now {
				continue
			}
			if task.PrivateData.LocalPath == "" {
				continue
			}
			if err := os.Remove(task.PrivateData.LocalPath); err != nil && !os.IsNotExist(err) {
				common.SysError(fmt.Sprintf("remove expired async image %s failed: %s", task.TaskID, err.Error()))
			}
		}
		if len(tasks) < batchSize {
			break
		}
	}
	return nil
}

func StartAsyncImageCleanupLoop() {
	if err := CleanupExpiredAsyncImages(); err != nil {
		common.SysError("cleanup expired async images failed: " + err.Error())
	}

	interval := GetAsyncImageCleanupInterval()
	if interval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := CleanupExpiredAsyncImages(); err != nil {
				common.SysError("cleanup expired async images failed: " + err.Error())
			}
		}
	}()
}

func isLikelyRawBase64Image(assetRef string) bool {
	if strings.Contains(assetRef, ",") || strings.HasPrefix(assetRef, "data:") {
		return false
	}
	trimmed := strings.TrimSpace(assetRef)
	if len(trimmed) < 32 {
		return false
	}
	for _, r := range trimmed {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func joinAsyncImageURL(path string) string {
	base := strings.TrimRight(system_setting.ServerAddress, "/")
	if base == "" {
		return path
	}
	return base + path
}

func downloadAsyncImageBytesWithRetry(assetURL string, proxy string) (string, []byte, error) {
	delays := []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond, 3000 * time.Millisecond}
	var lastErr error
	for i, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		mimeType, data, err := downloadAsyncImageBytes(assetURL, proxy)
		if err == nil {
			return mimeType, data, nil
		}
		lastErr = err
		if !shouldRetryAsyncImageDownload(err) || i == len(delays)-1 {
			break
		}
	}
	return "", nil, lastErr
}

func shouldRetryAsyncImageDownload(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	noRetryMessages := []string{
		"request blocked",
		"invalid image content type",
		"svg image content is not supported",
		"image size exceeds maximum allowed size",
	}
	for _, item := range noRetryMessages {
		if strings.Contains(message, item) {
			return false
		}
	}
	return true
}

func downloadAsyncImageBytes(assetURL string, proxy string) (string, []byte, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(assetURL, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return "", nil, fmt.Errorf("request blocked: %w", err)
	}

	client, err := GetHttpClientWithProxy(proxy)
	if err != nil {
		return "", nil, err
	}
	req, err := http.NewRequest(http.MethodGet, assetURL, nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download image failed with status %d", resp.StatusCode)
	}

	maxBytes := int64(constant.MaxFileDownloadMB*1024*1024) + 1
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return "", nil, err
	}
	if int64(len(data)) >= maxBytes {
		return "", nil, fmt.Errorf("image size exceeds maximum allowed size")
	}

	mimeType := normalizeMimeType(resp.Header.Get("Content-Type"))
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = normalizeMimeType(http.DetectContentType(data))
	}
	if mimeType == "image/svg+xml" {
		return "", nil, fmt.Errorf("svg image content is not supported")
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return "", nil, fmt.Errorf("invalid image content type: %s", mimeType)
	}
	return mimeType, data, nil
}

func decodeAsyncImageBytes(assetRef string) (string, []byte, error) {
	mimeType, base64Data, err := DecodeBase64FileData(assetRef)
	if err != nil {
		return "", nil, err
	}

	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(base64Data)
		if err != nil {
			return "", nil, err
		}
	}

	mimeType = normalizeMimeType(mimeType)
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = normalizeMimeType(http.DetectContentType(data))
	}
	if mimeType == "image/svg+xml" {
		return "", nil, fmt.Errorf("svg image content is not supported")
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return "", nil, fmt.Errorf("invalid image content type: %s", mimeType)
	}
	return mimeType, data, nil
}

func normalizeMimeType(mimeType string) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		return ""
	}
	parsedType, _, err := mime.ParseMediaType(mimeType)
	if err == nil {
		mimeType = parsedType
	}
	return strings.ToLower(mimeType)
}

func imageExtensionByMime(mimeType string) string {
	switch normalizeMimeType(mimeType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	default:
		return ".img"
	}
}
