package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	RechargeSourceTopUp      = "topup"
	RechargeSourceRedemption = "redemption"
	RechargeSourceHistorical = "historical"

	RechargeKindOnline     = "online"
	RechargeKindAdmin      = "admin"
	RechargeKindRedemption = "redemption"

	RechargeStatusUsed = "used"

	InvoiceStatusUnmarked = "unmarked"
	InvoiceStatusMarked   = "marked"
	InvoiceStatusRevoked  = "revoked"

	RechargeRecordDefaultDays = 30
	RechargeRecordMaxDays     = 366
	RechargeRecordMaxPageSize = 100
)

var (
	ErrInvalidRechargeRecordFilter = errors.New("invalid recharge record filter")
	ErrRechargeRecordNotFound      = errors.New("recharge record not found")
)

// RechargeRecord is the read model for a business recharge record. It is built
// from TopUp or Redemption rows and is never used as an accounting source.
type RechargeRecord struct {
	Source         string   `json:"source"`
	SourceID       int      `json:"source_id"`
	UserID         int      `json:"user_id"`
	Username       string   `json:"username"`
	SourceKind     string   `json:"source_kind"`
	RecordNo       string   `json:"record_no"`
	RechargeTime   int64    `json:"recharge_time"`
	CreditedQuota  int      `json:"credited_quota"`
	PaidAmount     *float64 `json:"paid_amount"`
	Currency       string   `json:"currency"`
	PaidConfirmed  bool     `json:"paid_amount_confirmed"`
	AmountSource   string   `json:"amount_source"`
	Status         string   `json:"status"`
	InvoiceStatus  string   `json:"invoice_status"`
	InvoiceNo      string   `json:"invoice_no,omitempty"`
	InvoiceDate    string   `json:"invoice_date,omitempty"`
	MarkedAt       int64    `json:"marked_at,omitempty"`
	InvoiceVersion int      `json:"invoice_version"`
	CanMark        bool     `json:"can_mark"`
	RequiresVerify bool     `json:"requires_receipt_verification"`
	InternalNote   string   `json:"internal_note,omitempty"`
	Verification   string   `json:"verification_note,omitempty"`
	MarkedByName   string   `json:"marked_by_name,omitempty"`
}

// RechargeRecordFilter is validated by the controller before it reaches the
// model. The model still applies hard limits so other callers cannot page past
// the API contract.
type RechargeRecordFilter struct {
	UserID        int
	Keyword       string
	SourceKind    string
	Status        string
	InvoiceStatus string
	StartTime     int64
	EndTime       int64
	Page          int
	PageSize      int
	exactSource   string
	exactID       int
}

func (record *RechargeRecord) IsMarkable() bool {
	if record == nil {
		return false
	}
	return record.Status == common.TopUpStatusSuccess || record.Status == RechargeStatusUsed
}

func (record *RechargeRecord) RequiresReceiptVerification() bool {
	if record == nil {
		return true
	}
	return record.SourceKind != RechargeKindOnline || !record.PaidConfirmed
}

type rechargeRecordRow struct {
	Source          string          `gorm:"column:source_type"`
	SourceID        int             `gorm:"column:source_id"`
	UserID          int             `gorm:"column:user_id"`
	Username        string          `gorm:"column:username"`
	SourceKind      string          `gorm:"column:source_kind"`
	RecordNo        string          `gorm:"column:record_no"`
	RechargeTime    int64           `gorm:"column:recharge_time"`
	Amount          int64           `gorm:"column:amount"`
	QuotaAmount     int             `gorm:"column:quota_amount"`
	PaymentProvider string          `gorm:"column:payment_provider"`
	Money           float64         `gorm:"column:money"`
	PaidAmount      sql.NullFloat64 `gorm:"column:paid_amount"`
	Currency        string          `gorm:"column:currency"`
	Status          string          `gorm:"column:source_status"`
	InvoiceID       int             `gorm:"column:invoice_id"`
	InvoiceStatus   string          `gorm:"column:invoice_status"`
	InvoiceNo       string          `gorm:"column:invoice_no"`
	InvoiceDate     string          `gorm:"column:invoice_date"`
	MarkedAt        int64           `gorm:"column:marked_at"`
	InvoiceVersion  int             `gorm:"column:invoice_version"`
	InternalNote    string          `gorm:"column:internal_note"`
	Verification    string          `gorm:"column:verification_note"`
	MarkedByName    string          `gorm:"column:marked_by_name"`
}

