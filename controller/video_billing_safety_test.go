package controller

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoBillingSafetyReservationDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{})
	oldDB := model.DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	model.DB = db
	common.SetDatabaseTypes(dialect, oldLog)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	t.Cleanup(func() {
		model.DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
	})
	user := model.User{Username: "video-reserve", AffCode: "video-reserve", Quota: 100}
	require.NoError(t, db.Create(&user).Error)
	c := taskSubmissionTestContext()
	info := &relaycommon.RelayInfo{UserId: user.Id, UsingGroup: "default", IsPlayground: true, ForcePreConsume: true}
	info.UserSetting.BillingPreference = "wallet_only"
	require.Nil(t, service.PreConsumeBilling(c, 60, info))
	require.Error(t, info.Billing.Reserve(110), "a provider retry cannot reserve more than the available balance")
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, 40, stored.Quota)
	assert.Equal(t, 60, info.Billing.GetPreConsumedQuota())
	require.NoError(t, info.Billing.Reserve(90))
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, 10, stored.Quota)
	require.NoError(t, info.Billing.Settle(40))
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, 60, stored.Quota, "unused reservation is returned")

	// Competing submissions reserve money, not a fixed number of task slots.
	if common.MainDatabaseType() != common.DatabaseTypeSQLite {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(4)
	}
	start := make(chan struct{})
	results := make(chan bool, 4)
	errors := make(chan error, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			<-start
			ok, err := model.TryReserveUserQuotaImmediately(user.Id, 20)
			results <- ok
			errors <- err
		})
	}
	close(start)
	wg.Wait()
	count := 0
	for range 4 {
		require.NoError(t, <-errors)
		if <-results {
			count++
		}
	}
	assert.Equal(t, 3, count)
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Zero(t, stored.Quota)
}
