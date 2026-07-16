package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetQuotaWarningMemoryStateForTest() {
	quotaWarningMemoryState.Range(func(key, _ any) bool {
		quotaWarningMemoryState.Delete(key)
		return true
	})
}

func TestQuotaWarningStageBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		remaining int64
		threshold int64
		want      quotaWarningStage
	}{
		{name: "disabled threshold", remaining: 0, threshold: 0, want: quotaWarningStageNone},
		{name: "at threshold", remaining: 100, threshold: 100, want: quotaWarningStageNone},
		{name: "below threshold", remaining: 99, threshold: 100, want: quotaWarningStageThreshold},
		{name: "at half", remaining: 50, threshold: 100, want: quotaWarningStageThreshold},
		{name: "below half", remaining: 49, threshold: 100, want: quotaWarningStageHalf},
		{name: "at twenty percent", remaining: 20, threshold: 100, want: quotaWarningStageHalf},
		{name: "below twenty percent", remaining: 19, threshold: 100, want: quotaWarningStageCritical},
		{name: "zero balance", remaining: 0, threshold: 100, want: quotaWarningStageCritical},
		{name: "negative balance", remaining: -1, threshold: 100, want: quotaWarningStageCritical},
		{name: "rounded half boundary", remaining: 3, threshold: 7, want: quotaWarningStageHalf},
		{name: "rounded critical boundary", remaining: 1, threshold: 7, want: quotaWarningStageCritical},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, calculateQuotaWarningStage(tt.remaining, tt.threshold))
		})
	}
}

func TestQuotaWarningMemoryStageOncePerCycle(t *testing.T) {
	resetQuotaWarningMemoryStateForTest()
	t.Cleanup(resetQuotaWarningMemoryStateForTest)

	key := quotaWarningStateKey(7, BillingSourceWallet, 0)

	claimed, err := claimMemoryQuotaWarningStage(key, 100, quotaWarningStageThreshold)
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageThreshold)
	require.NoError(t, err)
	assert.False(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageHalf)
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageHalf)
	require.NoError(t, err)
	assert.False(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageCritical)
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageCritical)
	require.NoError(t, err)
	assert.False(t, claimed)

	resetMemoryQuotaWarningStage(key)
	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageThreshold)
	require.NoError(t, err)
	assert.True(t, claimed)
}

func TestQuotaWarningMemoryDirectCriticalDoesNotBackfillEarlierStages(t *testing.T) {
	resetQuotaWarningMemoryStateForTest()
	t.Cleanup(resetQuotaWarningMemoryStateForTest)

	key := quotaWarningStateKey(8, BillingSourceWallet, 0)
	claimed, err := claimMemoryQuotaWarningStage(key, 100, quotaWarningStageCritical)
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageThreshold)
	require.NoError(t, err)
	assert.False(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageHalf)
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestQuotaWarningMemoryThresholdChangeStartsNewCycle(t *testing.T) {
	resetQuotaWarningMemoryStateForTest()
	t.Cleanup(resetQuotaWarningMemoryStateForTest)

	key := quotaWarningStateKey(9, BillingSourceWallet, 0)
	claimed, err := claimMemoryQuotaWarningStage(key, 100, quotaWarningStageHalf)
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 200, quotaWarningStageHalf)
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = claimMemoryQuotaWarningStage(key, 200, quotaWarningStageHalf)
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestQuotaWarningStateKeysIsolateWalletAndSubscriptions(t *testing.T) {
	walletKey := quotaWarningStateKey(10, BillingSourceWallet, 0)
	subscriptionKey := quotaWarningStateKey(10, BillingSourceSubscription, 42)
	otherSubscriptionKey := quotaWarningStateKey(10, BillingSourceSubscription, 43)

	assert.NotEqual(t, walletKey, subscriptionKey)
	assert.NotEqual(t, subscriptionKey, otherSubscriptionKey)
	assert.NotEqual(t, walletKey, otherSubscriptionKey)
}

func TestNewQuotaWarningNotificationUsesStageAndPostConsumptionBalance(t *testing.T) {
	generalSetting := operation_setting.GetGeneralSetting()
	originalDisplayType := generalSetting.QuotaDisplayType
	originalExchangeRate := operation_setting.USDExchangeRate
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		generalSetting.QuotaDisplayType = originalDisplayType
		operation_setting.USDExchangeRate = originalExchangeRate
		common.QuotaPerUnit = originalQuotaPerUnit
	})

	generalSetting.QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	operation_setting.USDExchangeRate = 7
	common.QuotaPerUnit = 500000

	const topUpLink = "https://example.com/console/topup"
	email := newQuotaWarningNotification(
		dto.NotifyTypeEmail,
		"您的额度即将用尽",
		quotaWarningStageHalf,
		1000000,
		topUpLink,
	)

	assert.Equal(t, "您的额度即将用尽（余额低于预警阈值的 50%）", email.Title)
	assert.Contains(t, email.Content, "<a href='{{value}}'>{{value}}</a>")
	assert.Equal(t, []interface{}{email.Title, "¥14.000000", topUpLink, topUpLink}, email.Values)

	bark := newQuotaWarningNotification(
		dto.NotifyTypeBark,
		"您的额度即将用尽",
		quotaWarningStageCritical,
		1000000,
		topUpLink,
	)

	assert.Equal(t, "您的额度即将用尽（余额低于预警阈值的 20%）", bark.Title)
	assert.NotContains(t, bark.Content, "<a")
	assert.Equal(t, []interface{}{bark.Title, "¥14.000000"}, bark.Values)
}

func TestQuotaWarningCycleResetsAfterBalanceRecovery(t *testing.T) {
	resetQuotaWarningMemoryStateForTest()
	originalRedisEnabled := common.RedisEnabled
	originalRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		resetQuotaWarningMemoryStateForTest()
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRDB
	})

	stage, claimed, err := claimQuotaWarningStage(11, BillingSourceWallet, 0, 99, 100)
	require.NoError(t, err)
	assert.Equal(t, quotaWarningStageThreshold, stage)
	assert.True(t, claimed)

	stage, claimed, err = claimQuotaWarningStage(11, BillingSourceWallet, 0, 100, 100)
	require.NoError(t, err)
	assert.Equal(t, quotaWarningStageNone, stage)
	assert.False(t, claimed)

	stage, claimed, err = claimQuotaWarningStage(11, BillingSourceWallet, 0, 99, 100)
	require.NoError(t, err)
	assert.Equal(t, quotaWarningStageThreshold, stage)
	assert.True(t, claimed)
}