// Push date and owner predicates into each source branch. In particular, do
// not filter a CASE expression over completed and uncompleted order dates.
func rechargeRecordFromSQL(filter RechargeRecordFilter) (string, []any) {
	branches := make([]string, 0, 4)
	args := make([]any, 0, 16)
	topUpRecordNo, invoiceRecordNo, historicalRecordNo := "t.trade_no", "d.source_record_no", "h.source_key"
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		// Imported payment tables and newly migrated tables can have different
		// collations. Normalize projected identifiers without altering their
		// stored values or the indexed predicates. This collation supports 5.7.
		topUpRecordNo = "CONVERT(t.trade_no USING utf8mb4) COLLATE utf8mb4_unicode_ci"
		invoiceRecordNo = "CONVERT(d.source_record_no USING utf8mb4) COLLATE utf8mb4_unicode_ci"
		historicalRecordNo = "CONVERT(h.source_key USING utf8mb4) COLLATE utf8mb4_unicode_ci"
	}
	for _, completed := range []bool{true, false} {
		dateCol, state := "t.complete_time", "t.complete_time > 0"
		if !completed {
			dateCol, state = "t.create_time", "(t.complete_time IS NULL OR t.complete_time <= 0)"
		}
		branch := `SELECT 'topup' AS source_type, t.id AS source_id, t.user_id,
   COALESCE(u.username, '') AS username,
   CASE WHEN t.payment_provider = 'admin' THEN 'admin' ELSE 'online' END AS source_kind,
   ` + topUpRecordNo + ` AS record_no, ` + dateCol + ` AS recharge_time,
   t.amount, t.quota_amount, t.payment_provider, t.money,
   CASE WHEN t.status = 'success' AND t.payment_provider = 'epay' AND t.money > 0 THEN t.money ELSE NULL END AS paid_amount,
   CASE WHEN t.payment_provider = 'epay' THEN 'CNY' ELSE '' END AS currency,
   t.status AS source_status
   FROM top_ups t LEFT JOIN users u ON u.id = t.user_id
   WHERE t.status = 'success' AND ` + state + ` AND NOT EXISTS (SELECT 1 FROM subscription_orders so WHERE so.trade_no = t.trade_no)`
		if filter.exactSource != "" {
			branch += " AND t.id = ?"
			args = append(args, filter.exactID)
		} else {
			branch += " AND " + dateCol + " >= ? AND " + dateCol + " <= ?"
			args = append(args, filter.StartTime, filter.EndTime)
		}
		if filter.UserID > 0 {
			branch += " AND t.user_id = ?"
			args = append(args, filter.UserID)
		}
		branches = append(branches, branch)
	}
	// Soft deletion is inventory cleanup, not reversal of a completed recharge.
	// Once marked, the immutable business snapshot remains authoritative even if
	// an old version of the application reassigns or deletes the original code.
	for _, snapshot := range []bool{false, true} {
		dateCol, ownerCol, idCol := "d.redeemed_time", "d.used_user_id", "d.id"
		branch := `SELECT 'redemption' AS source_type, d.id AS source_id, d.used_user_id AS user_id,
   COALESCE(u.username, '') AS username, 'redemption' AS source_kind, '' AS record_no,
   d.redeemed_time AS recharge_time, 0 AS amount, d.quota AS quota_amount,
   '' AS payment_provider, 0 AS money, NULL AS paid_amount, '' AS currency, 'used' AS source_status
   FROM redemptions d LEFT JOIN users u ON u.id = d.used_user_id
   WHERE d.used_user_id > 0 AND d.redeemed_time > 0
   AND NOT EXISTS (SELECT 1 FROM recharge_record_invoices m WHERE m.source_type = 'redemption' AND m.source_id = d.id)`
		if snapshot {
			dateCol, ownerCol, idCol = "d.source_time", "d.user_id", "d.source_id"
			branch = `SELECT 'redemption' AS source_type, d.source_id, d.user_id,
    COALESCE(u.username, '') AS username, 'redemption' AS source_kind, ` + invoiceRecordNo + ` AS record_no,
    d.source_time AS recharge_time, 0 AS amount, d.source_quota AS quota_amount,
    '' AS payment_provider, 0 AS money, NULL AS paid_amount, '' AS currency, 'used' AS source_status
    FROM recharge_record_invoices d LEFT JOIN users u ON u.id = d.user_id
    WHERE d.source_type = 'redemption'`
		}
		if filter.exactSource != "" {
			branch += " AND " + idCol + " = ?"
			args = append(args, filter.exactID)
		} else {
			branch += " AND " + dateCol + " >= ? AND " + dateCol + " <= ?"
			args = append(args, filter.StartTime, filter.EndTime)
		}
		if filter.UserID > 0 {
			branch += " AND " + ownerCol + " = ?"
			args = append(args, filter.UserID)
		}
		branches = append(branches, branch)
	}
	historical := `SELECT 'historical' AS source_type, h.id AS source_id, h.user_id,
 COALESCE(u.username, '') AS username, 'admin' AS source_kind, ` + historicalRecordNo + ` AS record_no,
 h.occurred_at AS recharge_time, 0 AS amount, h.quota AS quota_amount,
 'admin' AS payment_provider, 0 AS money, NULL AS paid_amount, '' AS currency, 'success' AS source_status
 FROM historical_recharges h LEFT JOIN users u ON u.id = h.user_id WHERE h.status = 'confirmed'`
	if filter.exactSource != "" {
		historical += " AND h.id = ?"
		args = append(args, filter.exactID)
	} else {
		historical += " AND h.occurred_at >= ? AND h.occurred_at <= ?"
		args = append(args, filter.StartTime, filter.EndTime)
	}
	if filter.UserID > 0 {
		historical += " AND h.user_id = ?"
		args = append(args, filter.UserID)
	}
	branches = append(branches, historical)
	return "FROM (" + strings.Join(branches, " UNION ALL ") + `) r
 LEFT JOIN recharge_record_invoices i ON i.source_type = r.source_type AND i.source_id = r.source_id AND i.user_id = r.user_id`, args
}

