package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic("failed to open test db: " + err.Error())
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic("failed to get sql.DB: " + err.Error())
	}
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true

	if err := db.AutoMigrate(
		&model.Task{},
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.TopUp{},
		&model.QuotaData{},
		&model.UserSubscription{},
		&model.ProfitCostPrice{},
		&model.ProfitCostPriceVersion{},
		&model.SystemTask{},
		&model.SystemTaskLock{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}

	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

func truncate(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM tasks")
		model.DB.Exec("DELETE FROM users")
		model.DB.Exec("DELETE FROM tokens")
		model.DB.Exec("DELETE FROM logs")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM quota_data")
		model.DB.Exec("DELETE FROM user_subscriptions")
		model.DB.Exec("DELETE FROM profit_cost_prices")
		model.DB.Exec("DELETE FROM profit_cost_price_versions")
		model.DB.Exec("DELETE FROM system_task_locks")
		model.DB.Exec("DELETE FROM system_tasks")
		model.CacheQuotaDataLock.Lock()
		model.CacheQuotaData = make(map[string]*model.QuotaData)
		model.CacheQuotaDataLock.Unlock()
	})
}

func seedUser(t *testing.T, id int, quota int) {
	t.Helper()
	user := &model.User{Id: id, Username: "test_user", Quota: quota, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
}

func seedToken(t *testing.T, id int, userId int, key string, remainQuota int) {
	t.Helper()
	token := &model.Token{
		Id:          id,
		UserId:      userId,
		Key:         key,
		Name:        "test_token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: remainQuota,
		UsedQuota:   0,
	}
	require.NoError(t, model.DB.Create(token).Error)
}

func seedSubscription(t *testing.T, id int, userId int, amountTotal int64, amountUsed int64) {
	t.Helper()
	sub := &model.UserSubscription{
		Id:          id,
		UserId:      userId,
		AmountTotal: amountTotal,
		AmountUsed:  amountUsed,
		Status:      "active",
		StartTime:   time.Now().Unix(),
		EndTime:     time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(sub).Error)
}

func seedChannel(t *testing.T, id int) {
	t.Helper()
	ch := &model.Channel{Id: id, Name: "test_channel", Key: "sk-test", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(ch).Error)
}

func makeTask(userId, channelId, quota, tokenId int, billingSource string, subscriptionId int) *model.Task {
	return &model.Task{
		TaskID:    "task_" + time.Now().Format("150405.000"),
		UserId:    userId,
		ChannelId: channelId,
		Quota:     quota,
		Status:    model.TaskStatus(model.TaskStatusInProgress),
		Group:     "default",
		Data:      json.RawMessage(`{}`),
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "test-model",
		},
		PrivateData: model.TaskPrivateData{
			BillingSource:  billingSource,
			SubscriptionId: subscriptionId,
			TokenId:        tokenId,
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      0.02,
				GroupRatio:      1.0,
				OriginModelName: "test-model",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Read-back helpers
// ---------------------------------------------------------------------------

func getUserQuota(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&user).Error)
	return user.Quota
}

func getUserUsedQuotaAndRequestCount(t *testing.T, id int) (int, int) {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").Where("id = ?", id).First(&user).Error)
	return user.UsedQuota, user.RequestCount
}

func getChannelUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var channel model.Channel
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&channel).Error)
	return int(channel.UsedQuota)
}

func getTokenRemainQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("remain_quota").Where("id = ?", id).First(&token).Error)
	return token.RemainQuota
}

func getTokenUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&token).Error)
	return token.UsedQuota
}

func getSubscriptionUsed(t *testing.T, id int) int64 {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").Where("id = ?", id).First(&sub).Error)
	return sub.AmountUsed
}

func getLastLog(t *testing.T) *model.Log {
	t.Helper()
	var log model.Log
	err := model.LOG_DB.Order("id desc").First(&log).Error
	if err != nil {
		return nil
	}
	return &log
}

func countLogs(t *testing.T) int64 {
	t.Helper()
	var count int64
	model.LOG_DB.Model(&model.Log{}).Count(&count)
	return count
}

