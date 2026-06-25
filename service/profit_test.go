package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertProfitTestChannel(t *testing.T, id int, name string) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.Channel{Id: id, Name: name}).Error)
}

func insertProfitTestLog(t *testing.T, id int, createdAt int64, channelId int, modelName string, quota int, prompt int, completion int, other string) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		Id:               id,
		CreatedAt:        createdAt,
		Type:             model.LogTypeConsume,
		ChannelId:        channelId,
		ModelName:        modelName,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		UseTime:          2,
		Other:            other,
	}).Error)
}

func truncateProfitTestTables(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM logs")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM profit_cost_prices")
		model.DB.Exec("DELETE FROM profit_cost_price_versions")
	})
}

func TestProfitReport_TieredCostUsesEffectiveVersion(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 1000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 10, "primary")
	base := time.Date(2026, 1, 10, 0, 0, 0, 0, time.Local).Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     10,
		ModelName:     "gpt-profit",
		PriceType:     model.ProfitPriceTypeTieredExpr,
		PriceValue:    "tier(\"old\", p * 1 + c * 2)",
		EffectiveFrom: base - 1000,
		CreatedBy:     1,
	})
	require.NoError(t, err)
	_, err = model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     10,
		ModelName:     "gpt-profit",
		PriceType:     model.ProfitPriceTypeTieredExpr,
		PriceValue:    "tier(\"new\", p * 2 + c * 4)",
		EffectiveFrom: base + 1000,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	insertProfitTestLog(t, 1, base, 10, "gpt-profit", 1000, 1000, 500, "")
	insertProfitTestLog(t, 2, base+2000, 10, "gpt-profit", 2000, 1000, 500, "")

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: base - 10,
		EndTimestamp:   base + 3000,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2), report.Summary.RequestCount)
	assert.Equal(t, int64(2), report.Summary.PricedCount)
	assert.InDelta(t, 3.0, report.Summary.Revenue, 0.000001)
	assert.InDelta(t, 0.006, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 2.994, report.Summary.Profit, 0.000001)
	assert.Equal(t, 1.0, report.Summary.CoverageRate)
}

func TestProfitReport_FixedCostAndUnpricedCoverage(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 1000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 20, "fixed")
	now := time.Now().Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     20,
		ModelName:     "fixed-model",
		PriceType:     model.ProfitPriceTypeFixedPrice,
		FixedUnit:     model.ProfitFixedUnitSecond,
		FixedAmount:   0.25,
		EffectiveFrom: now - 10,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	insertProfitTestLog(t, 1, now, 20, "fixed-model", 1000, 0, 0, "")
	insertProfitTestLog(t, 2, now, 20, "missing-model", 1000, 0, 0, "")

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 1,
		EndTimestamp:   now + 1,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2), report.Summary.RequestCount)
	assert.Equal(t, int64(1), report.Summary.PricedCount)
	assert.Equal(t, int64(1), report.Summary.UnpricedCount)
	assert.InDelta(t, 2.0, report.Summary.Revenue, 0.000001)
	assert.InDelta(t, 1.0, report.Summary.PricedRevenue, 0.000001)
	assert.InDelta(t, 1.0, report.Summary.UnpricedRevenue, 0.000001)
	assert.InDelta(t, 0.5, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 0.5, report.Summary.Profit, 0.000001)
	assert.InDelta(t, 0.5, report.Summary.CoverageRate, 0.000001)
}

func TestProfitReport_SecondRevenueCanUseRequestCost(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 1000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 21, "mixed-unit")
	now := time.Now().Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     21,
		ModelName:     "second-sale-request-cost-model",
		PriceType:     model.ProfitPriceTypeFixedPrice,
		FixedUnit:     model.ProfitFixedUnitRequest,
		FixedAmount:   0.2,
		EffectiveFrom: now - 10,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	insertProfitTestLog(t, 1, now, 21, "second-sale-request-cost-model", 2500, 0, 0, `{"is_task":true,"seconds":5}`)

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 1,
		EndTimestamp:   now + 1,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), report.Summary.PricedCount)
	assert.InDelta(t, 2.5, report.Summary.Revenue, 0.000001)
	assert.InDelta(t, 0.2, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 2.3, report.Summary.Profit, 0.000001)
}

func TestProfitReport_SecondCostUsesTaskDuration(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 1000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 22, "video")
	now := time.Now().Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     22,
		ModelName:     "video-second-model",
		PriceType:     model.ProfitPriceTypeFixedPrice,
		FixedUnit:     model.ProfitFixedUnitSecond,
		FixedAmount:   0.5,
		EffectiveFrom: now - 10,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	insertProfitTestLog(t, 1, now, 22, "video-second-model", 10000, 0, 0, `{"is_task":true,"seconds":8}`)
	insertProfitTestLog(t, 2, now, 22, "video-second-model", 10000, 0, 0, "")

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 1,
		EndTimestamp:   now + 1,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2), report.Summary.PricedCount)
	assert.InDelta(t, 5.0, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 15.0, report.Summary.Profit, 0.000001)
}

