package service

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"gorm.io/gorm"
)

// The task row is the durable lease. Unknown outcomes remain protected, and a
// terminal task keeps its inputs for a full idle lifetime after completion.
func protectedTaskReferences(tx *gorm.DB, user int) (map[string]bool, error) {
	protected := make(map[string]bool)
	if tx == nil {
		return nil, ErrReferenceUnavailable
	}
	query := tx.Model(&model.Task{}).Select("user_id", "private_data").Where(
		"status NOT IN ? OR finish_time = 0 OR finish_time > ?",
		[]model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure}, time.Now().Add(-ReferenceRetention()).Unix())
	if user > 0 {
		query = query.Where("user_id = ?", user)
	}
	var tasks []model.Task
	if err := query.Find(&tasks).Error; err != nil {
		return nil, err
	}
	for _, task := range tasks {
		for _, id := range task.PrivateData.ReferenceAssetIDs {
			protected[fmt.Sprintf("%d-%s", task.UserId, id)] = true
		}
	}
	return protected, nil
}

// InsertImageTaskWithReferences publishes the task and its leases atomically
// against both filesystem and database-backed reference cleanup. No new table
// or process-local heartbeat is needed, so restart cannot release active input.
func InsertImageTaskWithReferences(task *model.Task) error {
	store := ReferenceUploads
	if store == nil || task.PrivateData.ImageRetry == nil {
		return task.Insert()
	}
	body, err := os.ReadFile(task.PrivateData.ImageRetry.BodyPath)
	if err != nil {
		return err
	}
	refresh := referenceURLRefresh{user: task.UserId, renewed: make(map[string]string)}
	if _, _, err := refresh.body(body, task.PrivateData.RequestContentType); err != nil {
		return err
	}
	ids := make(map[string]bool)
	for address, renewed := range refresh.renewed {
		if address == renewed {
			// An already fresh signature can be identical within the same second.
			// External origins, however, must never become owned task leases.
			parsed, _ := url.Parse(address)
			origin, _ := url.Parse(system_setting.ServerAddress)
			if parsed == nil || origin == nil || !strings.EqualFold(parsed.Host, origin.Host) {
				continue
			}
		}
		parsed, err := url.Parse(renewed)
		if err != nil {
			return ErrReferenceInvalid
		}
		id := filepath.Base(parsed.Path)
		if validReferenceID(id) {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return task.Insert()
	}
	unlock, err := store.lockResumable()
	if err != nil {
		return err
	}
	defer unlock()
	return model.WithReferenceAdmission(func(tx *gorm.DB) error {
		protected, err := protectedTaskReferences(tx, task.UserId)
		if err != nil {
			return err
		}
		for id := range ids {
			name := fmt.Sprintf("%d-%s", task.UserId, id)
			valid := false
			if store.DirectEnabled() {
				var asset model.ReferenceAsset
				if err := tx.Where("user_id = ? AND sha256 = ?", task.UserId, id).Find(&asset).Error; err != nil {
					return err
				}
				if asset.State == "ready" && asset.StorageID == store.storageID && (asset.IdleExpires > time.Now().Unix() || protected[name]) {
					_, _, err := store.directBytes(&asset)
					valid = err == nil
				}
			}
			if !valid {
				info, err := os.Stat(filepath.Join(store.config.Directory, name+".asset"))
				valid = err == nil && info.Mode().IsRegular() && (time.Since(info.ModTime()) < ReferenceRetention() || protected[name])
			}
			if !valid {
				return ErrReferenceMissing
			}
			task.PrivateData.ReferenceAssetIDs = append(task.PrivateData.ReferenceAssetIDs, id)
		}
		return tx.Create(task).Error
	})
}
