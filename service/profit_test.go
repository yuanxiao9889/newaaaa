package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
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

func TestProfitReport_FullTaskRefundRemovesRevenueAndCost(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 500000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 24, "refunded-video")
	now := time.Now().Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     24,
		ModelName:     "refunded-video-model",
		PriceType:     model.ProfitPriceTypeFixedPrice,
		FixedUnit:     model.ProfitFixedUnitRequest,
		FixedAmount:   0.4,
		EffectiveFrom: now - 10,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	for id := 1; id <= 4; id++ {
		insertProfitTestLog(t, id, now, 24, "refunded-video-model", 650000, 0, 0, "")
	}
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		Id:        5,
		CreatedAt: now,
		Type:      model.LogTypeRefund,
		ChannelId: 24,
		ModelName: "refunded-video-model",
		Quota:     650000,
		Other:     `{"task_id":"task_refunded"}`,
	}).Error)

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 1,
		EndTimestamp:   now + 1,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(3), report.Summary.RequestCount)
	assert.Equal(t, int64(3), report.Summary.PricedCount)
	assert.InDelta(t, 3.9, report.Summary.Revenue, 0.000001)
	assert.InDelta(t, 3.9, report.Summary.PricedRevenue, 0.000001)
	assert.InDelta(t, 1.2, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 2.7, report.Summary.Profit, 0.000001)
	assert.Equal(t, 1.0, report.Summary.CoverageRate)
}

func TestProfitReport_PartialRefundOnlyAdjustsRevenue(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCryptoSecret := common.CryptoSecret
	common.QuotaPerUnit = 500000
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		common.CryptoSecret = originalCryptoSecret
	})

	insertProfitTestChannel(t, 28, "adjusted-video")
	now := time.Now().Unix()
	_, err := model.UpsertProfitCostPrice(model.ProfitCostPriceInput{
		ChannelId:     28,
		ModelName:     "adjusted-video-model",
		PriceType:     model.ProfitPriceTypeFixedPrice,
		FixedUnit:     model.ProfitFixedUnitRequest,
		FixedAmount:   0.4,
		EffectiveFrom: now - 10,
		CreatedBy:     1,
	})
	require.NoError(t, err)

	insertProfitTestLog(t, 1, now, 28, "adjusted-video-model", 650000, 0, 0, "")
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		Id:        2,
		CreatedAt: now,
		Type:      model.LogTypeRefund,
		ChannelId: 28,
		ModelName: "adjusted-video-model",
		Quota:     150000,
		Other:     `{"task_id":"task_adjusted","pre_consumed_quota":650000,"actual_quota":500000}`,
	}).Error)

	report, err := GetProfitReport(ProfitQuery{
		Range:          ProfitRangeDay,
		StartTimestamp: now - 1,
		EndTimestamp:   now + 1,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), report.Summary.RequestCount)
	assert.Equal(t, int64(1), report.Summary.PricedCount)
	assert.InDelta(t, 1.0, report.Summary.Revenue, 0.000001)
	assert.InDelta(t, 0.4, report.Summary.Cost, 0.000001)
	assert.InDelta(t, 0.6, report.Summary.Profit, 0.000001)
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

func TestCalculateSimpleTokenCostUSDIncludesAllUsageDimensionsWithoutDoubleCharging(t *testing.T) {
	price := model.ProfitSimpleTokenPrice{
		InputPrice:         1,
		CacheReadPrice:     0.1,
		CacheCreationPrice: 2,
		ImageInputPrice:    3,
		AudioInputPrice:    4,
		OutputPrice:        5,
		ImageOutputPrice:   6,
		AudioOutputPrice:   7,
	}
	log := model.Log{
		PromptTokens:     1000,
		CompletionTokens: 500,
		Other: `{
			"cache_tokens":100,
			"cache_creation_tokens":200,
			"image_input":150,
			"audio_input":50,
			"image_output":80,
			"audio_output":20
		}`,
	}

	cost := calculateSimpleTokenCostUSD(log, price)

	// Text input = 1000 - 100 - 200 - 150 - 50 = 500.
	// Text output = 500 - 80 - 20 = 400.
	assert.InDelta(t, 0.00418, cost, 0.000000001)
}