func getQuotaDataTotal(t *testing.T, userID int, modelName string) (int, int) {
	t.Helper()
	var total struct {
		Quota     int
		TokenUsed int
	}
	require.NoError(t, model.DB.Table("quota_data").
		Select("COALESCE(SUM(quota), 0) AS quota, COALESCE(SUM(token_used), 0) AS token_used").
		Where("user_id = ? AND model_name = ?", userID, modelName).
		Scan(&total).Error)
	return total.Quota, total.TokenUsed
}

// ===========================================================================
// RefundTaskQuota tests
// ===========================================================================

func TestRefundTaskQuota_Wallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 1, 1, 1
	const initQuota, preConsumed = 10000, 3000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-test-key", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota": preConsumed, "request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.SubmitTime = 1700000000
	task.StartTime = 1700000010
	task.FinishTime = 1700000042

	RefundTaskQuota(ctx, task, "task failed: upstream error")

	// User quota should increase by preConsumed
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Token remain_quota should increase, used_quota should decrease
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, -preConsumed, getTokenUsedQuota(t, tokenID))

	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, 0, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, 0, getChannelUsedQuota(t, channelID))

	// A refund log should be created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed, log.Quota)
	assert.Equal(t, "test-model", log.ModelName)
	assert.Equal(t, 32, log.UseTime)
}

func TestRefundTaskQuota_FinalAccountingRecordsRollbackOnly(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	originalDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = originalDataExportEnabled
	})

	const userID, tokenID, channelID = 26, 26, 26
	const initQuota, preConsumed, tokenRemain = 10000, 3000, 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-final-rollback", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingFinal

	RefundTaskQuota(ctx, task, "async image failed")
	model.SaveQuotaDataCache()

	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, 0, usedQuota)
	assert.Equal(t, 0, requestCount)
	assert.Equal(t, 0, getChannelUsedQuota(t, channelID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypePreConsumeRollback, log.Type)
	assert.Equal(t, preConsumed, log.Quota)

	quota, tokenUsed := getQuotaDataTotal(t, userID, "test-model")
	assert.Equal(t, 0, quota)
	assert.Equal(t, 0, tokenUsed)
}

func TestRefundTaskQuota_Subscription(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 2, 2, 2, 1
	const preConsumed = 2000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-key", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)

	RefundTaskQuota(ctx, task, "subscription task failed")

	// Subscription used should decrease by preConsumed
	assert.Equal(t, subUsed-int64(preConsumed), getSubscriptionUsed(t, subID))

	// Token should also be refunded
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestRefundTaskQuota_ZeroQuota(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 3
	seedUser(t, userID, 5000)

	task := makeTask(userID, 0, 0, 0, BillingSourceWallet, 0)

	RefundTaskQuota(ctx, task, "zero quota task")

	// No change to user quota
	assert.Equal(t, 5000, getUserQuota(t, userID))

	// No log created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRefundTaskQuota_NoToken(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 4, 4
	const initQuota, preConsumed = 10000, 1500

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0) // TokenId=0

	RefundTaskQuota(ctx, task, "no token task failed")

	// User quota refunded
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Log created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestRefundTaskQuotaCompensatesWhenRefundStatePersistenceFails(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 45, 45, 45
	const initQuota, preConsumed = 10000, 1500
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-refund-persist-fail", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota": preConsumed, "request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingSubmit
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_task_refund_state_update
		BEFORE UPDATE OF private_data ON tasks
		BEGIN
			SELECT RAISE(ABORT, 'forced refund state update failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_task_refund_state_update")
	})

	err := RefundTaskQuota(ctx, task, "forced persistence failure")
	require.Error(t, err)

	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, preConsumed, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, preConsumed, getChannelUsedQuota(t, channelID))
	assert.NotEqual(t, TaskBillingStateRefunded, task.PrivateData.BillingState)
	assert.Equal(t, int64(0), countLogs(t))
}

