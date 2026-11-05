package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	InvoiceActionMark    = "mark"
	InvoiceActionCorrect = "correct"
	InvoiceActionRevoke  = "revoke"
)

var (
	ErrInvoiceValidation       = errors.New("invalid invoice marking")
	ErrInvoiceStateConflict    = errors.New("invoice state conflict")
	ErrInvoiceRecordMismatch   = errors.New("invoice records must belong to one user")
	ErrInvoiceIdempotency      = errors.New("idempotency key was already used for a different request")
	ErrInvoiceHistoryNotFound  = errors.New("invoice history not found")
	invoiceWriteAttemptTimeout = 15 * time.Second
)

// RechargeRecordInvoice is the independent invoice marking for one business
// recharge record. The source record remains the accounting authority.
type RechargeRecordInvoice struct {
	Id             int      `json:"id" gorm:"primaryKey"`
	SourceType     string   `json:"source" gorm:"size:16;not null;uniqueIndex:idx_recharge_invoice_source,priority:1"`
	SourceID       int      `json:"source_id" gorm:"not null;uniqueIndex:idx_recharge_invoice_source,priority:2"`
	UserID         int      `json:"user_id" gorm:"not null;index"`
	Status         string   `json:"status" gorm:"size:16;not null;index"`
	InvoiceNo      string   `json:"invoice_no" gorm:"size:128;not null"`
	InvoiceDate    string   `json:"invoice_date" gorm:"size:10;not null"`
	InternalNote   string   `json:"internal_note" gorm:"type:text;not null"`
	Verification   string   `json:"verification_note" gorm:"column:verification_note;type:text;not null"`
	ReceiptChecked bool     `json:"receipt_confirmed" gorm:"column:receipt_confirmed;not null"`
	Version        int      `json:"version" gorm:"not null"`
	MarkedAt       int64    `json:"marked_at" gorm:"not null"`
	MarkedBy       int      `json:"marked_by" gorm:"not null"`
	MarkedByName   string   `json:"marked_by_name" gorm:"size:100;not null"`
	SourceStatus   string   `json:"source_status" gorm:"size:32;not null"`
	SourceRecordNo string   `json:"source_record_no" gorm:"size:255;not null"`
	SourceKind     string   `json:"source_kind" gorm:"size:32;not null"`
	SourceTime     int64    `json:"source_time" gorm:"not null;index"`
	SourceQuota    int      `json:"source_quota" gorm:"not null"`
	SourcePaid     *float64 `json:"source_paid_amount" gorm:"column:source_paid_amount"`
	SourceCurrency string   `json:"source_currency" gorm:"size:8;not null"`
	SourcePaidOK   bool     `json:"source_paid_confirmed" gorm:"column:source_paid_confirmed;not null"`
	SourceSnapshot string   `json:"-" gorm:"type:text;not null"`
	CreatedAt      int64    `json:"created_at" gorm:"not null"`
	UpdatedAt      int64    `json:"updated_at" gorm:"not null"`
}

func (RechargeRecordInvoice) TableName() string {
	return "recharge_record_invoices"
}