func TestCalculateSimpleTokenCostUSDUsesClaudeCacheSemanticsAndSplitCreationPrices(t *testing.T) {
	price := model.ProfitSimpleTokenPrice{
		InputPrice:           1,
		CacheReadPrice:       0.1,
		CacheCreationPrice:   2,
		CacheCreation1hPrice: 3,
	}
	log := model.Log{
		PromptTokens: 1000,
		Other: `{
			"claude":true,
			"cache_tokens":200,
			"cache_creation_tokens":500,
			"cache_creation_tokens_5m":300,
			"cache_creation_tokens_1h":200
		}`,
	}

	cost := calculateSimpleTokenCostUSD(log, price)

	// Claude prompt_tokens is already text-only and must not have cache removed again.
	assert.InDelta(t, 0.00222, cost, 0.000000001)
}

func TestCalculateSimpleTokenCostUSDReadsLegacyImageAndTaskUsageDetails(t *testing.T) {
	price := model.ProfitSimpleTokenPrice{
		InputPrice:       1,
		ImageInputPrice:  3,
		OutputPrice:      5,
		ImageOutputPrice: 6,
	}

	t.Run("legacy image_output is treated as prompt image input", func(t *testing.T) {
		log := model.Log{
			PromptTokens: 1000,
			Other:        `{"image":true,"image_output":200}`,
		}

		assert.InDelta(t, 0.0014, calculateSimpleTokenCostUSD(log, price), 0.000000001)
	})

	t.Run("async task usage details preserve input and output image tokens", func(t *testing.T) {
		log := model.Log{
			PromptTokens:     1000,
			CompletionTokens: 500,
			Other: `{
				"usage_details":{
					"prompt_tokens":1000,
					"completion_tokens":500,
					"prompt_tokens_details":{"image_tokens":200},
					"completion_tokens_details":{"image_tokens":100}
				}
			}`,
		}

		// Text input 800 + image input 600 + text output 2000 + image output 600.
		assert.InDelta(t, 0.004, calculateSimpleTokenCostUSD(log, price), 0.000000001)
	})
}

func TestCalculateSimpleTokenCostUSDKeepsLegacyCostPayloadCompatible(t *testing.T) {
	var price model.ProfitSimpleTokenPrice
	require.NoError(t, common.UnmarshalJsonStr(`{"input_price":1,"output_price":2}`, &price))
	log := model.Log{PromptTokens: 1000, CompletionTokens: 500}

	assert.InDelta(t, 0.002, calculateSimpleTokenCostUSD(log, price), 0.000000001)
}

