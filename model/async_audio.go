package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// AsyncAudioQueueState checkpoints a synchronous provider call. Submitting
// without Accepted is an uncertain outcome and must never be submitted again.
type AsyncAudioQueueState struct {
	QuotaClamp    *common.QuotaClamp `json:"quota_clamp,omitempty"`
	PluginKey     string             `json:"plugin_key"`
	PluginVersion string             `json:"plugin_version"`
	Format        string             `json:"format"`
	SampleRate    int                `json:"sample_rate,omitempty"`
	Submitting    bool               `json:"submitting,omitempty"`
	Accepted      bool               `json:"accepted,omitempty"`
	LeaseUntil    int64              `json:"lease_until,omitempty"`
}

func GetAsyncAudioQueueTasks(ctx context.Context) ([]*Task, error) {
	var tasks []*Task
	// Claim reloads the complete row. Avoid repeatedly reading accepted Base64
	// checkpoints while another worker holds their lease.
	err := DB.WithContext(ctx).Select("id", "status", "private_data").Where("action = ? AND status IN ?", "audio_generation", []TaskStatus{TaskStatusSubmitted, TaskStatusInProgress}).Order("id").Limit(500).Find(&tasks).Error
	return tasks, err
}

// ClaimAsyncAudioTask fences workers across processes. The ten-minute provider
// deadline is shorter than this lease; an interrupted POST is never replayed.
func ClaimAsyncAudioTask(task *Task) (bool, error) {
	won := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current Task
		if err := lockForUpdate(tx).First(&current, task.ID).Error; err != nil {
			return err
		}
		queue := current.PrivateData.AudioQueue
		if queue == nil || queue.LeaseUntil > time.Now().Unix() || (current.Status != TaskStatusSubmitted && current.Status != TaskStatusInProgress) {
			return nil
		}
		current.Status = TaskStatusInProgress
		current.Progress = "50%"
		if current.StartTime == 0 {
			current.StartTime = time.Now().Unix()
		}
		current.PrivateData.WorkerAttempts++
		queue.LeaseUntil = time.Now().Add(15 * time.Minute).Unix()
		if err := tx.Model(&current).Select("*").Updates(&current).Error; err != nil {
			return err
		}
		*task = current
		won = true
		return nil
	})
	return won, err
}

func SaveAsyncAudioTask(ctx context.Context, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Task
		if err := lockForUpdate(tx).First(&current, task.ID).Error; err != nil {
			return err
		}
		if current.Status != TaskStatusInProgress || current.PrivateData.WorkerAttempts != task.PrivateData.WorkerAttempts || current.PrivateData.AudioQueue == nil || current.PrivateData.AudioQueue.LeaseUntil <= time.Now().Unix() {
			return fmt.Errorf("audio task worker lease lost")
		}
		return tx.Model(task).Select("*").Updates(task).Error
	})
}
