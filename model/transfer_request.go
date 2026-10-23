package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Receipts are bounded metadata/text copies, not a second image asset store.
// Oversized responses retain their permanent identity but are never redispatched.
const TransferResponseLimit = 1 << 20
const TransferResponseRetention = 7 * 24 * time.Hour

var transferRequestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,96}$`)
var ErrTransferRequestConflict = errors.New("idempotency key was already used for different input")

// Keys and digests are permanent tombstones. Only bounded response content is
// compacted after seven days. A lease expiry never authorizes a second dispatch.
type TransferRequest struct {
	ID              int64  `gorm:"primaryKey"`
	UserID          int    `gorm:"uniqueIndex:idx_transfer_owner_key,priority:1;not null"`
	RequestKey      string `gorm:"type:varchar(96);not null"`
	KeyHash         string `gorm:"type:varchar(64);uniqueIndex:idx_transfer_owner_key,priority:2;not null"`
	Digest          string `gorm:"type:varchar(64);not null"`
	DigestVersion   int    `gorm:"not null"`
	ExecutionID     string `gorm:"type:varchar(64);uniqueIndex;not null"`
	State           string `gorm:"type:varchar(16);not null;index"`
	CreatedAt       int64
	UpdatedAt       int64
	FinishedAt      int64 `gorm:"index"`
	ResponseStatus  int
	ResponseHeaders string `gorm:"type:text"`
	ResponseBody    []byte
}

func ValidTransferRequestKey(key string) bool {
	return transferRequestKeyPattern.MatchString(key)
}

func FindTransferRequest(userID int, key string) (*TransferRequest, error) {
	var record TransferRequest
	hash := sha256.Sum256([]byte(key))
	err := DB.Where("user_id = ? AND key_hash = ?", userID, hex.EncodeToString(hash[:])).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func ClaimTransferRequest(userID int, key, digest string) (*TransferRequest, bool, error) {
	if userID <= 0 || !ValidTransferRequestKey(key) || len(digest) != 64 {
		return nil, false, errors.New("invalid transfer request identity")
	}
	now := time.Now().Unix()
	hash := sha256.Sum256([]byte(key))
	record := TransferRequest{UserID: userID, RequestKey: key, KeyHash: hex.EncodeToString(hash[:]), Digest: digest, DigestVersion: 1,
		ExecutionID: uuid.NewString(), State: "running", CreatedAt: now, UpdatedAt: now}
	result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return &record, true, nil
	}
	existing, err := FindTransferRequest(userID, key)
	if err != nil || existing == nil {
		return nil, false, errors.New("request registration could not be confirmed")
	}
	if existing.DigestVersion != 1 || existing.Digest != digest {
		return nil, false, ErrTransferRequestConflict
	}
	return existing, false, nil
}

func FinishTransferRequest(record *TransferRequest, state string, status int, headers string, body []byte) error {
	if len(body) > TransferResponseLimit {
		return errors.New("response exceeds recovery limit")
	}
	now := time.Now().Unix()
	update := DB.Model(&TransferRequest{}).
		Where("id = ? AND execution_id = ? AND state = ?", record.ID, record.ExecutionID, "running").
		Updates(map[string]any{"state": state, "response_status": status,
			"response_headers": headers, "response_body": body, "updated_at": now, "finished_at": now})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return errors.New("request execution no longer owns the registration")
	}
	return nil
}

// ResumeReferenceSubmission can reopen only an authoritative preprocessing
// rejection, with the same owner, identity and exact input digest. A timeout,
// upstream error, accepted task or completed generation can never enter here.
func ResumeReferenceSubmission(record *TransferRequest) (bool, error) {
	result := DB.Model(&TransferRequest{}).Where("id = ? AND user_id = ? AND execution_id = ? AND digest = ? AND state = ?",
		record.ID, record.UserID, record.ExecutionID, record.Digest, "input_required").
		Updates(map[string]any{"state": "running", "updated_at": time.Now().Unix(), "finished_at": 0,
			"response_status": 0, "response_headers": "", "response_body": nil})
	return result.RowsAffected == 1, result.Error
}

func CompactTransferResponses() error {
	// Unknown and running operations are never compacted. Keys are never deleted.
	return DB.Model(&TransferRequest{}).
		Where("state IN ? AND finished_at > 0 AND finished_at < ?", []string{"succeeded", "failed"},
			time.Now().Add(-TransferResponseRetention).Unix()).
		Updates(map[string]any{"state": "expired", "response_body": nil, "response_headers": ""}).Error
}