// ===========================================================================
// RecalculateTaskQuota tests
// ===========================================================================

func TestRecalculate_PositiveDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 10, 10, 10
	const initQuota, preConsumed = 10000, 2000
	const actualQuota = 3000 // under-charged by 1000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-pos", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota": preConsumed, "request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.SubmitTime = 1700000100
	task.StartTime = 1700000110
	task.FinishTime = 1700000180

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	// User quota should decrease by the delta (1000 additional charge)
	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID))

	// Token should also be charged the delta
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)

	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, actualQuota, getChannelUsedQuota(t, channelID))

	// Log type should be Consume (additional charge)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
	assert.Equal(t, 70, log.UseTime)
}

func TestRecalculateTaskQuotaCompensatesWhenQuotaPersistenceFails(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 44, 44, 44
	const initQuota, preConsumed, actualQuota = 10000, 2000, 3000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-persist-fail", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota": preConsumed, "request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_task_quota_update
		BEFORE UPDATE OF quota ON tasks
		BEGIN
			SELECT RAISE(ABORT, 'forced quota update failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_task_quota_update")
	})

	err := RecalculateTaskQuota(ctx, task, actualQuota, "forced persistence failure")
	require.Error(t, err)

	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, preConsumed, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, preConsumed, getChannelUsedQuota(t, channelID))
	assert.Equal(t, int64(0), countLogs(t))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, preConsumed, persisted.Quota)
	assert.NotEmpty(t, persisted.PrivateData.BillingError)
}

func TestRecalculate_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 11, 11, 11
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged by 2000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-neg", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota": preConsumed, "request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	// User quota should increase by abs(delta) = 2000 (refund overpayment)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))

	// Token should be refunded the difference
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota updated
	assert.Equal(t, actualQuota, task.Quota)

	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, actualQuota, getChannelUsedQuota(t, channelID))

	// Log type should be Refund
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed-actualQuota, log.Quota)
}