func rechargeRecordSelectSQL(from string) string {
	return `
SELECT
	r.source_type,
	r.source_id,
	r.user_id,
	r.username,
	r.source_kind,
	r.record_no,
	r.recharge_time,
	r.amount,
	r.quota_amount,
 r.payment_provider,
 r.money,
	r.paid_amount,
	r.currency,
	r.source_status,
	COALESCE(i.id, 0) AS invoice_id,
	COALESCE(i.status, '') AS invoice_status,
	COALESCE(i.invoice_no, '') AS invoice_no,
	COALESCE(i.invoice_date, '') AS invoice_date,
	COALESCE(i.marked_at, 0) AS marked_at,
	COALESCE(i.version, 0) AS invoice_version,
	COALESCE(i.internal_note, '') AS internal_note,
	COALESCE(i.verification_note, '') AS verification_note,
	COALESCE(i.marked_by_name, '') AS marked_by_name
` + from
}

func normalizeRechargeRecordFilter(filter RechargeRecordFilter) RechargeRecordFilter {
	now := common.GetTimestamp()
	if filter.EndTime <= 0 {
		filter.EndTime = now
	}
	if filter.StartTime <= 0 {
		filter.StartTime = filter.EndTime - RechargeRecordDefaultDays*24*60*60
	}
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 20
	}
	if filter.PageSize > RechargeRecordMaxPageSize {
		filter.PageSize = RechargeRecordMaxPageSize
	}
	return filter
}

