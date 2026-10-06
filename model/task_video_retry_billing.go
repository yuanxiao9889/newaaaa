package model

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// SetVideoTaskRetryQuota moves only the difference from the original task's
// reservation. The task claim, funding balance, token balance and durable quota
// are committed together, so an interrupted retry remains fully refundable.
func SetVideoTaskRetryQuota(ctx context.Context, task *Task, targetQuota int) error {
	if task == nil || task.ID <= 0 || task.PrivateData.VideoRetry == nil {
		return errors.New("video retry billing task is missing")
	}
	if targetQuota < 0 || targetQuota > common.MaxQuota {
		return errors.New("video retry quota is outside the supported range")
	}
	var delta int
	var token Token
	var wallet bool
	var userCacheReserved, tokenCacheReserved bool
	var nextRevision int64
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Task
		if err := lockForUpdate(tx).First(&current, task.ID).Error; err != nil {
			return err
		}
		retry := current.PrivateData.VideoRetry
		if retry == nil || !retry.Ready || !retry.Submitting || retry.Revision != task.PrivateData.VideoRetry.Revision ||
			current.Status == TaskStatusSuccess || current.Status == TaskStatusFailure ||
			current.UserId != task.UserId || current.GetTokenID() != task.GetTokenID() {
			return errors.New("video retry billing claim is no longer current")
		}
		if current.Quota < 0 || current.Quota > common.MaxQuota {
			return errors.New("video retry has an invalid existing reservation")
		}
		if current.PrivateData.BillingSource == "" && targetQuota > 0 {
			// A free initial job has not selected a funding source. Do not
			// silently turn it into a wallet charge on a different provider.
			return errors.New("video retry has no paid funding source")
		}
		delta = targetQuota - current.Quota
		var user User
		userQuery := tx
		if current.PrivateData.BillingSource != "subscription" {
			userQuery = lockForUpdate(tx)
		}
		if err := userQuery.First(&user, current.UserId).Error; err != nil {
			return err
		}
		if delta >= 0 && user.Status != common.UserStatusEnabled {
			return errors.New("video retry user is disabled")
		}
		if tokenID := current.GetTokenID(); tokenID > 0 {
			if err := lockForUpdate(tx).First(&token, tokenID).Error; err != nil {
				if delta >= 0 || !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				token = Token{}
			}
			if token.Id > 0 && (token.UserId != current.UserId || delta >= 0 && (token.Key == "" ||
				(token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusExhausted) ||
				(token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp()))) {
				return errors.New("video retry token is no longer usable")
			}
			if delta > 0 && !token.UnlimitedQuota && token.RemainQuota < delta {
				return errors.New("video retry token quota insufficient")
			}
			if delta > 0 && (token.UsedQuota > math.MaxInt-delta || token.RemainQuota < math.MinInt+delta) ||
				delta < 0 && (token.RemainQuota > math.MaxInt+delta || token.UsedQuota < math.MinInt-delta) {
				return errors.New("video retry token accounting would overflow")
			}
		}
		switch current.PrivateData.BillingSource {
		case "", "wallet":
			wallet = true
			if delta > 0 && user.Quota < delta {
				return errors.New("video retry wallet quota insufficient")
			}
			if delta < 0 && user.Quota > math.MaxInt+delta {
				return errors.New("video retry wallet refund would overflow")
			}
		case "subscription":
			var subscription UserSubscription
			if err := lockForUpdate(tx).First(&subscription, current.PrivateData.SubscriptionId).Error; err != nil {
				return err
			}
			if subscription.UserId != current.UserId || delta >= 0 &&
				(subscription.Status != "active" || subscription.EndTime <= common.GetTimestamp()) {
				return errors.New("video retry subscription is no longer active")
			}
			if subscription.AmountUsed < 0 || subscription.AmountTotal < 0 {
				return errors.New("video retry subscription accounting is invalid")
			}
			if delta > 0 && (subscription.AmountUsed > math.MaxInt64-int64(delta) ||
				(subscription.AmountTotal > 0 && subscription.AmountTotal-subscription.AmountUsed < int64(delta))) {
				return errors.New("video retry subscription quota insufficient")
			}
			if delta != 0 {
				if err := tx.Model(&subscription).Update("amount_used", max(subscription.AmountUsed+int64(delta), 0)).Error; err != nil {
					return err
				}
			}
		default:
			return errors.New("video retry funding source is unsupported")
		}
		nextRevision = retry.Revision
		if delta == 0 {
			return nil
		}
		// A live Redis balance can already include ordinary requests waiting
		// for batch persistence. Reserve there before using the DB balance.
		if common.RedisEnabled && delta > 0 {
			if wallet {
				result, err := cacheTryReserveUserQuota(current.UserId, int64(delta))
				if err != nil {
					return fmt.Errorf("video retry wallet cache reservation failed: %w", err)
				}
				if result == cacheQuotaInsufficient {
					return errors.New("video retry wallet quota insufficient")
				}
				userCacheReserved = result == cacheQuotaOK
			}
			if token.Id > 0 {
				var result cacheQuotaResult
				var err error
				if token.UnlimitedQuota {
					result, err = cacheApplyTokenQuotaDelta(token.Id, token.Key, -int64(delta))
				} else {
					result, err = cacheTryReserveTokenQuota(token.Id, token.Key, int64(delta))
				}
				if err != nil {
					return fmt.Errorf("video retry token cache reservation failed: %w", err)
				}
				if result == cacheQuotaInsufficient {
					return errors.New("video retry token quota insufficient")
				}
				tokenCacheReserved = result == cacheQuotaOK
			}
		}
		if wallet {
			query := tx.Model(&User{}).Where("id = ?", current.UserId)
			if delta > 0 {
				query = query.Where("quota >= ?", delta)
			}
			result := query.Update("quota", gorm.Expr("quota - ?", delta))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("video retry wallet quota insufficient")
			}
		}
		if token.Id > 0 {
			query := tx.Model(&Token{}).Where("id = ?", token.Id)
			if delta > 0 && !token.UnlimitedQuota {
				query = query.Where("remain_quota >= ?", delta)
			}
			result := query.Updates(map[string]any{
				"remain_quota":  gorm.Expr("remain_quota - ?", delta),
				"used_quota":    gorm.Expr("used_quota + ?", delta),
				"accessed_time": common.GetTimestamp(),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("video retry token quota insufficient")
			}
		}
		retry.Revision++
		nextRevision = retry.Revision
		current.PrivateData.PreConsumedQuota = targetQuota
		return tx.Model(&current).Updates(map[string]any{"quota": targetQuota, "private_data": current.PrivateData}).Error
	})
	if err != nil {
		if userCacheReserved {
			if result, cacheErr := cacheApplyUserQuotaDelta(task.UserId, int64(delta)); cacheErr != nil || result != cacheQuotaOK {
				common.SysError(fmt.Sprintf("video retry wallet cache compensation failed: task=%s result=%d error=%v", task.TaskID, result, cacheErr))
			}
		}
		if tokenCacheReserved {
			if result, cacheErr := cacheApplyTokenQuotaDelta(token.Id, token.Key, int64(delta)); cacheErr != nil || result != cacheQuotaOK {
				common.SysError(fmt.Sprintf("video retry token cache compensation failed: task=%s result=%d error=%v", task.TaskID, result, cacheErr))
			}
		}
		return err
	}
	if common.RedisEnabled && delta < 0 {
		if wallet {
			if _, err := cacheApplyUserQuotaDelta(task.UserId, -int64(delta)); err != nil {
				common.SysError(fmt.Sprintf("video retry wallet cache reconciliation failed: task=%s error=%v", task.TaskID, err))
			}
		}
		if token.Id > 0 {
			if _, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -int64(delta)); err != nil {
				common.SysError(fmt.Sprintf("video retry token cache reconciliation failed: task=%s error=%v", task.TaskID, err))
			}
		}
	}
	task.Quota = targetQuota
	task.PrivateData.PreConsumedQuota = targetQuota
	retry := *task.PrivateData.VideoRetry
	retry.Revision = nextRevision
	task.PrivateData.VideoRetry = &retry
	return nil
}
