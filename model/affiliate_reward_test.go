package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func insertAffiliateUser(t *testing.T, id int, username string, inviterId int) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:        id,
		Username:  username,
		Status:    common.UserStatusEnabled,
		AffCode:   fmt.Sprintf("aff_%d", id),
		InviterId: inviterId,
	}).Error)
}

func insertAffiliateTopUp(t *testing.T, tradeNo string, userId int, amount int64, money float64, provider string) {
	t.Helper()
	require.NoError(t, (&TopUp{
		UserId:          userId,
		Amount:          amount,
		Money:           money,
		TradeNo:         tradeNo,
		PaymentMethod:   provider,
		PaymentProvider: provider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}).Insert())
}

func getAffiliateUser(t *testing.T, id int) User {
	t.Helper()
	var user User
	require.NoError(t, DB.Where("id = ?", id).First(&user).Error)
	return user
}

func countAffiliateRewards(t *testing.T, inviteeId int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&AffiliateRewardRecord{}).Where("invitee_id = ?", inviteeId).Count(&count).Error)
	return count
}

func TestRechargeEpay_GrantsAffiliateRewardFromPaymentMoney(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertAffiliateUser(t, 1, "inviter", 0)
	insertAffiliateUser(t, 2, "invitee", 1)
	insertAffiliateTopUp(t, "epay-aff-1", 2, 20, 12.34, PaymentProviderEpay)

	require.NoError(t, RechargeEpay("epay-aff-1", "alipay", "127.0.0.1"))

	inviter := getAffiliateUser(t, 1)
	assert.Equal(t, 617, inviter.AffQuota)
	assert.Equal(t, 617, inviter.AffHistoryQuota)
	assert.Equal(t, int64(1), countAffiliateRewards(t, 2))
	assert.Equal(t, 20000, getAffiliateUser(t, 2).Quota)
}

func TestRechargeEpay_AffiliateRewardIsIdempotent(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertAffiliateUser(t, 1, "inviter", 0)
	insertAffiliateUser(t, 2, "invitee", 1)
	insertAffiliateTopUp(t, "epay-aff-idempotent", 2, 10, 10, PaymentProviderEpay)

	require.NoError(t, RechargeEpay("epay-aff-idempotent", "alipay", "127.0.0.1"))
	require.NoError(t, RechargeEpay("epay-aff-idempotent", "alipay", "127.0.0.1"))

	inviter := getAffiliateUser(t, 1)
	assert.Equal(t, 500, inviter.AffQuota)
	assert.Equal(t, 500, inviter.AffHistoryQuota)
	assert.Equal(t, int64(1), countAffiliateRewards(t, 2))
}

func TestRechargeEpay_AffiliateRewardLimitedToFirstThreeTopUpsPerInvitee(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertAffiliateUser(t, 1, "inviter", 0)
	insertAffiliateUser(t, 2, "invitee", 1)
	for i := 1; i <= 4; i++ {
		tradeNo := fmt.Sprintf("epay-aff-limit-%d", i)
		insertAffiliateTopUp(t, tradeNo, 2, int64(i), 10, PaymentProviderEpay)
		require.NoError(t, RechargeEpay(tradeNo, "alipay", "127.0.0.1"))
	}

	inviter := getAffiliateUser(t, 1)
	assert.Equal(t, 1500, inviter.AffQuota)
	assert.Equal(t, 1500, inviter.AffHistoryQuota)
	assert.Equal(t, int64(3), countAffiliateRewards(t, 2))
}

func TestRechargeEpay_AffiliateRewardSkippedWithoutInviterOrPositiveReward(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertAffiliateUser(t, 1, "inviter", 0)
	insertAffiliateUser(t, 2, "invitee", 1)
	insertAffiliateUser(t, 3, "solo", 0)
	insertAffiliateTopUp(t, "epay-aff-zero", 2, 1, 0.01, PaymentProviderEpay)
	insertAffiliateTopUp(t, "epay-aff-no-inviter", 3, 1, 10, PaymentProviderEpay)

	require.NoError(t, RechargeEpay("epay-aff-zero", "alipay", "127.0.0.1"))
	require.NoError(t, RechargeEpay("epay-aff-no-inviter", "alipay", "127.0.0.1"))

	inviter := getAffiliateUser(t, 1)
	assert.Equal(t, 0, inviter.AffQuota)
	assert.Equal(t, 0, inviter.AffHistoryQuota)
	assert.Equal(t, int64(0), countAffiliateRewards(t, 2))
	assert.Equal(t, int64(0), countAffiliateRewards(t, 3))
}