func ListRechargeRecords(ctx context.Context, filter RechargeRecordFilter) ([]*RechargeRecord, int64, error) {
	return listRechargeRecordsWithDB(ctx, DB, filter)
}

func listRechargeRecordsWithDB(ctx context.Context, db *gorm.DB, filter RechargeRecordFilter) ([]*RechargeRecord, int64, error) {
	filter = normalizeRechargeRecordFilter(filter)
	if filter.exactSource == "" && (filter.EndTime < filter.StartTime || filter.EndTime-filter.StartTime > RechargeRecordMaxDays*24*60*60) {
		return nil, 0, ErrInvalidRechargeRecordFilter
	}

	from, args := rechargeRecordFromSQL(filter)
	where := []string{"1 = 1"}
	if filter.UserID > 0 {
		where = append(where, "r.user_id = ?")
		args = append(args, filter.UserID)
	}
	if filter.exactSource != "" {
		where = append(where, "r.source_type = ?", "r.source_id = ?")
		args = append(args, filter.exactSource, filter.exactID)
	}
	if filter.SourceKind != "" {
		where = append(where, "r.source_kind = ?")
		args = append(args, filter.SourceKind)
	}
	if filter.Status != "" {
		where = append(where, "r.source_status = ?")
		args = append(args, filter.Status)
	}
	switch filter.InvoiceStatus {
	case InvoiceStatusUnmarked:
		where = append(where, "i.id IS NULL")
	case InvoiceStatusMarked, InvoiceStatusRevoked:
		where = append(where, "i.status = ?")
		args = append(args, filter.InvoiceStatus)
	}
	keyword := strings.TrimSpace(filter.Keyword)
	if keyword != "" {
		pattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, ErrInvalidRechargeRecordFilter
		}
		like := "%" + pattern + "%"
		redemptionID := 0
		if raw, ok := strings.CutPrefix(strings.ToUpper(keyword), "RDM-"); ok {
			redemptionID, _ = strconv.Atoi(raw)
		}
		if redemptionID <= 0 {
			redemptionID, _ = strconv.Atoi(keyword)
		}
		if redemptionID > 0 {
			where = append(where, "(r.record_no LIKE ? ESCAPE '!' OR r.username LIKE ? ESCAPE '!' OR (r.source_type = ? AND r.source_id = ?))")
			args = append(args, like, like, RechargeSourceRedemption, redemptionID)
		} else {
			where = append(where, "(r.record_no LIKE ? ESCAPE '!' OR r.username LIKE ? ESCAPE '!')")
			args = append(args, like, like)
		}
	}

	countSQL := "SELECT COUNT(*) " + from + " WHERE " + strings.Join(where, " AND ")
	var total int64
	if err := db.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	querySQL := rechargeRecordSelectSQL(from) + " WHERE " + strings.Join(where, " AND ") +
		" ORDER BY r.recharge_time DESC, r.source_type ASC, r.source_id DESC LIMIT ? OFFSET ?"
	queryArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	var rows []rechargeRecordRow
	if err := db.WithContext(ctx).Raw(querySQL, queryArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	records := make([]*RechargeRecord, 0, len(rows))
	for i := range rows {
		records = append(records, rows[i].toRechargeRecord())
	}
	return records, total, nil
}

func GetRechargeRecord(ctx context.Context, source string, id int) (*RechargeRecord, error) {
	return getRechargeRecordWithDB(ctx, DB, source, id)
}

func getRechargeRecordWithDB(ctx context.Context, db *gorm.DB, source string, id int) (*RechargeRecord, error) {
	if id <= 0 {
		return nil, ErrRechargeRecordNotFound
	}
	records, _, err := listRechargeRecordsWithDB(ctx, db, RechargeRecordFilter{
		Page:        1,
		PageSize:    1,
		exactSource: source,
		exactID:     id,
	})
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrRechargeRecordNotFound
	}
	return records[0], nil
}

