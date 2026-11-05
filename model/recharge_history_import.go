package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HistoricalRecharge is a reviewed historical event, never a payment order.
// Pending evidence is admin-only; confirmation cannot credit a wallet.
type HistoricalRecharge struct {
	Id         int     `json:"id" gorm:"primaryKey"`
	SourceKey  string  `json:"source_key" gorm:"size:64;not null;uniqueIndex"`
	JobID      int     `json:"job_id" gorm:"index"`
	LogID      int     `json:"log_id"`
	RequestID  string  `json:"request_id" gorm:"size:128"`
	RequestKey *string `json:"-" gorm:"size:64;uniqueIndex"`
	UserID     int     `json:"user_id" gorm:"index;index:idx_historical_user_time,priority:1"`
	Quota      int     `json:"quota"`
	OccurredAt int64   `json:"occurred_at" gorm:"index;index:idx_historical_user_time,priority:2"`
	Evidence   string  `json:"evidence" gorm:"type:text"`
	Status     string  `json:"status" gorm:"size:16;index"`
	ReviewNote string  `json:"review_note" gorm:"type:text"`
	ReviewedBy int     `json:"reviewed_by"`
	ReviewedAt int64   `json:"reviewed_at"`
}

// Each explicit advance scans at most 100 rows. The committed cursor allows
// pausing simply by closing the page and resuming after a server restart.
type RechargeImportJob struct {
	Id         int   `json:"id" gorm:"primaryKey"`
	StartTime  int64 `json:"start_time"`
	EndTime    int64 `json:"end_time"`
	Cursor     int   `json:"cursor" gorm:"column:last_log_id"`
	CursorTime int64 `json:"cursor_time"`
	Done       bool  `json:"done"`
	CreatedBy  int   `json:"created_by"`
	CreatedAt  int64 `json:"created_at"`
}

var ErrRechargeImportConflict = errors.New("historical recharge review conflict")
var ErrRechargeImportUnsupported = errors.New("historical recharge scan requires stable SQL log IDs; ClickHouse scanning is disabled")

func CreateRechargeImportJob(ctx context.Context, start, end int64, actor int) (*RechargeImportJob, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return nil, ErrRechargeImportUnsupported
	}
	start, end, err := ValidateRechargeRecordWindow(start, end)
	if err != nil || actor <= 0 {
		return nil, ErrInvalidRechargeRecordFilter
	}
	job := &RechargeImportJob{StartTime: start, EndTime: end, CreatedBy: actor, CreatedAt: common.GetTimestamp(), CursorTime: start}
	return job, DB.WithContext(ctx).Create(job).Error
}

func AdvanceRechargeImportJob(ctx context.Context, id int) (*RechargeImportJob, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return nil, ErrRechargeImportUnsupported
	}
	var job RechargeImportJob
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&job, id).Error; err != nil {
			return err
		}
		if job.Done {
			return nil
		}
		var logs []Log
		// Read the separately configured log DB. Never use a generated display ID.
		logDB := LOG_DB
		if logDB == DB {
			logDB = tx
		}
		err := logDB.WithContext(ctx).Model(&Log{}).
			Select("id", "user_id", "created_at", "content", "other", "request_id", "type", "quota").
			Where("created_at >= ? AND created_at <= ? AND (created_at > ? OR (created_at = ? AND id > ?))", job.StartTime, job.EndTime, job.CursorTime, job.CursorTime, job.Cursor).
			Where("type IN ?", []int{LogTypeManage, LogTypeTopup}).Order("created_at asc, id asc").Limit(100).Find(&logs).Error
		if err != nil {
			return err
		}
		oldID, oldTime := job.Cursor, job.CursorTime
		for _, entry := range logs {
			job.Cursor, job.CursorTime = entry.Id, entry.CreatedAt
			if entry.Id <= 0 {
				return ErrRechargeImportUnsupported
			}
			var operation struct {
				Op struct {
					Action string `json:"action"`
					Params struct {
						UserID int `json:"target_user_id"`
					} `json:"params"`
				} `json:"op"`
			}
			_ = common.UnmarshalJsonStr(entry.Other, &operation)
			if operation.Op.Action != "" && operation.Op.Action != "user.quota_add" {
				continue
			}
			if operation.Op.Action != "user.quota_add" && !(strings.Contains(entry.Content, "增加用户额度") || strings.HasPrefix(entry.Content, "Increased user quota by")) {
				continue
			}
			proposed := adminQuotaTopUpFromLog(&entry)
			// Keep the legacy deterministic key so an already imported TopUp cannot
			// become a second recharge through this review workflow.
			key := fmt.Sprintf("ADMLOG%x", common.Sha256Raw([]byte(fmt.Sprintf("%d|%d|%d|%s|%s|%s", entry.Id, entry.UserId, entry.CreatedAt, entry.RequestId, entry.Content, entry.Other))))[:30]
			userID, quota := 0, 0
			if proposed != nil {
				key = proposed.TradeNo
				quota = proposed.QuotaAmount
			}
			if operation.Op.Action == "user.quota_add" {
				userID = operation.Op.Params.UserID
				if credit, currency, ok := topupCredit(entry); ok && currency == "" && credit.IsPositive() {
					if value, err := common.WalletQuotaFromDecimalStrict(credit); err == nil {
						quota = value
					}
				}
			}
			var existing int64
			if err := tx.Model(&TopUp{}).Where("trade_no = ?", key).Count(&existing).Error; err != nil {
				return err
			}
			if existing > 0 {
				continue
			}
			candidate := HistoricalRecharge{SourceKey: key, JobID: job.Id, LogID: entry.Id, RequestID: entry.RequestId, UserID: userID, Quota: quota, OccurredAt: entry.CreatedAt, Evidence: entry.Content, Status: "pending"}
			if entry.RequestId != "" {
				requestKey := fmt.Sprintf("%x", common.Sha256Raw([]byte(fmt.Sprintf("user.quota_add|%s|%d", entry.RequestId, userID))))
				candidate.RequestKey = &requestKey
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
				return err
			}
		}
		job.Done = len(logs) < 100
		update := tx.Model(&RechargeImportJob{}).Where("id = ? AND last_log_id = ? AND cursor_time = ?", job.Id, oldID, oldTime).
			Updates(map[string]any{"last_log_id": job.Cursor, "cursor_time": job.CursorTime, "done": job.Done})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrRechargeImportConflict
		}
		return nil
	})
	return &job, err
}