func TestRecalculate_ZeroDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 12
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, preConsumed, "exact match")

	// No change to user quota
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No log created (delta is zero)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_ActualQuotaZero(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 13
	const initQuota = 10000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, 5000, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, 0, "zero actual")

	// No change (early return)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculateTaskQuotaByUsageUsesCompletionRatio(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	originalModelRatio := ratio_setting.ModelRatio2JSONString()
	originalCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModelRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(originalCompletionRatio))
	})

	const userID, tokenID, channelID = 131, 131, 131
	const initQuota, preConsumed = 100000, 1000
	const tokenRemain = 50000
	const actualQuota = 6050 // (prompt 100 + completion 200 * 60) * modelRatio 0.5

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gemini-async-image-token-test":0.5}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gemini-async-image-token-test":60}`))
	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-async-image-usage", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Properties.OriginModelName = "gemini-async-image-token-test"
	task.PrivateData.BillingContext.OriginModelName = "gemini-async-image-token-test"
	task.PrivateData.BillingContext.ModelRatio = 0.5
	task.PrivateData.BillingContext.GroupRatio = 1

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 200,
		TotalTokens:      300,
	}

	RecalculateTaskQuotaByUsage(ctx, task, usage)

	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
	assert.Equal(t, "gemini-async-image-token-test", log.ModelName)
}

func TestRecalculate_Subscription_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 14, 14, 14, 2
	const preConsumed = 5000
	const actualQuota = 2000 // over-charged by 3000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-recalc", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)

	RecalculateTaskQuota(ctx, task, actualQuota, "subscription over-charge")

	// Subscription used should decrease by delta (refund 3000)
	assert.Equal(t, subUsed-int64(preConsumed-actualQuota), getSubscriptionUsed(t, subID))

	// Token refunded
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	assert.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

// ===========================================================================
// CAS + Billing integration tests
// Simulates the flow in updateVideoSingleTask (service/task_polling.go)
// ===========================================================================

// simulatePollBilling reproduces the CAS + billing logic from updateVideoSingleTask.
// It takes a persisted task (already in DB), applies the new status, and performs
// the conditional update + billing exactly as the polling loop does.
func simulatePollBilling(ctx context.Context, task *model.Task, newStatus model.TaskStatus, actualQuota int) {
	snap := task.Snapshot()

	shouldRefund := false
	shouldSettle := false
	quota := task.Quota

	task.Status = newStatus
	switch string(newStatus) {
	case model.TaskStatusSuccess:
		task.Progress = "100%"
		task.FinishTime = 9999
		shouldSettle = true
	case model.TaskStatusFailure:
		task.Progress = "100%"
		task.FinishTime = 9999
		task.FailReason = "upstream error"
		if quota != 0 {
			shouldRefund = true
		}
	default:
		task.Progress = "50%"
	}

	isDone := task.Status == model.TaskStatus(model.TaskStatusSuccess) || task.Status == model.TaskStatus(model.TaskStatusFailure)
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			shouldRefund = false
			shouldSettle = false
		} else if !won {
			shouldRefund = false
			shouldSettle = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		_, _ = task.UpdateWithStatus(snap.Status)
	}

	if shouldSettle && actualQuota > 0 {
		RecalculateTaskQuota(ctx, task, actualQuota, "test settle")
	}
	if shouldRefund {
		RefundTaskQuota(ctx, task, task.FailReason)
	}
}

func TestCASGuardedRefund_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 20, 20, 20
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS wins: task in DB should now be FAILURE
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)

	// Refund should have happened
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestCASGuardedRefund_Lose(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 21, 21, 21
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-lose", tokenRemain)
	seedChannel(t, channelID)

	// Create task with IN_PROGRESS in DB
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate another process already transitioning to FAILURE
	model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailure)

	// Our process still has the old in-memory state (IN_PROGRESS) and tries to transition
	// task.Status is still IN_PROGRESS in the snapshot
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS lost: user quota should NOT change (no double refund)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))

	// No billing log should be created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestCASGuardedSettle_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 22, 22, 22
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged, should get partial refund
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-settle-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusSuccess), actualQuota)

	// CAS wins: task should be SUCCESS
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, reloaded.Status)

	// Settlement should refund the over-charge (5000 - 3000 = 2000 back to user)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)
}

func TestConfirmTaskBillingSettledRecordsQuotaData(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	originalDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = originalDataExportEnabled
	})

	const userID, tokenID, channelID = 24, 24, 24
	const actualQuota = 3500

	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-quota-data", 10000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, actualQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task_quota_data"
	task.SubmitTime = 1700000200
	task.StartTime = 1700000215
	task.FinishTime = 1700000260
	task.PrivateData.PromptTokens = 123
	task.PrivateData.CompletionTokens = 77
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingFinal

	require.NoError(t, ConfirmTaskBillingSettled(ctx, task, actualQuota, "async task completed"))
	model.SaveQuotaDataCache()

	quota, tokenUsed := getQuotaDataTotal(t, userID, "test-model")
	assert.Equal(t, actualQuota, quota)
	assert.Equal(t, 200, tokenUsed)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, 45, log.UseTime)
}

func TestRefundTaskQuotaOffsetsQuotaDataWithoutAddingCalls(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	originalDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = originalDataExportEnabled
	})

	const userID, tokenID, channelID = 25, 25, 25
	const preConsumed = 2000

	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-refund-quota-data", 10000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task_refund_quota_data"

	RefundTaskQuota(ctx, task, "task failed")
	model.SaveQuotaDataCache()

	quota, tokenUsed := getQuotaDataTotal(t, userID, "test-model")
	assert.Equal(t, -preConsumed, quota)
	assert.Equal(t, 0, tokenUsed)

	var countTotal int
	require.NoError(t, model.DB.Table("quota_data").
		Select("COALESCE(SUM(count), 0)").
		Where("user_id = ? AND model_name = ?", userID, "test-model").
		Scan(&countTotal).Error)
	assert.Equal(t, 0, countTotal)
}

func TestNonTerminalUpdate_NoBilling(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 23, 23
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "20%"
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate a non-terminal poll update (still IN_PROGRESS, progress changed)
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusInProgress), 0)

	// User quota should NOT change
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No billing log
	assert.Equal(t, int64(0), countLogs(t))

	// Task progress should be updated in DB
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.Equal(t, "50%", reloaded.Progress)
}

// ===========================================================================
// Mock adaptor for settleTaskBillingOnComplete tests
// ===========================================================================

type mockAdaptor struct {
	adjustReturn int
}

func (m *mockAdaptor) Init(_ *relaycommon.RelayInfo) {}
func (m *mockAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}
func (m *mockAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) { return nil, nil }
func (m *mockAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return m.adjustReturn
}

// ===========================================================================
// PerCallBilling tests — settleTaskBillingOnComplete
// ===========================================================================

func TestSettle_PerCallBilling_SkipsAdaptorAdjust(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 30, 30, 30
	const initQuota, preConsumed = 10000, 5000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-adaptor", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 2000}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no adjustment despite adaptor returning 2000
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_PerCallBilling_SkipsTotalTokens(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 31, 31, 31
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 7000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-tokens", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 0}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 9999}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no recalculation by tokens
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_NonPerCall_AdaptorAdjustWorks(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 32, 32, 32
	const initQuota, preConsumed = 10000, 5000
	const adaptorQuota = 3000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-nonpercall-adj", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	// PerCallBilling defaults to false

	adaptor := &mockAdaptor{adjustReturn: adaptorQuota}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Non-per-call: adaptor adjustment applies (refund 2000)
	assert.Equal(t, initQuota+(preConsumed-adaptorQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-adaptorQuota), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, adaptorQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestCalculateTaskQuotaByUsage_GPTImage2OFUsesTokenPricing(t *testing.T) {
	originalCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gpt-image-2-OF":6}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(originalCompletionRatio))
	})

	task := makeTask(41, 41, 0, 0, BillingSourceWallet, 0)
	task.Properties.OriginModelName = "gpt-image-2-OF"
	task.PrivateData.BillingContext.OriginModelName = "gpt-image-2-OF"
	task.PrivateData.BillingContext.ModelPrice = 0
	task.PrivateData.BillingContext.ModelRatio = 13.0 / 1000 * ratio_setting.RMB
	task.PrivateData.BillingContext.PerCallBilling = false

	usage := &dto.Usage{
		PromptTokens: 1000,
		CompletionTokenDetails: dto.OutputTokenDetails{
			ImageTokens: 200,
		},
	}

	actualQuota := CalculateTaskQuotaByUsage(task, usage)
	expectedQuotaFloat := (float64(1000) + float64(200)*6) * (13.0 / 1000 * ratio_setting.RMB)
	expectedQuota := int(expectedQuotaFloat + 0.5)

	assert.Equal(t, expectedQuota, actualQuota)
}

func TestCalculateTaskQuotaByUsageCheckedClampsOverflow(t *testing.T) {
	task := makeTask(42, 42, 0, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.ModelPrice = 0
	task.PrivateData.BillingContext.ModelRatio = 1e20
	task.PrivateData.BillingContext.GroupRatio = 1

	quota, clamp := CalculateTaskQuotaByUsageChecked(task, &dto.Usage{PromptTokens: 100})

	assert.Equal(t, common.MaxQuota, quota)
	require.NotNil(t, clamp)
	assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	assert.Equal(t, common.MaxQuota, CalculateTaskQuotaByUsage(task, &dto.Usage{PromptTokens: 100}))
}

func TestConfirmTaskBillingSettledAttachesQuotaClamp(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 43, 43, 43
	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-clamped-task", 10000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, 1000, tokenID, BillingSourceWallet, 0)
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingFinal
	clamp := &common.QuotaClamp{
		Op:       "QuotaFromDecimal",
		Kind:     common.QuotaClampOverflow,
		Original: 1e30,
		Clamped:  common.MaxQuota,
	}
	require.NoError(t, ConfirmTaskBillingSettled(ctx, task, 1000, "clamped settlement", clamp))

	log := getLastLog(t)
	require.NotNil(t, log)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	saturation, ok := adminInfo["quota_saturation"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, common.QuotaClampOverflow, saturation["kind"])
}

func TestRefundTaskQuotaDeletedTokenStillRefundsFunding(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, deletedTokenID = 41, 4100
	const initQuota, preConsumed = 10000, 1500
	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, preConsumed, deletedTokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	require.NoError(t, RefundTaskQuota(ctx, task, "deleted token task failed"))

	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, TaskBillingStateRefunded, task.PrivateData.BillingState)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, deletedTokenID, log.TokenId)
}

func TestConfirmTaskBillingSettledRollsBackStateWhenUsageAccountingFails(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, actualQuota = 46, 46, 46, 1800
	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-confirm-accounting-fail", 5000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, actualQuota, tokenID, BillingSourceWallet, 0)
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingFinal
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_confirm_user_usage_update
		BEFORE UPDATE OF used_quota ON users
		BEGIN
			SELECT RAISE(ABORT, 'forced confirm usage update failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_confirm_user_usage_update")
	})

	err := ConfirmTaskBillingSettled(ctx, task, actualQuota, "forced accounting failure")
	require.Error(t, err)

	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Zero(t, usedQuota)
	assert.Zero(t, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	assert.NotEqual(t, TaskBillingStateSettled, task.PrivateData.BillingState)
	assert.NotEmpty(t, task.PrivateData.BillingError)
	assert.Equal(t, int64(0), countLogs(t))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.NotEqual(t, TaskBillingStateSettled, persisted.PrivateData.BillingState)
	assert.NotEmpty(t, persisted.PrivateData.BillingError)
}

func TestRecalculateTaskQuotaCompensatesWhenUsageAccountingFails(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 47, 47, 47
	const initQuota, preConsumed, actualQuota, tokenRemain = 10000, 2000, 3000, 5000
	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-accounting-fail", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota": preConsumed, "request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_recalculate_channel_usage_update
		BEFORE UPDATE OF used_quota ON channels
		BEGIN
			SELECT RAISE(ABORT, 'forced channel usage update failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_recalculate_channel_usage_update")
	})

	err := RecalculateTaskQuota(ctx, task, actualQuota, "forced accounting failure")
	require.Error(t, err)

	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, preConsumed, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, preConsumed, getChannelUsedQuota(t, channelID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, preConsumed, persisted.Quota)
	assert.NotEmpty(t, persisted.PrivateData.BillingError)
}

func TestRefundTaskQuotaBypassesBatchUpdaterForCriticalAccounting(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})

	const userID, tokenID, channelID = 48, 48, 48
	const initQuota, preConsumed, tokenRemain = 10000, 1500, 5000
	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-refund-batch-bypass", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota": preConsumed, "request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, RefundTaskQuota(ctx, task, "batch updater bypass"))

	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	assert.Equal(t, TaskBillingStateRefunded, task.PrivateData.BillingState)
}

func TestUpdateSunoTasksMissingChannelKeepsTaskPending(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, missingChannelID = 42, 4200
	seedUser(t, userID, 10000)

	task := makeTask(userID, missingChannelID, 1200, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatusInProgress
	require.NoError(t, model.DB.Create(task).Error)

	err := updateSunoTasks(ctx, missingChannelID, []string{"upstream-task"}, map[string]*model.Task{
		"upstream-task": task,
	})
	require.Error(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusInProgress, reloaded.Status)
	assert.Equal(t, 1200, reloaded.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestForcePreConsumePersistsAndRefundsBeforeReturning(t *testing.T) {
	truncate(t)

	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})

	const userID, tokenID = 49, 49
	const userQuota, tokenQuota, preConsumed = 10000, 5000, 1500
	const tokenKey = "sk-force-preconsume"
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, tokenKey, tokenQuota)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        tokenKey,
		ForcePreConsume: true,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}

	require.Nil(t, PreConsumeBilling(c, preConsumed, info))
	assert.Equal(t, userQuota-preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, getTokenUsedQuota(t, tokenID))

	info.Billing.Refund(c)
	assert.Equal(t, userQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
}

func TestForcePreConsumeRollsBackTokenWhenWalletPersistenceFails(t *testing.T) {
	truncate(t)

	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})

	const userID, tokenID = 50, 50
	const userQuota, tokenQuota, preConsumed = 10000, 5000, 1500
	const tokenKey = "sk-force-preconsume-wallet-failure"
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, tokenKey, tokenQuota)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_force_preconsume_wallet_update
		BEFORE UPDATE OF quota ON users
		BEGIN
			SELECT RAISE(ABORT, 'forced wallet update failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_force_preconsume_wallet_update")
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations?async=true", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        tokenKey,
		ForcePreConsume: true,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}

	require.NotNil(t, PreConsumeBilling(c, preConsumed, info))
	assert.Equal(t, userQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
}

func TestForcePreConsumeSettleCompensatesFundingWhenTokenPersistenceFails(t *testing.T) {
	truncate(t)

	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})

	const userID, tokenID = 51, 51
	const userQuota, tokenQuota, preConsumed, actualQuota = 10000, 5000, 1000, 1600
	const tokenKey = "sk-force-preconsume-settle-failure"
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, tokenKey, tokenQuota)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        tokenKey,
		ForcePreConsume: true,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}
	require.Nil(t, PreConsumeBilling(c, preConsumed, info))

	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_force_preconsume_token_settlement
		BEFORE UPDATE OF remain_quota ON tokens
		BEGIN
			SELECT RAISE(ABORT, 'forced token settlement failure');
		END;
	`).Error)

	err := info.Billing.Settle(actualQuota)
	require.Error(t, err)
	assert.Equal(t, userQuota-preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, getTokenUsedQuota(t, tokenID))

	require.NoError(t, model.DB.Exec("DROP TRIGGER IF EXISTS fail_force_preconsume_token_settlement").Error)
	info.Billing.Refund(c)
	assert.Equal(t, userQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
}