func (row *rechargeRecordRow) toRechargeRecord() *RechargeRecord {
	record := &RechargeRecord{
		Source:         row.Source,
		SourceID:       row.SourceID,
		UserID:         row.UserID,
		Username:       row.Username,
		SourceKind:     row.SourceKind,
		RecordNo:       row.RecordNo,
		RechargeTime:   row.RechargeTime,
		Status:         row.Status,
		InvoiceStatus:  row.InvoiceStatus,
		InvoiceNo:      row.InvoiceNo,
		InvoiceDate:    row.InvoiceDate,
		MarkedAt:       row.MarkedAt,
		InvoiceVersion: row.InvoiceVersion,
		InternalNote:   row.InternalNote,
		Verification:   row.Verification,
		MarkedByName:   row.MarkedByName,
		Currency:       row.Currency,
	}
	if record.InvoiceStatus == "" {
		record.InvoiceStatus = InvoiceStatusUnmarked
	}
	if record.Source == RechargeSourceRedemption {
		record.RecordNo = fmt.Sprintf("RDM-%d", record.SourceID)
	}
	if row.PaidAmount.Valid {
		value := row.PaidAmount.Float64
		record.PaidAmount = &value
	}
	record.PaidConfirmed = record.SourceKind == RechargeKindOnline &&
		record.Status == common.TopUpStatusSuccess &&
		record.PaidAmount != nil &&
		record.Currency != ""
	switch {
	case record.PaidConfirmed:
		record.AmountSource = "payment"
	case record.SourceKind == RechargeKindAdmin:
		record.AmountSource = "quota_conversion"
	default:
		record.AmountSource = "none"
	}
	record.CanMark = record.IsMarkable()
	record.RequiresVerify = record.RequiresReceiptVerification()
	if record.InvoiceStatus == InvoiceStatusRevoked {
		record.InvoiceNo = ""
		record.InvoiceDate = ""
	}
	record.CreditedQuota = row.QuotaAmount
	if record.Status != common.TopUpStatusSuccess && record.Source == RechargeSourceTopUp {
		record.CreditedQuota = 0
	} else if record.CreditedQuota <= 0 && record.Source == RechargeSourceTopUp && record.SourceKind != RechargeKindAdmin {
		// Match the original settlement units, not the displayed currency.
		quota := decimal.NewFromInt(row.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		switch row.PaymentProvider {
		case PaymentProviderCreem:
			quota = decimal.NewFromInt(row.Amount)
		case PaymentProviderStripe:
			quota = decimal.NewFromFloat(row.Money).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		}
		if quota.IsPositive() {
			if value, err := common.WalletQuotaFromDecimalStrict(quota); err == nil {
				record.CreditedQuota = value
			}
		}
	}

	return record
}

// RechargeRecordWindowStart returns the default inclusive start timestamp for
// admin queries. It is exported for controller validation.
func RechargeRecordWindowStart(end int64) int64 {
	if end <= 0 {
		end = common.GetTimestamp()
	}
	return end - RechargeRecordDefaultDays*24*60*60
}

// ValidateRechargeRecordWindow enforces the API's default and maximum range.
func ValidateRechargeRecordWindow(start, end int64) (int64, int64, error) {
	now := common.GetTimestamp()
	if end <= 0 {
		end = now
	}
	if end > now {
		end = now
	}
	if start <= 0 {
		start = RechargeRecordWindowStart(end)
	}
	if start > end {
		return 0, 0, ErrInvalidRechargeRecordFilter
	}
	if end-start > RechargeRecordMaxDays*24*60*60 {
		return 0, 0, ErrInvalidRechargeRecordFilter
	}
	return start, end, nil
}

// ParseRechargeRecordDate validates an administrator-marked invoice date.
func ParseRechargeRecordDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil || parsed.Format("2006-01-02") != value {
		return "", errors.New("invoice date must use YYYY-MM-DD")
	}
	if parsed.After(time.Now()) {
		return "", errors.New("invoice date cannot be in the future")
	}
	return value, nil
}