type RechargeImportReviewPage struct {
	Jobs         []RechargeImportJob  `json:"jobs"`
	Items        []HistoricalRecharge `json:"items"`
	NextBeforeID int                  `json:"next_before_id"`
}

func ListRechargeImportReview(ctx context.Context, before int) (*RechargeImportReviewPage, error) {
	page := &RechargeImportReviewPage{Jobs: []RechargeImportJob{}, Items: []HistoricalRecharge{}}
	if err := DB.WithContext(ctx).Order("id desc").Limit(20).Find(&page.Jobs).Error; err != nil {
		return nil, err
	}
	query := DB.WithContext(ctx).Where("status = ?", "pending")
	if before > 0 {
		query = query.Where("id < ?", before)
	}
	if err := query.Order("id desc").Limit(100).Find(&page.Items).Error; err != nil {
		return nil, err
	}
	if len(page.Items) == 100 {
		page.NextBeforeID = page.Items[99].Id
	}
	return page, nil
}

func ReviewHistoricalRecharge(ctx context.Context, id, userID, quota, actor int, note string, reject bool) error {
	note = strings.TrimSpace(note)
	if id <= 0 || actor <= 0 || note == "" || utf8.RuneCountInString(note) > 2000 {
		return ErrInvoiceValidation
	}
	if !reject && (userID <= 0 || quota <= 0 || common.ValidateWalletQuota(quota) != nil) {
		return ErrInvoiceValidation
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidate HistoricalRecharge
		if err := lockForUpdate(tx).First(&candidate, id).Error; err != nil {
			return err
		}
		if candidate.Status != "pending" {
			return ErrRechargeImportConflict
		}
		status := "rejected"
		if !reject {
			var user User
			if err := tx.Select("id").First(&user, userID).Error; err != nil {
				return err
			}
			var duplicates int64
			if err := tx.Model(&TopUp{}).Where("trade_no = ?", candidate.SourceKey).Count(&duplicates).Error; err != nil {
				return err
			}
			if duplicates > 0 {
				return ErrRechargeImportConflict
			}
			// An unlinked but matching administrator order is ambiguous. Do not
			// turn it into a second recharge; the reviewer can reject the candidate.
			if err := tx.Model(&TopUp{}).Where("user_id = ? AND payment_provider = ? AND quota_amount = ? AND complete_time = ?", userID, PaymentProviderAdmin, quota, candidate.OccurredAt).Count(&duplicates).Error; err != nil {
				return err
			}
			if duplicates > 0 {
				return ErrRechargeImportConflict
			}
			if candidate.RequestID != "" {
				if err := tx.Model(&HistoricalRecharge{}).Where("id <> ? AND request_id = ? AND status = ? AND user_id = ?", id, candidate.RequestID, "confirmed", userID).Count(&duplicates).Error; err != nil {
					return err
				}
				if duplicates > 0 {
					return ErrRechargeImportConflict
				}
			}
			status = "confirmed"
		}
		result := tx.Model(&HistoricalRecharge{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]any{
			"status": status, "user_id": userID, "quota": quota, "review_note": note, "reviewed_by": actor, "reviewed_at": common.GetTimestamp(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRechargeImportConflict
		}
		return nil
	})
}
