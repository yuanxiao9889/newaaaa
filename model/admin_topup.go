package model

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	adminQuotaTopUpBackfillOptionKey = "_internal.admin_quota_topup_backfill_v1"
	adminQuotaTopUpBackfillBatchSize = 500
)

func adminQuotaAmounts(quota int) (int64, float64) {
	quotaDecimal := decimal.NewFromInt(int64(quota))
	quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	if quotaPerUnit.LessThanOrEqual(decimal.Zero) {
		return 0, 0
	}

	usdAmount := quotaDecimal.Div(quotaPerUnit)
	amount := usdAmount.Round(0).IntPart()
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return amount, float64(quota)
	}

	rate := operation_setting.GetUsdToCurrencyRate(operation_setting.USDExchangeRate)
	return amount, usdAmount.Mul(decimal.NewFromFloat(rate)).InexactFloat64()
}

// AddAdminQuota credits quota and creates the corresponding successful billing
// record in one database transaction.
func AddAdminQuota(userId int, quota int) (*TopUp, error) {
	if userId <= 0 || quota <= 0 {
		return nil, errors.New("invalid admin quota top-up")
	}

	now := common.GetTimestamp()
	amount, money := adminQuotaAmounts(quota)
	topUp := &TopUp{
		UserId:          userId,
		Amount:          amount,
		QuotaAmount:     quota,
		Money:           money,
		TradeNo:         fmt.Sprintf("ADMUSR%dNO%s%d", userId, common.GetRandomString(6), time.Now().UnixNano()),
		PaymentMethod:   PaymentMethodOfficialWebsite,
		PaymentProvider: PaymentProviderAdmin,
		CreateTime:      now,
		CompleteTime:    now,
		Status:          common.TopUpStatusSuccess,
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&User{}).Where("id = ?", userId).Update("quota", gorm.Expr("quota + ?", quota))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(topUp).Error
	})
	if err != nil {
		return nil, err
	}

	gopool.Go(func() {
		if err := cacheIncrUserQuota(userId, int64(quota)); err != nil {
			common.SysLog("failed to increase user quota cache after admin top-up: " + err.Error())
		}
	})
	return topUp, nil
}

type adminQuotaAuditEnvelope struct {
	Op struct {
		Action string `json:"action"`
		Params struct {
			TargetUserId int    `json:"target_user_id"`
			Quota        string `json:"quota"`
			QuotaValue   int    `json:"quota_value"`
		} `json:"params"`
	} `json:"op"`
}

