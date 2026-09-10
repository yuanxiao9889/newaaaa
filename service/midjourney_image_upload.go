package service

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	midjourneyUploadMaxImages              = 4
	midjourneyUploadMaxBytes               = 8 * 1024 * 1024
	defaultMidjourneyUploadRetention       = 24 * time.Hour
	defaultMidjourneyUploadCleanupInterval = time.Hour
)

var ErrInvalidMidjourneyUpload = errors.New("invalid Midjourney image upload")

type MidjourneyUploadResult struct{ URLs []string }

func GetMidjourneyUploadStoragePath() string {
	return filepath.Clean(common.GetEnvOrDefaultString("MIDJOURNEY_UPLOAD_STORAGE_PATH", "/data/midjourney-uploads"))
}

func GetMidjourneyUploadRetention() time.Duration {
	hours := common.GetEnvOrDefault("MIDJOURNEY_UPLOAD_RETENTION_HOURS", 24)
	if hours <= 0 {
		return defaultMidjourneyUploadRetention
	}
	return time.Duration(hours) * time.Hour
}

func GetMidjourneyUploadCleanupInterval() time.Duration {
	hours := common.GetEnvOrDefault("MIDJOURNEY_UPLOAD_CLEANUP_INTERVAL_HOURS", 1)
	if hours <= 0 {
		return defaultMidjourneyUploadCleanupInterval
	}
	return time.Duration(hours) * time.Hour
}

func StoreMidjourneyImages(base64Array []string) (MidjourneyUploadResult, error) {
	if len(base64Array) == 0 || len(base64Array) > midjourneyUploadMaxImages {
		return MidjourneyUploadResult{}, fmt.Errorf("%w: base64Array must contain 1 to %d images", ErrInvalidMidjourneyUpload, midjourneyUploadMaxImages)
	}
	type imageFile struct {
		data []byte
		ext  string
	}
	files := make([]imageFile, 0, len(base64Array))
	for _, value := range base64Array {
		data, ext, err := decodeMidjourneyUpload(value)
		if err != nil {
			return MidjourneyUploadResult{}, err
		}
		files = append(files, imageFile{data: data, ext: ext})
	}
	storagePath := GetMidjourneyUploadStoragePath()
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		return MidjourneyUploadResult{}, fmt.Errorf("create Midjourney upload directory: %w", err)
	}
	created := make([]string, 0, len(files))
	cleanup := func() {
		for _, name := range created {
			_ = os.Remove(filepath.Join(storagePath, name))
		}
	}
	for _, file := range files {
		name, err := randomMidjourneyUploadName(file.ext)
		if err != nil {
			cleanup()
			return MidjourneyUploadResult{}, err
		}
		path := filepath.Join(storagePath, name)
		tmp, err := os.CreateTemp(storagePath, ".midjourney-upload-*")
		if err != nil {
			cleanup()
			return MidjourneyUploadResult{}, fmt.Errorf("create Midjourney upload temp file: %w", err)
		}
		tmpPath := tmp.Name()
		_, writeErr := tmp.Write(file.data)
		if writeErr == nil {
			writeErr = tmp.Chmod(0600)
		}
		if closeErr := tmp.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = os.Remove(tmpPath)
			cleanup()
			return MidjourneyUploadResult{}, fmt.Errorf("write Midjourney upload: %w", writeErr)
		}
		if err = os.Rename(tmpPath, path); err != nil {
			_ = os.Remove(tmpPath)
			cleanup()
			return MidjourneyUploadResult{}, fmt.Errorf("store Midjourney upload: %w", err)
		}
		created = append(created, name)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	result := MidjourneyUploadResult{URLs: make([]string, 0, len(created))}
	for _, name := range created {
		path := "/mj/upload/" + name
		if baseURL == "" {
			result.URLs = append(result.URLs, path)
		} else {
			result.URLs = append(result.URLs, baseURL+path)
		}
	}
	return result, nil
}