func TestWalletRechargePaths_GrantAffiliateRewardFromPaymentMoney(t *testing.T) {
	testCases := []struct {
		name     string
		tradeNo  string
		provider string
		complete func(tradeNo string) error
	}{
		{
			name:     "stripe",
			tradeNo:  "stripe-aff-path",
			provider: PaymentProviderStripe,
			complete: func(tradeNo string) error {
				return Recharge(tradeNo, "cus_aff_path", "127.0.0.1")
			},
		},
		{
			name:     "creem",
			tradeNo:  "creem-aff-path",
			provider: PaymentProviderCreem,
			complete: func(tradeNo string) error {
				return RechargeCreem(tradeNo, "invitee@example.com", "Invitee", "127.0.0.1")
			},
		},
		{
			name:     "waffo",
			tradeNo:  "waffo-aff-path",
			provider: PaymentProviderWaffo,
			complete: func(tradeNo string) error {
				return RechargeWaffo(tradeNo, "127.0.0.1")
			},
		},
		{
			name:     "waffo pancake",
			tradeNo:  "waffo-pancake-aff-path",
			provider: PaymentProviderWaffoPancake,
			complete: func(tradeNo string) error {
				return RechargeWaffoPancake(tradeNo)
			},
		},
		{
			name:     "manual complete",
			tradeNo:  "manual-aff-path",
			provider: PaymentProviderEpay,
			complete: func(tradeNo string) error {
				return ManualCompleteTopUp(tradeNo, "127.0.0.1")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			originalQuotaPerUnit := common.QuotaPerUnit
			common.QuotaPerUnit = 1000
			t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

			insertAffiliateUser(t, 1, "inviter", 0)
			insertAffiliateUser(t, 2, "invitee", 1)
			insertAffiliateTopUp(t, tc.tradeNo, 2, 20, 12.34, tc.provider)

			require.NoError(t, tc.complete(tc.tradeNo))

			inviter := getAffiliateUser(t, 1)
			assert.Equal(t, 617, inviter.AffQuota)
			assert.Equal(t, 617, inviter.AffHistoryQuota)
			assert.Equal(t, int64(1), countAffiliateRewards(t, 2))
		})
	}
}

func TestUserInsert_RecordsInviteCountWithoutFixedInviterReward(t *testing.T) {
	truncateTables(t)
	originalQuotaForInviter := common.QuotaForInviter
	originalQuotaForInvitee := common.QuotaForInvitee
	originalConfirmed := operation_setting.GetPaymentSetting().ComplianceConfirmed
	originalTermsVersion := operation_setting.GetPaymentSetting().ComplianceTermsVersion
	common.QuotaForInviter = 999
	common.QuotaForInvitee = 0
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		common.QuotaForInviter = originalQuotaForInviter
		common.QuotaForInvitee = originalQuotaForInvitee
		operation_setting.GetPaymentSetting().ComplianceConfirmed = originalConfirmed
		operation_setting.GetPaymentSetting().ComplianceTermsVersion = originalTermsVersion
	})

	insertAffiliateUser(t, 1, "inviter", 0)
	user := &User{
		Username:  "new_invitee",
		Password:  "password123",
		InviterId: 1,
	}
	require.NoError(t, user.Insert(1))

	inviter := getAffiliateUser(t, 1)
	assert.Equal(t, 1, inviter.AffCount)
	assert.Equal(t, 0, inviter.AffQuota)
	assert.Equal(t, 0, inviter.AffHistoryQuota)
}

func TestCompleteSubscriptionOrder_DoesNotGrantAffiliateReward(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	insertAffiliateUser(t, 1, "inviter", 0)
	insertAffiliateUser(t, 2, "invitee", 1)
	order := &SubscriptionOrder{
		UserId:          2,
		Money:           20,
		TradeNo:         "sub-aff-no-reward",
		PaymentMethod:   PaymentProviderStripe,
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusSuccess,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return upsertSubscriptionTopUpTx(tx, order)
	}))

	inviter := getAffiliateUser(t, 1)
	assert.Equal(t, 0, inviter.AffQuota)
	assert.Equal(t, 0, inviter.AffHistoryQuota)
	assert.Equal(t, int64(0), countAffiliateRewards(t, 2))
}