func TestForcePreConsumeSettlementBypassesBatchUpdater(t *testing.T) {
	truncate(t)

	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})

	const userID, tokenID = 52, 52
	const userQuota, tokenQuota, preConsumed, actualQuota = 10000, 5000, 1000, 1600
	const tokenKey = "sk-force-preconsume-settlement"
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, tokenKey, tokenQuota)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        tokenKey,
		ForcePreConsume: true,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}

	require.Nil(t, PreConsumeBilling(c, preConsumed, info))
	require.NoError(t, info.Billing.Settle(actualQuota))
	assert.Equal(t, userQuota-actualQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-actualQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, getTokenUsedQuota(t, tokenID))

	info.Billing.Refund(c)
	assert.Equal(t, userQuota-actualQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-actualQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, getTokenUsedQuota(t, tokenID))
}

func TestLogTaskConsumptionBypassesBatchUpdaterForForcedPreConsume(t *testing.T) {
	truncate(t)

	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})

	const userID, tokenID, channelID, quota = 53, 53, 53, 1200
	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-task-usage-accounting", 5000)
	seedChannel(t, channelID)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	c.Set("username", "test_user")
	c.Set("token_name", "test_token")
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		OriginModelName: "test-model",
		UsingGroup:      "default",
		ForcePreConsume: true,
		PriceData: types.PriceData{
			Quota: quota,
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: "video",
		},
	}

	require.NoError(t, LogTaskConsumption(c, info))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, quota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, quota, getChannelUsedQuota(t, channelID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, quota, log.Quota)
	assert.Equal(t, tokenID, log.TokenId)
}