func decodeMidjourneyUpload(value string) ([]byte, string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "data:") {
		return nil, "", fmt.Errorf("%w: image must be a data URL", ErrInvalidMidjourneyUpload)
	}
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || encoded == "" || !strings.HasSuffix(header, ";base64") {
		return nil, "", fmt.Errorf("%w: invalid data URL", ErrInvalidMidjourneyUpload)
	}
	maxEncodedBytes := ((midjourneyUploadMaxBytes + 2) / 3) * 4
	if len(encoded) > maxEncodedBytes {
		return nil, "", fmt.Errorf("%w: encoded image is too large", ErrInvalidMidjourneyUpload)
	}
	mimeType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	var ext string
	switch mimeType {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	default:
		return nil, "", fmt.Errorf("%w: unsupported image MIME type", ErrInvalidMidjourneyUpload)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(data) == 0 {
		return nil, "", fmt.Errorf("%w: invalid base64 image data", ErrInvalidMidjourneyUpload)
	}
	if len(data) > midjourneyUploadMaxBytes {
		return nil, "", fmt.Errorf("%w: image exceeds %d bytes", ErrInvalidMidjourneyUpload, midjourneyUploadMaxBytes)
	}
	_, format, err := decodeImageConfig(data)
	if err != nil {
		return nil, "", fmt.Errorf("%w: image content cannot be decoded", ErrInvalidMidjourneyUpload)
	}
	format = strings.ToLower(format)
	if (mimeType == "image/jpeg" && format != "jpeg") || (mimeType == "image/png" && format != "png") || (mimeType == "image/webp" && format != "webp") {
		return nil, "", fmt.Errorf("%w: MIME type does not match image content", ErrInvalidMidjourneyUpload)
	}
	return data, ext, nil
}

func randomMidjourneyUploadName(ext string) (string, error) {
	var id [16]byte
	if _, err := io.ReadFull(rand.Reader, id[:]); err != nil {
		return "", fmt.Errorf("generate Midjourney upload id: %w", err)
	}
	return hex.EncodeToString(id[:]) + ext, nil
}

func ServeMidjourneyUpload(w http.ResponseWriter, r *http.Request, name string) error {
	if !isMidjourneyUploadName(name) {
		return os.ErrNotExist
	}
	path := filepath.Join(GetMidjourneyUploadStoragePath(), filepath.Base(name))
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = os.ErrNotExist
		}
		return err
	}
	if info.ModTime().Before(time.Now().Add(-GetMidjourneyUploadRetention())) {
		return os.ErrNotExist
	}
	mimeType := map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}[filepath.Ext(name)]
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filepath.Base(name)))
	http.ServeContent(w, r, filepath.Base(name), info.ModTime(), file)
	return nil
}

func isMidjourneyUploadName(name string) bool {
	if name == "" || filepath.Base(name) != name {
		return false
	}
	ext := filepath.Ext(name)
	if ext != ".jpg" && ext != ".png" && ext != ".webp" {
		return false
	}
	id := strings.TrimSuffix(name, ext)
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func CleanupExpiredMidjourneyUploads() error {
	entries, err := os.ReadDir(GetMidjourneyUploadStoragePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-GetMidjourneyUploadRetention())
	for _, entry := range entries {
		if entry.IsDir() || !isMidjourneyUploadName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(GetMidjourneyUploadStoragePath(), entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func StartMidjourneyUploadCleanupLoop() {
	interval := GetMidjourneyUploadCleanupInterval()
	go func() {
		if err := CleanupExpiredMidjourneyUploads(); err != nil {
			common.SysError("cleanup expired Midjourney uploads failed: " + err.Error())
		}
		if interval <= 0 {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := CleanupExpiredMidjourneyUploads(); err != nil {
				common.SysError("cleanup expired Midjourney uploads failed: " + err.Error())
			}
		}
	}()
}