func parseAdminQuotaText(value string) int {
	value = strings.TrimSpace(value)
	for _, marker := range []string{"增加用户额度", "Increased user quota by"} {
		if index := strings.Index(value, marker); index >= 0 {
			value = strings.TrimSpace(value[index+len(marker):])
			break
		}
	}
	for _, suffix := range []string{"额度", "quota"} {
		value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
	}

	if strings.HasSuffix(value, "点") {
		quota, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(value, "点")), 10, 32)
		if err != nil || quota <= 0 {
			return 0
		}
		return int(quota)
	}

	rate := 1.0
	switch {
	case strings.HasPrefix(value, "＄"):
		value = strings.TrimSpace(strings.TrimPrefix(value, "＄"))
	case strings.HasPrefix(value, "$"):
		value = strings.TrimSpace(strings.TrimPrefix(value, "$"))
	case strings.HasPrefix(value, "¥"):
		value = strings.TrimSpace(strings.TrimPrefix(value, "¥"))
		rate = operation_setting.USDExchangeRate
	default:
		symbol := operation_setting.GetGeneralSetting().CustomCurrencySymbol
		if symbol == "" || !strings.HasPrefix(value, symbol) {
			return 0
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, symbol))
		rate = operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate
	}
	if rate <= 0 || common.QuotaPerUnit <= 0 {
		return 0
	}

	amount, err := decimal.NewFromString(value)
	if err != nil || !amount.IsPositive() {
		return 0
	}
	quota, clamp := common.QuotaFromDecimalChecked(amount.
		Div(decimal.NewFromFloat(rate)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
	if clamp != nil || quota <= 0 {
		return 0
	}
	return quota
}

func adminQuotaTopUpFromLog(logEntry *Log) *TopUp {
	if logEntry == nil {
		return nil
	}

	userId := logEntry.UserId
	quota := 0
	if logEntry.Other != "" {
		var envelope adminQuotaAuditEnvelope
		if err := common.UnmarshalJsonStr(logEntry.Other, &envelope); err == nil && envelope.Op.Action == "user.quota_add" {
			if envelope.Op.Params.TargetUserId > 0 {
				userId = envelope.Op.Params.TargetUserId
			}
			quota = envelope.Op.Params.QuotaValue
			if quota <= 0 {
				quota = parseAdminQuotaText(envelope.Op.Params.Quota)
			}
		}
	}
	if quota <= 0 && ((strings.HasPrefix(logEntry.Content, "管理员") && strings.Contains(logEntry.Content, "增加用户额度")) || strings.HasPrefix(logEntry.Content, "Increased user quota by")) {
		quota = parseAdminQuotaText(logEntry.Content)
	}
	if userId <= 0 || quota <= 0 {
		return nil
	}

	amount, money := adminQuotaAmounts(quota)
	tradeSource := fmt.Sprintf("%d|%d|%d|%s|%s|%s", logEntry.Id, userId, logEntry.CreatedAt, logEntry.RequestId, logEntry.Content, logEntry.Other)
	tradeHash := hex.EncodeToString(common.Sha256Raw([]byte(tradeSource)))
	tradeNo := "ADMLOG" + tradeHash[:24]
	return &TopUp{
		UserId:          userId,
		Amount:          amount,
		QuotaAmount:     quota,
		Money:           money,
		TradeNo:         tradeNo,
		PaymentMethod:   PaymentMethodOfficialWebsite,
		PaymentProvider: PaymentProviderAdmin,
		CreateTime:      logEntry.CreatedAt,
		CompleteTime:    logEntry.CreatedAt,
		Status:          common.TopUpStatusSuccess,
	}
}

// BackfillAdminQuotaTopUps imports historical administrator quota increases
// from management audit logs. The migration is idempotent and runs once.
func BackfillAdminQuotaTopUps() (int64, error) {
	var marker Option
	err := DB.Where(commonKeyCol+" = ?", adminQuotaTopUpBackfillOptionKey).Take(&marker).Error
	if err == nil {
		return 0, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}

	var inserted int64
	for offset := 0; ; offset += adminQuotaTopUpBackfillBatchSize {
		var logs []*Log
		err = LOG_DB.Model(&Log{}).
			Select("id", "user_id", "created_at", "content", "other", "request_id").
			Where("type = ? AND (content LIKE ? OR content LIKE ?)", LogTypeManage, "管理员%增加用户额度%", "Increased user quota by%").
			Order("created_at asc, request_id asc, id asc").
			Limit(adminQuotaTopUpBackfillBatchSize).
			Offset(offset).
			Find(&logs).Error
		if err != nil {
			return inserted, err
		}
		if len(logs) == 0 {
			break
		}

		for _, logEntry := range logs {
			topUp := adminQuotaTopUpFromLog(logEntry)
			if topUp == nil {
				continue
			}
			result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(topUp)
			if result.Error != nil {
				return inserted, result.Error
			}
			inserted += result.RowsAffected
		}
		if len(logs) < adminQuotaTopUpBackfillBatchSize {
			break
		}
	}

	marker = Option{Key: adminQuotaTopUpBackfillOptionKey, Value: strconv.FormatInt(common.GetTimestamp(), 10)}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; err != nil {
		return inserted, err
	}
	return inserted, nil
}
