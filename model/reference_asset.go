package model

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReferenceAsset is the authoritative metadata for relay-direct uploads.
// All instances serving a storage ID must mount the same directory.
type ReferenceAsset struct {
	ID             int64  `gorm:"primaryKey"`
	UserID         int    `gorm:"uniqueIndex:idx_reference_owner_hash,priority:1;not null"`
	SHA256         string `gorm:"type:varchar(64);uniqueIndex:idx_reference_owner_hash,priority:2;not null"`
	SessionID      string `gorm:"type:varchar(36);uniqueIndex;not null"`
	StorageID      string `gorm:"type:varchar(36);not null;index"`
	State          string `gorm:"type:varchar(16);not null"`
	Size           int64
	MIME           string `gorm:"type:varchar(32)"`
	Generation     string `gorm:"type:varchar(36)"`
	LeaseUntil     int64
	SessionExpires int64
	IdleExpires    int64
	FileName       string `gorm:"type:varchar(64)"`
}

type ReferencePreparation struct {
	ID        int64  `gorm:"primaryKey"`
	UserID    int    `gorm:"uniqueIndex:idx_reference_prepare_owner,priority:1;not null"`
	KeyHash   string `gorm:"type:varchar(64);uniqueIndex:idx_reference_prepare_owner,priority:2;not null"`
	AssetID   int64  `gorm:"index;not null"`
	SessionID string `gorm:"type:varchar(36);not null"`
}

// The admission row serializes quota reservations and filesystem publication,
// including SQLite, whose SELECT FOR UPDATE is unsupported. Network upload
// reads never hold this transaction.
type ReferenceAdmission struct {
	ID    int `gorm:"primaryKey;autoIncrement:false"`
	Guard int
}

func WithReferenceAdmission(fn func(*gorm.DB) error) error {
	if DB == nil {
		return errors.New("reference database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&ReferenceAdmission{ID: 1}).Error; err != nil {
			return err
		}
		// An UPDATE acquires a write lock even when the value is unchanged.
		if err := tx.Model(&ReferenceAdmission{}).Where("id = ?", 1).Update("guard", 0).Error; err != nil {
			return err
		}
		return fn(tx)
	})
}