func TestConfirmTaskBillingSettledSubmitModeDoesNotDoubleAccount(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, actualQuota = 54, 54, 54, 1800
	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-submit-confirm", 5000)
	seedChannel(t, channelID)
	require.NoError(t, model.AdjustTaskUsageAccounting(userID, channelID, actualQuota, 1))

	task := makeTask(userID, channelID, actualQuota, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingState = TaskBillingStatePending
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingSubmit
	task.PrivateData.PreConsumedQuota = actualQuota
	require.NoError(t, model.DB.Create(task).Error)

	require.NoError(t, ConfirmTaskBillingSettled(ctx, task, actualQuota, "submit-accounted task completed"))

	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, actualQuota, getChannelUsedQuota(t, channelID))
	assert.Equal(t, int64(0), countLogs(t))
	assert.Equal(t, TaskBillingStateSettled, task.PrivateData.BillingState)

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, TaskBillingStateSettled, persisted.PrivateData.BillingState)
}

func TestRecalculateTaskQuotaFinalModeDefersUsageUntilConfirmation(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 55, 55, 55
	const currentQuota, preConsumed, actualQuota, tokenRemain = 10000, 1200, 2000, 5000
	seedUser(t, userID, currentQuota)
	seedToken(t, tokenID, userID, "sk-final-accounting", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingState = TaskBillingStatePending
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingFinal
	task.PrivateData.PreConsumedQuota = preConsumed
	require.NoError(t, model.DB.Create(task).Error)

	require.NoError(t, RecalculateTaskQuota(ctx, task, actualQuota, "final-mode adjustment"))
	assert.Equal(t, currentQuota-(actualQuota-preConsumed), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Zero(t, usedQuota)
	assert.Zero(t, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	assert.Equal(t, int64(0), countLogs(t))

	require.NoError(t, ConfirmTaskBillingSettled(ctx, task, task.Quota, "final-mode task completed"))
	usedQuota, requestCount = getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, actualQuota, getChannelUsedQuota(t, channelID))
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota, log.Quota)
}

func TestSettleTaskBillingRetriesDeferredSubmitSettlement(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 56, 56, 56
	const currentQuota, preConsumed, actualQuota, tokenRemain = 10000, 1500, 2400, 5000
	seedUser(t, userID, currentQuota)
	seedToken(t, tokenID, userID, "sk-deferred-settlement", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.AdjustTaskUsageAccounting(userID, channelID, preConsumed, 1))

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingState = TaskBillingStatePending
	task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingSubmit
	task.PrivateData.PreConsumedQuota = preConsumed
	task.PrivateData.ActualQuota = actualQuota
	task.PrivateData.BillingError = "task settlement failed; retained pre-consumed quota"
	task.PrivateData.BillingContext.PerCallBilling = true
	require.NoError(t, model.DB.Create(task).Error)

	adaptor := &mockAdaptor{adjustReturn: 0}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}
	require.NoError(t, settleTaskBillingOnComplete(ctx, adaptor, task, taskResult))

	assert.Equal(t, currentQuota-(actualQuota-preConsumed), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, actualQuota, getChannelUsedQuota(t, channelID))
	assert.Equal(t, actualQuota, task.Quota)
	assert.Equal(t, TaskBillingStateSettled, task.PrivateData.BillingState)
	assert.Empty(t, task.PrivateData.BillingError)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
}

func TestLogTaskConsumptionDoesNotWriteLogWhenImmediateUsageAccountingFails(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID, quota = 57, 57, 57, 1200
	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-task-usage-failure", 5000)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_log_task_usage_update
		BEFORE UPDATE OF used_quota ON users
		BEGIN
			SELECT RAISE(ABORT, 'forced task usage failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_log_task_usage_update")
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		OriginModelName: "test-model",
		UsingGroup:      "default",
		ForcePreConsume: true,
		PriceData: types.PriceData{
			Quota: quota,
		},
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelId: channelID},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: "video"},
	}

	require.Error(t, LogTaskConsumption(c, info))
	usedQuota, requestCount := getUserUsedQuotaAndRequestCount(t, userID)
	assert.Zero(t, usedQuota)
	assert.Zero(t, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	assert.Equal(t, int64(0), countLogs(t))
}
