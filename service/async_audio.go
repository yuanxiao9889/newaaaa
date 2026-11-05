package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type AudioTaskResult struct {
	Audio            string  `json:"audio,omitempty"`
	URL              string  `json:"url"`
	Duration         float64 `json:"duration"`
	OriginalDuration float64 `json:"original_duration"`
	Subtitle         any     `json:"subtitle,omitempty"`
}

func AudioTaskStoragePath() string {
	return filepath.Join(GetAsyncImageStoragePath(), "audio")
}

func AudioTaskContentURL(taskID string) string {
	return "/v1/audio/tasks/" + taskID + "/content"
}

// StoreAsyncAudioResult verifies the returned bytes and publishes them before
// success/settlement. It keeps compact metadata in the database, not Base64.
func StoreAsyncAudioResult(ctx context.Context, task *model.Task) error {
	var result AudioTaskResult
	if err := common.Unmarshal(task.Data, &result); err != nil {
		return err
	}
	if len(result.Audio) > 1<<20 {
		return fmt.Errorf("audio response exceeds 1 MiB")
	}
	data, err := base64.StdEncoding.DecodeString(result.Audio)
	if err != nil || len(data) == 0 {
		return fmt.Errorf("provider returned invalid audio data")
	}
	queue := task.PrivateData.AudioQueue
	if queue == nil {
		return fmt.Errorf("audio request metadata is missing")
	}
	ext, mimeType := "."+queue.Format, ""
	switch queue.Format {
	case "mp3":
		mimeType = "audio/mpeg"
	case "wav":
		mimeType = "audio/wav"
	case "ogg_opus":
		ext, mimeType = ".ogg", "audio/ogg"
	case "pcm":
		mimeType = "audio/pcm"
	default:
		return fmt.Errorf("unsupported audio format")
	}
	var duration float64
	if queue.Format == "pcm" {
		if queue.SampleRate <= 0 || len(data)%2 != 0 {
			return fmt.Errorf("invalid PCM audio")
		}
		duration = float64(len(data)/2) / float64(queue.SampleRate)
	} else {
		duration, err = common.GetAudioDuration(ctx, bytes.NewReader(data), ext)
	}
	if err != nil || duration <= 0 || math.IsInf(duration, 0) || math.IsNaN(duration) {
		return fmt.Errorf("provider returned invalid audio content")
	}
	if err = os.MkdirAll(AudioTaskStoragePath(), 0700); err != nil {
		return err
	}
	path := filepath.Join(AudioTaskStoragePath(), task.TaskID+ext)
	defer os.Remove(path + ".tmp")
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return err
	}
	task.PrivateData.LocalPath, task.PrivateData.MimeType = path, mimeType
	task.PrivateData.AssetType, task.PrivateData.FileSize = "audio", int64(len(data))
	task.PrivateData.StoredAt = time.Now().Unix()
	task.PrivateData.ExpiresAt = time.Now().Add(GetAsyncImageRetention()).Unix()
	task.PrivateData.SourceURL = result.URL
	task.PrivateData.ResultURL = AudioTaskContentURL(task.TaskID)
	result.Audio, result.URL = "", task.PrivateData.ResultURL
	task.Data, err = common.Marshal(result)
	return err
}

func ServeAudioTaskContent(c *gin.Context, task *model.Task) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if task.Status != model.TaskStatusSuccess {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "audio_not_ready", "message": "Audio task is not successful"}})
		return
	}
	if task.PrivateData.ExpiresAt <= time.Now().Unix() || task.PrivateData.LocalPath == "" {
		c.JSON(http.StatusGone, gin.H{"error": gin.H{"code": "audio_expired", "message": "Audio content has expired"}})
		return
	}
	file, err := os.Open(task.PrivateData.LocalPath)
	if err != nil {
		c.Status(http.StatusGone)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", task.PrivateData.MimeType)
	http.ServeContent(c.Writer, c.Request, filepath.Base(task.PrivateData.LocalPath), info.ModTime(), file)
}

func CleanupExpiredAudioTasks(ctx context.Context) error {
	var afterID int64
	for {
		var tasks []*model.Task
		if err := model.DB.WithContext(ctx).Select("id", "private_data").Where("id > ? AND action = ? AND status IN ? AND finish_time > 0 AND finish_time < ?", afterID, "audio_generation", []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure}, time.Now().Add(-GetAsyncImageRetention()).Unix()).Order("id").Limit(100).Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) == 0 {
			return nil
		}
		for _, task := range tasks {
			afterID = task.ID
			if task.PrivateData.AudioQueue == nil {
				continue
			}
			if task.PrivateData.LocalPath != "" && task.PrivateData.ExpiresAt > time.Now().Unix() {
				continue
			}
			for _, path := range []string{task.PrivateData.LocalPath, task.PrivateData.RequestBodyPath} {
				if path == "" {
					continue
				}
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
	}
}
