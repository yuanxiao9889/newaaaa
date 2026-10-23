package service

import (
	"os"
	"path/filepath"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/shirou/gopsutil/disk"
	"gorm.io/gorm"
)

type ReferenceStorageStatus struct {
	Enabled           bool   `json:"enabled"`
	Available         bool   `json:"available"`
	Measured          bool   `json:"measured"`
	StoredBytes       int64  `json:"stored_bytes"`
	ReservedBytes     int64  `json:"reserved_bytes"`
	ReadyAssets       int    `json:"ready_assets"`
	ActiveUploads     int    `json:"active_uploads"`
	MaxCacheBytes     int64  `json:"max_cache_bytes"`
	MaxUserCacheBytes int64  `json:"max_user_cache_bytes"`
	FreeBytes         uint64 `json:"free_bytes"`
	RequiredFreeBytes uint64 `json:"required_free_bytes"`
	RetentionSeconds  int64  `json:"retention_seconds"`
}

func (s *ReferenceUploadStore) DirectStatus() ReferenceStorageStatus {
	status := ReferenceStorageStatus{Enabled: s.DirectEnabled(), RetentionSeconds: int64(ReferenceRetention().Seconds())}
	if !status.Enabled {
		return status
	}
	status.MaxCacheBytes, status.MaxUserCacheBytes = s.config.MaxCacheBytes, s.config.MaxUserCacheBytes
	err := model.WithReferenceAdmission(func(tx *gorm.DB) error {
		var assets []model.ReferenceAsset
		if err := tx.Where("storage_id = ?", s.storageID).Find(&assets).Error; err != nil {
			return err
		}
		now := time.Now().Unix()
		for _, asset := range assets {
			if asset.State == "ready" && asset.IdleExpires > now {
				info, err := os.Stat(filepath.Join(s.directDir, asset.FileName))
				if err == nil && info.Mode().IsRegular() && info.Size() == asset.Size {
					status.ReadyAssets++
				}
			} else if asset.State != "ready" && asset.SessionExpires > now {
				status.ReservedBytes += asset.Size
				if asset.State == "uploading" && asset.LeaseUntil > now {
					status.ActiveUploads++
				}
			}
		}
		entries, err := os.ReadDir(s.directDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				status.StoredBytes += info.Size()
			}
		}
		return nil
	})
	space, diskErr := disk.Usage(s.directDir)
	if err != nil || diskErr != nil {
		return ReferenceStorageStatus{Enabled: true, RetentionSeconds: status.RetentionSeconds}
	}
	status.FreeBytes = space.Free
	status.RequiredFreeBytes = max(uint64(2<<30), space.Total*uint64(s.config.MinFreePercent)/100)
	status.Available = space.Free >= status.RequiredFreeBytes+uint64(status.ReservedBytes) &&
		status.StoredBytes+status.ReservedBytes < status.MaxCacheBytes
	status.Measured = true
	return status
}