func TestProfitCostPricePrefillUsesChannelModelsAndCurrentPricing(t *testing.T) {
	truncateProfitTestTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	originalModelRatio := ratio_setting.ModelRatio2JSONString()
	originalCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	originalModelPrice := ratio_setting.ModelPrice2JSONString()
	originalFixedPriceUnits := billing_setting.GetFixedPriceUnitCopy()
	originalFixedPriceUnitsJSON, err := common.Marshal(originalFixedPriceUnits)
	require.NoError(t, err)
	common.QuotaPerUnit = 500000
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModelRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(originalCompletionRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(originalModelPrice))
		require.NoError(t, config.UpdateConfigFromMap(config.GlobalConfig.Get("billing_setting"), map[string]string{
			billing_setting.FixedPriceUnitField: string(originalFixedPriceUnitsJSON),
		}))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"profit-prefill-model":1.25}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"profit-prefill-model":4}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"ok-video-1.5-preview":1.3,"profit-fixed-model":0.07,"sora-profit-video":0.2}`))
	require.NoError(t, config.UpdateConfigFromMap(config.GlobalConfig.Get("billing_setting"), map[string]string{
		billing_setting.FixedPriceUnitField: `{"ok-video-1.5-preview":"request"}`,
	}))
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     26,
		Name:   "prefill",
		Models: "ok-video-1.5-preview,profit-fixed-model, profit-prefill-model,profit-prefill-model,sora-profit-video",
	}).Error)

	prefill, err := GetProfitCostPricePrefill(26, "profit-prefill-model")
	require.NoError(t, err)
	require.Equal(t, []string{"ok-video-1.5-preview", "profit-fixed-model", "profit-prefill-model", "sora-profit-video"}, prefill.Models)
	require.Equal(t, model.ProfitPriceTypeSimpleToken, prefill.PricingMode)
	require.True(t, prefill.HasPricing)
	assert.InDelta(t, 2.5, prefill.InputPrice, 0.000001)
	assert.InDelta(t, 2.5, prefill.CacheReadPrice, 0.000001)
	assert.InDelta(t, 3.125, prefill.CacheCreationPrice, 0.000001)
	assert.InDelta(t, 5.0, prefill.CacheCreation1hPrice, 0.000001)
	assert.InDelta(t, 2.5, prefill.ImageInputPrice, 0.000001)
	assert.InDelta(t, 10.0, prefill.ImageOutputPrice, 0.000001)
	assert.InDelta(t, 2.5, prefill.AudioInputPrice, 0.000001)
	assert.InDelta(t, 2.5, prefill.AudioOutputPrice, 0.000001)
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

	okVideoPrefill, err := GetProfitCostPricePrefill(26, "ok-video-1.5-preview")
	require.NoError(t, err)
	require.Equal(t, model.ProfitPriceTypeFixedPrice, okVideoPrefill.PricingMode)
	assert.InDelta(t, 1.3, okVideoPrefill.RequestPrice, 0.000001)
	assert.Zero(t, okVideoPrefill.SecondPrice)
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
	// Random base64 ciphertext may contain short substrings such as "99".
	assert.NotEmpty(t, stored.CipherText)
	assert.NotEqual(t, plain, stored.CipherText)

	revealed, err := common.DecryptString(stored.CipherText, stored.Nonce, stored.EncryptionVer)
	require.NoError(t, err)
	assert.Equal(t, plain, revealed)
}

func TestProfitCostPricePersistsAndListsEverySimpleTokenDimension(t *testing.T) {
	truncateProfitTestTables(t)
	originalCryptoSecret := common.CryptoSecret
	common.CryptoSecret = "profit-test-secret"
	t.Cleanup(func() { common.CryptoSecret = originalCryptoSecret })

	insertProfitTestChannel(t, 31, "multimodal-cost")
	input := model.ProfitCostPriceInput{
		ChannelId:            31,
		ModelName:            "multimodal-cost-model",
		PriceType:            model.ProfitPriceTypeSimpleToken,
		InputPrice:           1,
		CacheReadPrice:       2,
		CacheCreationPrice:   3,
		CacheCreation1hPrice: 4,
		ImageInputPrice:      5,
		ImageOutputPrice:     6,
		AudioInputPrice:      7,
		AudioOutputPrice:     8,
		OutputPrice:          9,
		RequestPrice:         10,
		SecondPrice:          11,
		EffectiveFrom:        time.Now().Unix(),
		CreatedBy:            1,
	}

	version, err := model.UpsertProfitCostPrice(input)
	require.NoError(t, err)
	assert.Equal(t, input.CacheCreationPrice, version.CacheCreationPrice)
	assert.Equal(t, input.CacheCreation1hPrice, version.CacheCreation1hPrice)
	assert.Equal(t, input.ImageInputPrice, version.ImageInputPrice)
	assert.Equal(t, input.ImageOutputPrice, version.ImageOutputPrice)
	assert.Equal(t, input.AudioInputPrice, version.AudioInputPrice)
	assert.Equal(t, input.AudioOutputPrice, version.AudioOutputPrice)

	items, err := model.ListProfitCostPrices(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	assert.Equal(t, input.InputPrice, item.InputPrice)
	assert.Equal(t, input.CacheReadPrice, item.CacheReadPrice)
	assert.Equal(t, input.CacheCreationPrice, item.CacheCreationPrice)
	assert.Equal(t, input.CacheCreation1hPrice, item.CacheCreation1hPrice)
	assert.Equal(t, input.ImageInputPrice, item.ImageInputPrice)
	assert.Equal(t, input.ImageOutputPrice, item.ImageOutputPrice)
	assert.Equal(t, input.AudioInputPrice, item.AudioInputPrice)
	assert.Equal(t, input.AudioOutputPrice, item.AudioOutputPrice)
	assert.Equal(t, input.OutputPrice, item.OutputPrice)
	assert.Equal(t, input.RequestPrice, item.RequestPrice)
	assert.Equal(t, input.SecondPrice, item.SecondPrice)
	assert.Contains(t, item.PriceSummary, "cache create 1h $4")
	assert.Contains(t, item.PriceSummary, "image input $5")
	assert.Contains(t, item.PriceSummary, "audio output $8")

	var payload model.ProfitSimpleTokenPrice
	require.NoError(t, common.UnmarshalJsonStr(item.PriceValue, &payload))
	assert.Equal(t, input.CacheCreationPrice, payload.CacheCreationPrice)
	assert.Equal(t, input.ImageOutputPrice, payload.ImageOutputPrice)
	assert.Equal(t, input.AudioInputPrice, payload.AudioInputPrice)
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

func newProfitCostTestResolver(t testing.TB, priceType, plain string) *profitCostPriceResolver {
	t.Helper()
	cipherText, nonce, version, err := common.EncryptString(plain)
	require.NoError(t, err)
	return &profitCostPriceResolver{versions: map[string][]profitCostPriceVersionEntry{
		profitBreakdownKey(10, "cost-model"): {{version: model.ProfitCostPriceVersion{
			Id: 1, ChannelId: 10, ModelName: "cost-model", PriceType: priceType,
			CipherText: cipherText, Nonce: nonce, EncryptionVer: version, EffectiveFrom: 1,
		}}},
	}}
}

func TestProfitCostResolverReusesPricesWithoutReusingUsage(t *testing.T) {
	for _, tc := range []struct {
		name, priceType, plain string
		first, second          float64
	}{
		{"token", model.ProfitPriceTypeSimpleToken, `{"input_price":2,"output_price":4}`, .004, .008},
		{"per request", model.ProfitPriceTypeFixedPrice, `{"amount":0.2,"unit":"request"}`, .2, .2},
		{"per second", model.ProfitPriceTypeFixedPrice, `{"amount":0.2,"unit":"second"}`, .4, .8},
		{"expression", model.ProfitPriceTypeTieredExpr, `tier("base", p * 2 + c * 4)`, .004, .008},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newProfitCostTestResolver(t, tc.priceType, tc.plain)
			log := model.Log{ChannelId: 10, ModelName: "cost-model", CreatedAt: 10, PromptTokens: 1000, CompletionTokens: 500, UseTime: 2}
			first, priced, err := calculateLogCostUSD(log, resolver)
			require.NoError(t, err)
			require.True(t, priced)
			assert.InDelta(t, tc.first, first, 1e-12)
			log.PromptTokens = 2000
			log.CompletionTokens = 1000
			log.UseTime = 4
			second, priced, err := calculateLogCostUSD(log, resolver)
			require.NoError(t, err)
			require.True(t, priced)
			assert.InDelta(t, tc.second, second, 1e-12)
		})
	}
}

func TestProfitCostResolverCountsEveryInvalidPriceUsage(t *testing.T) {
	resolver := newProfitCostTestResolver(t, model.ProfitPriceTypeSimpleToken, `invalid-json`)
	log := model.Log{Type: model.LogTypeConsume, ChannelId: 10, ModelName: "cost-model", CreatedAt: 10, Quota: 1}
	for range 2 {
		metric := calculateLogProfit(log, resolver)
		assert.Equal(t, int64(1), metric.ErrorCount)
		assert.Equal(t, int64(1), metric.UnpricedCount)
		assert.Zero(t, metric.PricedCount)
	}
	// A newly loaded report must use its own prices, including after a correction.
	corrected := newProfitCostTestResolver(t, model.ProfitPriceTypeFixedPrice, `{"amount":0.2,"unit":"request"}`)
	metric := calculateLogProfit(log, corrected)
	assert.Zero(t, metric.ErrorCount)
	assert.Equal(t, int64(1), metric.PricedCount)
	assert.Equal(t, .2, metric.Cost)
}

func BenchmarkProfitCostToken(b *testing.B) {
	resolver := newProfitCostTestResolver(b, model.ProfitPriceTypeSimpleToken, `{"input_price":2,"output_price":4,"cache_read_price":0.2}`)
	log := model.Log{ChannelId: 10, ModelName: "cost-model", CreatedAt: 10, PromptTokens: 1000, CompletionTokens: 500, Other: `{"cache_tokens":400}`}
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := calculateLogCostUSD(log, resolver)
		if err != nil {
			b.Fatal(err)
		}
	}
}