// RechargeRecordInvoiceHistory is append-only. There is deliberately no update
// or delete helper for this table.
type RechargeRecordInvoiceHistory struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	SourceType     string `json:"source" gorm:"size:16;not null;index:idx_recharge_history_source,priority:1"`
	SourceID       int    `json:"source_id" gorm:"not null;index:idx_recharge_history_source,priority:2"`
	InvoiceID      int    `json:"invoice_id" gorm:"not null"`
	UserID         int    `json:"user_id" gorm:"not null;index"`
	Action         string `json:"action" gorm:"size:16;not null"`
	RequestID      string `json:"request_id" gorm:"size:128;not null;index"`
	RequestHash    string `json:"-" gorm:"size:64;not null"`
	OperatorID     int    `json:"operator_id" gorm:"not null"`
	OperatorName   string `json:"operator_name" gorm:"size:100;not null"`
	Reason         string `json:"reason" gorm:"type:text;not null"`
	BeforeSnapshot string `json:"before" gorm:"type:text;not null"`
	AfterSnapshot  string `json:"after" gorm:"type:text;not null"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
}

func (RechargeRecordInvoiceHistory) TableName() string {
	return "recharge_record_invoice_histories"
}

// RechargeRecordInvoiceRequest makes the request idempotency key a database
// constraint instead of a best-effort read. A retry with the same hash is
// answered from the committed history without applying a second mutation.
type RechargeRecordInvoiceRequest struct {
	Id          int    `json:"id" gorm:"primaryKey"`
	RequestID   string `json:"request_id" gorm:"size:128;not null;uniqueIndex"`
	RequestHash string `json:"request_hash" gorm:"size:64;not null"`
	Action      string `json:"action" gorm:"size:16;not null"`
	OperatorID  int    `json:"operator_id" gorm:"not null"`
	CreatedAt   int64  `json:"created_at" gorm:"not null"`
}

func (RechargeRecordInvoiceRequest) TableName() string {
	return "recharge_record_invoice_requests"
}

type InvoiceMarkingRecord struct {
	Source          string `json:"source"`
	ID              int    `json:"id"`
	ExpectedVersion int    `json:"expected_version"`
}

type InvoiceMarkingRequest struct {
	Action           string                 `json:"action"`
	Records          []InvoiceMarkingRecord `json:"records"`
	InvoiceNo        string                 `json:"invoice_no"`
	InvoiceDate      string                 `json:"invoice_date"`
	InternalNote     string                 `json:"internal_note"`
	VerificationNote string                 `json:"verification_note"`
	Reason           string                 `json:"reason"`
	ReceiptConfirmed bool                   `json:"receipt_confirmed"`
	RequestID        string                 `json:"request_id"`
	OperatorID       int                    `json:"-"`
	OperatorName     string                 `json:"-"`
}

type InvoiceMarkingResultRecord struct {
	Source      string `json:"source"`
	ID          int    `json:"id"`
	Status      string `json:"invoice_status"`
	Version     int    `json:"invoice_version"`
	InvoiceNo   string `json:"invoice_no,omitempty"`
	InvoiceDate string `json:"invoice_date,omitempty"`
}

type InvoiceMarkingResult struct {
	Action   string                       `json:"action"`
	Count    int                          `json:"count"`
	Replayed bool                         `json:"replayed"`
	Records  []InvoiceMarkingResultRecord `json:"records"`
}

type InvoiceMarkingConflict struct {
	Source         string `json:"source"`
	ID             int    `json:"id"`
	Reason         string `json:"reason"`
	CurrentVersion int    `json:"current_version,omitempty"`
}

type InvoiceMarkingConflictError struct {
	Conflicts []InvoiceMarkingConflict `json:"conflicts"`
}

func (err *InvoiceMarkingConflictError) Error() string {
	if err == nil || len(err.Conflicts) == 0 {
		return ErrInvoiceStateConflict.Error()
	}
	return fmt.Sprintf("%s: %s", ErrInvoiceStateConflict.Error(), err.Conflicts[0].Reason)
}

type InvoiceHistoryView struct {
	Id           int             `json:"id"`
	Source       string          `json:"source"`
	SourceID     int             `json:"source_id"`
	Action       string          `json:"action"`
	RequestID    string          `json:"request_id"`
	OperatorID   int             `json:"operator_id"`
	OperatorName string          `json:"operator_name"`
	Reason       string          `json:"reason"`
	CreatedAt    int64           `json:"created_at"`
	Before       InvoiceSnapshot `json:"before"`
	After        InvoiceSnapshot `json:"after"`
}

type InvoiceSnapshot struct {
	Status         string `json:"status"`
	InvoiceNo      string `json:"invoice_no"`
	InvoiceDate    string `json:"invoice_date"`
	InternalNote   string `json:"internal_note"`
	Verification   string `json:"verification_note"`
	ReceiptChecked bool   `json:"receipt_confirmed"`
	Version        int    `json:"version"`
	MarkedAt       int64  `json:"marked_at"`
	MarkedBy       int    `json:"marked_by"`
	MarkedByName   string `json:"marked_by_name"`
}

func validateInvoiceMarkingRequest(req *InvoiceMarkingRequest) error {
	if req == nil {
		return ErrInvoiceValidation
	}
	req.Action = strings.TrimSpace(req.Action)
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.InvoiceNo = strings.TrimSpace(req.InvoiceNo)
	req.InvoiceDate = strings.TrimSpace(req.InvoiceDate)
	req.InternalNote = strings.TrimSpace(req.InternalNote)
	req.VerificationNote = strings.TrimSpace(req.VerificationNote)
	req.Reason = strings.TrimSpace(req.Reason)

	if req.Action != InvoiceActionMark && req.Action != InvoiceActionCorrect && req.Action != InvoiceActionRevoke {
		return ErrInvoiceValidation
	}
	if len(req.Records) == 0 || len(req.Records) > 100 {
		return ErrInvoiceValidation
	}
	if len(req.RequestID) < 8 || len(req.RequestID) > 128 {
		return ErrInvoiceValidation
	}
	if req.OperatorID <= 0 || strings.TrimSpace(req.OperatorName) == "" {
		return ErrInvoiceValidation
	}
	if utf8.RuneCountInString(req.InvoiceNo) > 128 ||
		utf8.RuneCountInString(req.InternalNote) > 2000 ||
		utf8.RuneCountInString(req.VerificationNote) > 2000 ||
		utf8.RuneCountInString(req.Reason) > 1000 {
		return ErrInvoiceValidation
	}
	if len(req.InvoiceDate) > 10 {
		return ErrInvoiceValidation
	}
	if _, err := ParseRechargeRecordDate(req.InvoiceDate); err != nil {
		return ErrInvoiceValidation
	}
	if (req.Action == InvoiceActionCorrect || req.Action == InvoiceActionRevoke) && req.Reason == "" {
		return ErrInvoiceValidation
	}

	seen := make(map[string]struct{}, len(req.Records))
	for i := range req.Records {
		record := &req.Records[i]
		record.Source = strings.TrimSpace(record.Source)
		if record.Source != RechargeSourceTopUp && record.Source != RechargeSourceRedemption && record.Source != RechargeSourceHistorical {
			return ErrInvoiceValidation
		}
		if record.ID <= 0 || record.ExpectedVersion < 0 {
			return ErrInvoiceValidation
		}
		key := record.Source + ":" + fmt.Sprint(record.ID)
		if _, ok := seen[key]; ok {
			return ErrInvoiceValidation
		}
		seen[key] = struct{}{}
	}
	return nil
}

func invoiceRequestHash(req *InvoiceMarkingRequest) (string, error) {
	records := append([]InvoiceMarkingRecord(nil), req.Records...)
	slices.SortFunc(records, func(a, b InvoiceMarkingRecord) int {
		if a.Source != b.Source {
			return strings.Compare(a.Source, b.Source)
		}
		return a.ID - b.ID
	})
	canonical := struct {
		Action           string
		Records          []InvoiceMarkingRecord
		InvoiceNo        string
		InvoiceDate      string
		InternalNote     string
		VerificationNote string
		Reason           string
		ReceiptConfirmed bool
		OperatorID       int
	}{
		Action:           req.Action,
		Records:          records,
		InvoiceNo:        req.InvoiceNo,
		InvoiceDate:      req.InvoiceDate,
		InternalNote:     req.InternalNote,
		VerificationNote: req.VerificationNote,
		Reason:           req.Reason,
		ReceiptConfirmed: req.ReceiptConfirmed,
		OperatorID:       req.OperatorID,
	}
	encoded, err := common.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func invoiceSnapshot(invoice *RechargeRecordInvoice) InvoiceSnapshot {
	if invoice == nil || invoice.Id == 0 {
		return InvoiceSnapshot{Status: InvoiceStatusUnmarked}
	}
	status := invoice.Status
	if status == "" {
		status = InvoiceStatusUnmarked
	}
	return InvoiceSnapshot{
		Status:         status,
		InvoiceNo:      invoice.InvoiceNo,
		InvoiceDate:    invoice.InvoiceDate,
		InternalNote:   invoice.InternalNote,
		Verification:   invoice.Verification,
		ReceiptChecked: invoice.ReceiptChecked,
		Version:        invoice.Version,
		MarkedAt:       invoice.MarkedAt,
		MarkedBy:       invoice.MarkedBy,
		MarkedByName:   invoice.MarkedByName,
	}
}

func encodeInvoiceSnapshot(snapshot InvoiceSnapshot) string {
	encoded, err := common.Marshal(snapshot)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// MarkRechargeInvoices applies one mark/correct/revoke request in a single
// transaction. It only touches invoice and history tables; no recharge,
// balance, refund, or order state is read for update or written.
func MarkRechargeInvoices(ctx context.Context, req InvoiceMarkingRequest) (*InvoiceMarkingResult, error) {
	if err := validateInvoiceMarkingRequest(&req); err != nil {
		return nil, err
	}
	hash, err := invoiceRequestHash(&req)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, invoiceWriteAttemptTimeout)
	defer cancel()

	replay, err := loadInvoiceRequestReplay(ctx, req.RequestID, hash, req.Action)
	if err != nil || replay != nil {
		return replay, err
	}

	var result *InvoiceMarkingResult
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		request := RechargeRecordInvoiceRequest{
			RequestID:   req.RequestID,
			RequestHash: hash,
			Action:      req.Action,
			OperatorID:  req.OperatorID,
			CreatedAt:   common.GetTimestamp(),
		}
		if err := tx.Create(&request).Error; err != nil {
			return err
		}

		records, err := loadInvoiceTargetsWithTx(ctx, tx, req.Records)
		if err != nil {
			return err
		}
		userID := records[0].record.UserID
		for i := range records {
			if records[i].record.UserID != userID {
				return ErrInvoiceRecordMismatch
			}
		}
		requiresVerification := false
		for i := range records {
			if records[i].record.RequiresReceiptVerification() {
				requiresVerification = true
				break
			}
		}
		if requiresVerification && req.Action != InvoiceActionRevoke && (!req.ReceiptConfirmed || req.VerificationNote == "") {
			return ErrInvoiceValidation
		}

		now := common.GetTimestamp()
		result = &InvoiceMarkingResult{Action: req.Action, Count: len(records), Records: make([]InvoiceMarkingResultRecord, 0, len(records))}
		for i := range records {
			target := &records[i]
			if !target.record.IsMarkable() {
				return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
					Source: target.record.Source,
					ID:     target.record.SourceID,
					Reason: "not_markable",
				}}}
			}
			current := target.invoice
			if target.record.InvoiceVersion != target.expectedVersion {
				return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
					Source:         target.record.Source,
					ID:             target.record.SourceID,
					Reason:         "version_conflict",
					CurrentVersion: target.record.InvoiceVersion,
				}}}
			}
			if current == nil {
				if target.expectedVersion != 0 {
					return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
						Source: target.record.Source, ID: target.record.SourceID, Reason: "version_conflict",
					}}}
				}
				if req.Action == InvoiceActionCorrect || req.Action == InvoiceActionRevoke {
					return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
						Source: target.record.Source, ID: target.record.SourceID, Reason: "not_marked",
					}}}
				}
			} else {
				switch req.Action {
				case InvoiceActionMark:
					if current.Status != InvoiceStatusRevoked {
						return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
							Source: target.record.Source, ID: target.record.SourceID, Reason: "already_marked",
							CurrentVersion: current.Version,
						}}}
					}
				case InvoiceActionCorrect, InvoiceActionRevoke:
					if current.Status != InvoiceStatusMarked {
						return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
							Source: target.record.Source, ID: target.record.SourceID, Reason: "not_marked",
							CurrentVersion: current.Version,
						}}}
					}
				}
			}

			before := invoiceSnapshot(current)
			after := before
			after.Status = InvoiceStatusMarked
			after.Version = before.Version + 1
			after.MarkedAt = now
			after.MarkedBy = req.OperatorID
			after.MarkedByName = req.OperatorName
			if req.Action == InvoiceActionRevoke {
				after.Status = InvoiceStatusRevoked
				after.InvoiceNo = ""
				after.InvoiceDate = ""
			} else {
				after.InvoiceNo = req.InvoiceNo
				after.InvoiceDate = req.InvoiceDate
			}
			if req.Action == InvoiceActionCorrect {
				after.InternalNote = req.InternalNote
				after.Verification = req.VerificationNote
				after.ReceiptChecked = req.ReceiptConfirmed
			} else if req.Action == InvoiceActionMark {
				after.InternalNote = req.InternalNote
				after.Verification = req.VerificationNote
				after.ReceiptChecked = req.ReceiptConfirmed
			}

			snapshotJSON, err := common.Marshal(target.record)
			if err != nil {
				return err
			}
			if current == nil {
				current = &RechargeRecordInvoice{
					SourceType:     target.record.Source,
					SourceID:       target.record.SourceID,
					UserID:         target.record.UserID,
					Status:         after.Status,
					InvoiceNo:      after.InvoiceNo,
					InvoiceDate:    after.InvoiceDate,
					InternalNote:   after.InternalNote,
					Verification:   after.Verification,
					ReceiptChecked: after.ReceiptChecked,
					Version:        after.Version,
					MarkedAt:       now,
					MarkedBy:       req.OperatorID,
					MarkedByName:   req.OperatorName,
					SourceStatus:   target.record.Status,
					SourceRecordNo: target.record.RecordNo,
					SourceKind:     target.record.SourceKind,
					SourceTime:     target.record.RechargeTime,
					SourceQuota:    target.record.CreditedQuota,
					SourcePaid:     target.record.PaidAmount,
					SourceCurrency: target.record.Currency,
					SourcePaidOK:   target.record.PaidConfirmed,
					SourceSnapshot: string(snapshotJSON),
					CreatedAt:      now,
					UpdatedAt:      now,
				}
				if err := tx.Create(current).Error; err != nil {
					return err
				}
			} else {
				updates := map[string]any{
					"status":            after.Status,
					"invoice_no":        after.InvoiceNo,
					"invoice_date":      after.InvoiceDate,
					"internal_note":     after.InternalNote,
					"verification_note": after.Verification,
					"receipt_confirmed": after.ReceiptChecked,
					"version":           after.Version,
					"marked_at":         now,
					"marked_by":         req.OperatorID,
					"marked_by_name":    req.OperatorName,
					"updated_at":        now,
				}
				update := tx.Model(&RechargeRecordInvoice{}).
					Where("id = ? AND version = ?", current.Id, target.expectedVersion).
					Updates(updates)
				if update.Error != nil {
					return update.Error
				}
				if update.RowsAffected != 1 {
					return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
						Source: target.record.Source, ID: target.record.SourceID, Reason: "version_conflict",
						CurrentVersion: target.expectedVersion,
					}}}
				}
				current.Status = after.Status
				current.InvoiceNo = after.InvoiceNo
				current.InvoiceDate = after.InvoiceDate
				current.InternalNote = after.InternalNote
				current.Verification = after.Verification
				current.ReceiptChecked = after.ReceiptChecked
				current.Version = after.Version
				current.MarkedAt = now
				current.MarkedBy = req.OperatorID
				current.MarkedByName = req.OperatorName
			}

			history := RechargeRecordInvoiceHistory{
				SourceType:     target.record.Source,
				SourceID:       target.record.SourceID,
				InvoiceID:      current.Id,
				UserID:         target.record.UserID,
				Action:         req.Action,
				RequestID:      req.RequestID,
				RequestHash:    hash,
				OperatorID:     req.OperatorID,
				OperatorName:   req.OperatorName,
				Reason:         req.Reason,
				BeforeSnapshot: encodeInvoiceSnapshot(before),
				AfterSnapshot:  encodeInvoiceSnapshot(after),
				CreatedAt:      now,
			}
			if err := tx.Create(&history).Error; err != nil {
				return err
			}
			result.Records = append(result.Records, InvoiceMarkingResultRecord{
				Source: target.record.Source, ID: target.record.SourceID, Status: after.Status,
				Version: after.Version, InvoiceNo: after.InvoiceNo, InvoiceDate: after.InvoiceDate,
			})
		}
		return nil
	})
	if err != nil {
		if replay, replayErr := loadInvoiceRequestReplay(ctx, req.RequestID, hash, req.Action); replayErr == nil && replay != nil {
			return replay, nil
		}
		if conflict := invoiceVersionConflictForRefs(ctx, req.Records); conflict != nil {
			return nil, conflict
		}
		return nil, err
	}
	return result, nil
}

// invoiceVersionConflictForRefs turns a unique-key race on a first marking into
// the same 409 response an optimistic version conflict produces.
func invoiceVersionConflictForRefs(ctx context.Context, refs []InvoiceMarkingRecord) *InvoiceMarkingConflictError {
	for i := range refs {
		var invoice RechargeRecordInvoice
		err := DB.WithContext(ctx).Where("source_type = ? AND source_id = ?", refs[i].Source, refs[i].ID).First(&invoice).Error
		if err != nil {
			continue
		}
		if invoice.Version != refs[i].ExpectedVersion {
			return &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{
				Source: refs[i].Source, ID: refs[i].ID, Reason: "version_conflict",
				CurrentVersion: invoice.Version,
			}}}
		}
	}
	return nil
}

type invoiceTarget struct {
	record          *RechargeRecord
	invoice         *RechargeRecordInvoice
	expectedVersion int
}

func loadInvoiceTargetsWithTx(ctx context.Context, tx *gorm.DB, refs []InvoiceMarkingRecord) ([]invoiceTarget, error) {
	targets := make([]invoiceTarget, 0, len(refs))
	sorted := append([]InvoiceMarkingRecord(nil), refs...)
	slices.SortFunc(sorted, func(a, b InvoiceMarkingRecord) int {
		if a.Source != b.Source {
			return strings.Compare(a.Source, b.Source)
		}
		return a.ID - b.ID
	})
	for i := range sorted {
		record, err := getRechargeRecordWithDB(ctx, tx, sorted[i].Source, sorted[i].ID)
		if err != nil {
			if errors.Is(err, ErrRechargeRecordNotFound) {
				return nil, &InvoiceMarkingConflictError{Conflicts: []InvoiceMarkingConflict{{Source: sorted[i].Source, ID: sorted[i].ID, Reason: "not_markable"}}}
			}
			return nil, err
		}
		var invoice *RechargeRecordInvoice
		var stored RechargeRecordInvoice
		err = lockForUpdate(tx).Where("source_type = ? AND source_id = ?", sorted[i].Source, sorted[i].ID).First(&stored).Error
		switch {
		case err == nil:
			if stored.UserID != record.UserID {
				return nil, ErrInvoiceRecordMismatch
			}
			invoice = &stored
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return nil, err
		}
		targets = append(targets, invoiceTarget{
			record: record, invoice: invoice, expectedVersion: sorted[i].ExpectedVersion,
		})
	}
	return targets, nil
}

func loadInvoiceRequestReplay(ctx context.Context, requestID, hash, action string) (*InvoiceMarkingResult, error) {
	var stored RechargeRecordInvoiceRequest
	err := DB.WithContext(ctx).Where("request_id = ?", requestID).First(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if stored.RequestHash != hash || stored.Action != action {
		return nil, ErrInvoiceIdempotency
	}
	var rows []RechargeRecordInvoiceHistory
	if err := DB.WithContext(ctx).Where("request_id = ?", requestID).Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrInvoiceHistoryNotFound
	}
	result := &InvoiceMarkingResult{Action: action, Count: len(rows), Replayed: true, Records: make([]InvoiceMarkingResultRecord, 0, len(rows))}
	for i := range rows {
		var snapshot InvoiceSnapshot
		if err := common.UnmarshalJsonStr(rows[i].AfterSnapshot, &snapshot); err != nil {
			return nil, err
		}
		result.Records = append(result.Records, InvoiceMarkingResultRecord{
			Source: rows[i].SourceType, ID: rows[i].SourceID, Status: snapshot.Status,
			Version: snapshot.Version, InvoiceNo: snapshot.InvoiceNo, InvoiceDate: snapshot.InvoiceDate,
		})
	}
	return result, nil
}

func ListRechargeInvoiceHistory(ctx context.Context, source string, id int, before ...int) ([]InvoiceHistoryView, error) {
	var rows []RechargeRecordInvoiceHistory
	query := DB.WithContext(ctx).Where("source_type = ? AND source_id = ?", source, id)
	if len(before) > 0 && before[0] > 0 {
		query = query.Where("id < ?", before[0])
	}
	if err := query.Order("id desc").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]InvoiceHistoryView, 0, len(rows))
	for i := range rows {
		view := InvoiceHistoryView{
			Id: rows[i].Id, Source: rows[i].SourceType, SourceID: rows[i].SourceID,
			Action: rows[i].Action, RequestID: rows[i].RequestID,
			OperatorID: rows[i].OperatorID, OperatorName: rows[i].OperatorName,
			Reason: rows[i].Reason, CreatedAt: rows[i].CreatedAt,
		}
		if err := common.UnmarshalJsonStr(rows[i].BeforeSnapshot, &view.Before); err != nil {
			view.Before = InvoiceSnapshot{Status: InvoiceStatusUnmarked}
		}
		if err := common.UnmarshalJsonStr(rows[i].AfterSnapshot, &view.After); err != nil {
			view.After = InvoiceSnapshot{}
		}
		result = append(result, view)
	}
	return result, nil
}