func TestProfitReport_SimpleTokenCost(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 1000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 25, "simple")
	now := time.Now().Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     25,
		ModelName:     "simple-model",
		PriceType:     model.ProfitPriceTypeSimpleToken,
		InputPrice:    1,
		OutputPrice:   2,
		RequestPrice:  0.1,
		SecondPrice:   0.25,
		EffectiveFrom: now - 10,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	insertProfitTestLog(t, 1, now, 25, "simple-model", 1000, 1000, 500, "")

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 1,
		EndTimestamp:   now + 1,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), report.Summary.PricedCount)
	assert.InDelta(t, 0.602, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 0.398, report.Summary.Profit, 0.000001)
}

func TestProfitReport_SimpleTokenCacheReadCostExcludesCachedInput(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 1000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 27, "cache-read")
	now := time.Now().Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:      27,
		ModelName:      "cache-read-model",
		PriceType:      model.ProfitPriceTypeSimpleToken,
		InputPrice:     1,
		CacheReadPrice: 0.25,
		OutputPrice:    2,
		EffectiveFrom:  now - 10,
		CreatedBy:      1,
	})
	require.NoError(t, err)

	insertProfitTestLog(t, 1, now, 27, "cache-read-model", 1000, 1000, 500, `{"cache_tokens":400}`)

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 1,
		EndTimestamp:   now + 1,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), report.Summary.PricedCount)
	assert.InDelta(t, 0.0017, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 0.9983, report.Summary.Profit, 0.000001)
}

func TestProfitCostPricePrefillUsesChannelModelsAndCurrentPricing(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalModelRatio := ratio_setting.ModelRatio2JSONString()
	originalCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	originalModelPrice := ratio_setting.ModelPrice2JSONString()
	common.QuotaPerUnit = 500000
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModelRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(originalCompletionRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(originalModelPrice))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"profit-prefill-model":1.25}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"profit-prefill-model":4}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"profit-fixed-model":0.07,"sora-profit-video":0.2}`))
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     26,
		Name:   "prefill",
		Models: "profit-fixed-model, profit-prefill-model,profit-prefill-model,sora-profit-video",
	}).Error)

	prefill, err := GetProfitCostPricePrefill(26, "profit-prefill-model")
	require.NoError(t, err)
	require.Equal(t, []string{"profit-fixed-model", "profit-prefill-model", "sora-profit-video"}, prefill.Models)
	require.Equal(t, model.ProfitPriceTypeSimpleToken, prefill.PricingMode)
	require.True(t, prefill.HasPricing)
	assert.InDelta(t, 2.5, prefill.InputPrice, 0.000001)
	assert.InDelta(t, 10.0, prefill.OutputPrice, 0.000001)

	fixedPrefill, err := GetProfitCostPricePrefill(26, "profit-fixed-model")
	require.NoError(t, err)
	require.Equal(t, model.ProfitPriceTypeFixedPrice, fixedPrefill.PricingMode)
	assert.InDelta(t, 0.07, fixedPrefill.RequestPrice, 0.000001)

	videoPrefill, err := GetProfitCostPricePrefill(26, "sora-profit-video")
	require.NoError(t, err)
	require.Equal(t, model.ProfitPriceTypeFixedPrice, videoPrefill.PricingMode)
	assert.InDelta(t, 0.2, videoPrefill.SecondPrice, 0.000001)
	assert.Zero(t, videoPrefill.RequestPrice)
}

func TestProfitCostPriceEncryptionDoesNotStorePlaintext(t *testing.T) {
	truncateProfitTestTables(t)
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() { common.CryptoSecret = originalCryptoSecret })

	plain := "tier(\"secret\", p * 9 + c * 99)"
	version, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     30,
		ModelName:     "secret-model",
		PriceType:     model.ProfitPriceTypeTieredExpr,
		PriceValue:    plain,
		EffectiveFrom: time.Now().Unix(),
		CreatedBy:     1,
	})
	require.NoError(t, err)

	var stored model.ProfitCostPriceVersion
	require.NoError(t, model.DB.Where("id = ?", version.Id).First(&stored).Error)
	assert.NotContains(t, stored.CipherText, "secret")
	assert.NotContains(t, stored.CipherText, "99")

	revealed, err := common.DecryptString(stored.CipherText, stored.Nonce, stored.EncryptionVer)
	require.NoError(t, err)
	assert.Equal(t, plain, revealed)
}

func TestProfitReport_DisabledCostPriceKeepsHistoricalOnly(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 1000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 40, "disabled")
	now := time.Now().Unix()
	version, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     40,
		ModelName:     "disabled-model",
		PriceType:     model.ProfitPriceTypeFixedPrice,
		FixedUnit:     model.ProfitFixedUnitRequest,
		FixedAmount:   0.1,
		EffectiveFrom: now - 100,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	var price model.ProfitCostPrice
	require.NoError(t, model.DB.Where("current_version_id = ?", version.Id).First(&price).Error)
	require.NoError(t, model.DB.Model(&model.ProfitCostPrice{}).Where("id = ?", price.Id).Updates(map[string]interface{}{
		"disabled":    true,
		"disabled_at": now,
	}).Error)

	insertProfitTestLog(t, 1, now-1, 40, "disabled-model", 1000, 0, 0, "")
	insertProfitTestLog(t, 2, now+1, 40, "disabled-model", 1000, 0, 0, "")

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 10,
		EndTimestamp:   now + 10,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), report.Summary.PricedCount)
	assert.Equal(t, int64(1), report.Summary.UnpricedCount)
	assert.InDelta(t, 0.1, report.Summary.Cost, 0.000001)
}
