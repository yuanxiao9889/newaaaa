package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddAdminQuotaCreatesBillingRecord(t *testing.T) {
	truncateTables(t)

	user := &User{
		Id:       801,
		Username: "admin_topup_user",
		Status:   common.UserStatusEnabled,
		Quota:    100,
	}
	require.NoError(t, DB.Create(user).Error)

	quota := int(common.QuotaPerUnit) * 2
	topUp, err := AddAdminQuota(user.Id, quota)
	require.NoError(t, err)
	require.NotNil(t, topUp)

	var updatedUser User
	require.NoError(t, DB.First(&updatedUser, user.Id).Error)
	assert.Equal(t, 100+quota, updatedUser.Quota)

	var stored TopUp
	require.NoError(t, DB.Where("trade_no = ?", topUp.TradeNo).First(&stored).Error)
	assert.Equal(t, user.Id, stored.UserId)
	assert.Equal(t, quota, stored.QuotaAmount)
	assert.Equal(t, int64(2), stored.Amount)
	assert.Equal(t, PaymentMethodOfficialWebsite, stored.PaymentMethod)
	assert.Equal(t, PaymentProviderAdmin, stored.PaymentProvider)
	assert.Equal(t, common.TopUpStatusSuccess, stored.Status)
	assert.NotZero(t, stored.CreateTime)
	assert.Equal(t, stored.CreateTime, stored.CompleteTime)
	assert.True(t, strings.HasPrefix(stored.TradeNo, "ADMUSR801NO"))
}

func TestAddAdminQuotaRollsBackWhenUserDoesNotExist(t *testing.T) {
	truncateTables(t)

	topUp, err := AddAdminQuota(9999, int(common.QuotaPerUnit))
	require.Error(t, err)
	assert.Nil(t, topUp)

	var count int64
	require.NoError(t, DB.Model(&TopUp{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBackfillAdminQuotaTopUps(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where(commonKeyCol+" = ?", adminQuotaTopUpBackfillOptionKey).Delete(&Option{}).Error)
	t.Cleanup(func() {
		DB.Where(commonKeyCol+" = ?", adminQuotaTopUpBackfillOptionKey).Delete(&Option{})
	})

	oldCreatedAt := int64(1_700_000_000)
	namedAdminCreatedAt := int64(1_700_000_050)
	structuredCreatedAt := int64(1_700_000_100)
	logs := []*Log{
		{
			UserId:    811,
			CreatedAt: oldCreatedAt,
			Type:      LogTypeManage,
			Content:   "管理员增加用户额度 ＄1.000000 额度",
		},
		{
			UserId:    814,
			CreatedAt: namedAdminCreatedAt,
			Type:      LogTypeManage,
			Content:   "管理员(root)增加用户额度 ＄2.000000 额度",
		},
		{
			UserId:    900,
			CreatedAt: structuredCreatedAt,
			Type:      LogTypeManage,
			Content:   "Increased user quota by $1.500000 quota",
			RequestId: "admin-quota-request",
			Other: common.MapToJsonStr(map[string]interface{}{
				"op": map[string]interface{}{
					"action": "user.quota_add",
					"params": map[string]interface{}{
						"target_user_id": 812,
						"quota":          "$1.500000 quota",
						"quota_value":    750000,
					},
				},
			}),
		},
		{
			UserId:    813,
			CreatedAt: structuredCreatedAt + 1,
			Type:      LogTypeManage,
			Content:   "Updated user profile",
		},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	inserted, err := BackfillAdminQuotaTopUps()
	require.NoError(t, err)
	assert.Equal(t, int64(3), inserted)

	var topUps []TopUp
	require.NoError(t, DB.Order("create_time asc").Find(&topUps).Error)
	require.Len(t, topUps, 3)

	assert.Equal(t, 811, topUps[0].UserId)
	assert.Equal(t, int(common.QuotaPerUnit), topUps[0].QuotaAmount)
	assert.Equal(t, oldCreatedAt, topUps[0].CreateTime)
	assert.Equal(t, oldCreatedAt, topUps[0].CompleteTime)
	assert.True(t, strings.HasPrefix(topUps[0].TradeNo, "ADMLOG"))

	assert.Equal(t, 814, topUps[1].UserId)
	assert.Equal(t, int(common.QuotaPerUnit)*2, topUps[1].QuotaAmount)
	assert.Equal(t, namedAdminCreatedAt, topUps[1].CreateTime)
	assert.Equal(t, namedAdminCreatedAt, topUps[1].CompleteTime)
	assert.True(t, strings.HasPrefix(topUps[1].TradeNo, "ADMLOG"))

	assert.Equal(t, 812, topUps[2].UserId)
	assert.Equal(t, 750000, topUps[2].QuotaAmount)
	assert.Equal(t, structuredCreatedAt, topUps[2].CreateTime)
	assert.Equal(t, structuredCreatedAt, topUps[2].CompleteTime)
	assert.True(t, strings.HasPrefix(topUps[2].TradeNo, "ADMLOG"))

	for _, topUp := range topUps {
		assert.Equal(t, PaymentMethodOfficialWebsite, topUp.PaymentMethod)
		assert.Equal(t, PaymentProviderAdmin, topUp.PaymentProvider)
		assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
	}

	visibleTopUps, total, err := GetUserTopUps(811, &common.PageInfo{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, visibleTopUps, 1)
	assert.Equal(t, oldCreatedAt, visibleTopUps[0].CreateTime)

	inserted, err = BackfillAdminQuotaTopUps()
	require.NoError(t, err)
	assert.Zero(t, inserted)

	var count int64
	require.NoError(t, DB.Model(&TopUp{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
}
