package service

import (
	"encoding/base64"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/shopspring/decimal"
)

type ProfitRange string

const (
	ProfitRangeDay   ProfitRange = "day"
	ProfitRangeWeek  ProfitRange = "week"
	ProfitRangeMonth ProfitRange = "month"
)

type ProfitQuery struct {
	Range          ProfitRange `json:"range"`
	StartTimestamp int64       `json:"start_timestamp"`
	EndTimestamp   int64       `json:"end_timestamp"`
	ChannelId      int         `json:"channel_id"`
	ModelName      string      `json:"model_name"`
}

type ProfitMetric struct {
	Revenue          float64 `json:"revenue"`
	PricedRevenue    float64 `json:"priced_revenue"`
	UnpricedRevenue  float64 `json:"unpriced_revenue"`
	Cost             float64 `json:"cost"`
	Profit           float64 `json:"profit"`
	ProfitMargin     float64 `json:"profit_margin"`
	CoverageRate     float64 `json:"coverage_rate"`
	RequestCount     int64   `json:"request_count"`
	PricedCount      int64   `json:"priced_count"`
	UnpricedCount    int64   `json:"unpriced_count"`
	ErrorCount       int64   `json:"error_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
}

type ProfitSeriesItem struct {
	PeriodStart int64  `json:"period_start"`
	PeriodLabel string `json:"period_label"`
	ProfitMetric
}

type ProfitBreakdownItem struct {
	ChannelId   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ModelName   string `json:"model_name"`
	ProfitMetric
}

type ProfitReport struct {
	Range      ProfitRange           `json:"range"`
	Start      int64                 `json:"start_timestamp"`
	End        int64                 `json:"end_timestamp"`
	Summary    ProfitMetric          `json:"summary"`
	Series     []ProfitSeriesItem    `json:"series"`
	Breakdown  []ProfitBreakdownItem `json:"breakdown"`
	Generated  int64                 `json:"generated_at"`
	QuotaUnit  float64               `json:"quota_per_unit"`
	Currency   string                `json:"currency"`
	HasFilters bool                  `json:"has_filters"`
}

type ProfitCostPricePrefill struct {
	ChannelId       int      `json:"channel_id"`
	ModelName       string   `json:"model_name"`
	Models          []string `json:"models"`
	PricingMode     string   `json:"pricing_mode"`
	InputPrice      float64  `json:"input_price"`
	CacheReadPrice  float64  `json:"cache_read_price"`
	OutputPrice     float64  `json:"output_price"`
	RequestPrice    float64  `json:"request_price"`
	SecondPrice     float64  `json:"second_price"`
	ModelRatio      float64  `json:"model_ratio,omitempty"`
	CompletionRatio float64  `json:"completion_ratio,omitempty"`
	ModelPrice      float64  `json:"model_price,omitempty"`
	HasPricing      bool     `json:"has_pricing"`
	Note            string   `json:"note,omitempty"`
}

type profitLogOther struct {
	BillingMode             string  `json:"billing_mode"`
	ExprB64                 string  `json:"expr_b64"`
	Seconds                 float64 `json:"seconds"`
	CacheTokens             int     `json:"cache_tokens"`
	CacheCreationTokens     int     `json:"cache_creation_tokens"`
	CacheCreationTokens5m   int     `json:"cache_creation_tokens_5m"`
	CacheCreationTokens1h   int     `json:"cache_creation_tokens_1h"`
	ImageOutput             int     `json:"image_output"`
	AudioInput              int     `json:"audio_input"`
	AudioOutput             int     `json:"audio_output"`
	AudioInputTokenCount    int     `json:"audio_input_token_count"`
	ImageGenerationCall     bool    `json:"image_generation_call"`
	ImageGenerationCallCost float64 `json:"image_generation_call_price"`
}

type profitCostPriceResolver struct {
	versions map[string][]profitCostPriceVersionEntry
}

type profitCostPriceVersionEntry struct {
	version    model.ProfitCostPriceVersion
	disabledAt int64
}

func GetProfitCostPricePrefill(channelId int, modelName string) (*ProfitCostPricePrefill, error) {
	var channel model.Channel
	if err := model.DB.Where("id = ?", channelId).First(&channel).Error; err != nil {
		return nil, err
	}
	models := normalizeProfitChannelModels(channel.GetModels())
	modelName = strings.TrimSpace(modelName)
	if modelName == "" && len(models) > 0 {
		modelName = models[0]
	}
	result := &ProfitCostPricePrefill{
		ChannelId: channelId,
		ModelName: modelName,
		Models:    models,
	}
	if modelName == "" {
		result.Note = "channel has no configured models"
		return result, nil
	}
	if modelPrice, ok := ratio_setting.GetModelPrice(modelName, false); ok {
		result.PricingMode = model.ProfitPriceTypeFixedPrice
		if isProfitSecondBasedFixedModel(modelName) {
			result.SecondPrice = modelPrice
		} else {
			result.RequestPrice = modelPrice
		}
		result.ModelPrice = modelPrice
		result.HasPricing = true
		return result, nil
	}
	modelRatio, ok, _ := ratio_setting.GetModelRatio(modelName)
	if !ok {
		result.Note = "model pricing not found"
		return result, nil
	}
	completionRatio := ratio_setting.GetCompletionRatio(modelName)
	multiplier := 0.0
	if common.QuotaPerUnit > 0 {
		multiplier = 1_000_000 / common.QuotaPerUnit
	}
	result.PricingMode = model.ProfitPriceTypeSimpleToken
	result.InputPrice = modelRatio * multiplier
	if cacheRatio, ok := ratio_setting.GetCacheRatio(modelName); ok {
		result.CacheReadPrice = result.InputPrice * cacheRatio
	}
	result.OutputPrice = modelRatio * completionRatio * multiplier
	result.ModelRatio = modelRatio
	result.CompletionRatio = completionRatio
	result.HasPricing = true
	return result, nil
}

func normalizeProfitChannelModels(models []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(models))
	for _, item := range models {
		modelName := strings.TrimSpace(item)
		if modelName == "" || seen[modelName] {
			continue
		}
		seen[modelName] = true
		result = append(result, modelName)
	}
	sort.Strings(result)
	return result
}

func isProfitSecondBasedFixedModel(modelName string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(modelName))
	if lowerName == "" {
		return false
	}
	if lowerName == "ok-video-1.5-preview" {
		return true
	}
	if strings.HasPrefix(lowerName, "ok-") && strings.Contains(lowerName, "video") {
		return true
	}
	for _, marker := range []string{"video", "sora", "veo", "kling", "pika", "grok"} {
		if strings.Contains(lowerName, marker) {
			return true
		}
	}
	return false
}

func NormalizeProfitQuery(q ProfitQuery) ProfitQuery {
	if q.Range == "" {
		q.Range = ProfitRangeDay
	}
	if q.Range != ProfitRangeDay && q.Range != ProfitRangeWeek && q.Range != ProfitRangeMonth {
		q.Range = ProfitRangeDay
	}
	now := time.Now()
	if q.EndTimestamp <= 0 {
		q.EndTimestamp = now.Unix()
	}
	if q.StartTimestamp <= 0 {
		switch q.Range {
		case ProfitRangeMonth:
			q.StartTimestamp = now.AddDate(0, -6, 0).Unix()
		case ProfitRangeWeek:
			q.StartTimestamp = now.AddDate(0, 0, -12*7).Unix()
		default:
			q.StartTimestamp = now.AddDate(0, 0, -30).Unix()
		}
	}
	if q.StartTimestamp > q.EndTimestamp {
		q.StartTimestamp, q.EndTimestamp = q.EndTimestamp, q.StartTimestamp
	}
	q.ModelName = strings.TrimSpace(q.ModelName)
	return q
}

func GetProfitReport(q ProfitQuery) (*ProfitReport, error) {
	q = NormalizeProfitQuery(q)
	logs, err := queryProfitLogs(q)
	if err != nil {
		return nil, err
	}
	channelNames, _ := loadProfitChannelNames(logs)
	resolver, err := loadProfitCostPriceResolver(logs)
	if err != nil {
		return nil, err
	}
	report := &ProfitReport{
		Range:      q.Range,
		Start:      q.StartTimestamp,
		End:        q.EndTimestamp,
		Generated:  common.GetTimestamp(),
		QuotaUnit:  common.QuotaPerUnit,
		Currency:   "USD",
		HasFilters: q.ChannelId != 0 || q.ModelName != "",
	}
	seriesMap := map[int64]*ProfitSeriesItem{}
	breakdownMap := map[string]*ProfitBreakdownItem{}
	for _, log := range logs {
		metric := calculateLogProfit(log, resolver)
		addProfitMetric(&report.Summary, metric)

		periodStart, periodLabel := profitPeriod(log.CreatedAt, q.Range)
		series := seriesMap[periodStart]
		if series == nil {
			series = &ProfitSeriesItem{PeriodStart: periodStart, PeriodLabel: periodLabel}
			seriesMap[periodStart] = series
		}
		addProfitMetric(&series.ProfitMetric, metric)

		key := profitBreakdownKey(log.ChannelId, log.ModelName)
		item := breakdownMap[key]
		if item == nil {
			item = &ProfitBreakdownItem{
				ChannelId:   log.ChannelId,
				ChannelName: channelNames[log.ChannelId],
				ModelName:   log.ModelName,
			}
			breakdownMap[key] = item
		}
		addProfitMetric(&item.ProfitMetric, metric)
	}
	finalizeProfitMetric(&report.Summary)
	for _, item := range seriesMap {
		finalizeProfitMetric(&item.ProfitMetric)
		report.Series = append(report.Series, *item)
	}
	sort.Slice(report.Series, func(i, j int) bool { return report.Series[i].PeriodStart < report.Series[j].PeriodStart })
	for _, item := range breakdownMap {
		finalizeProfitMetric(&item.ProfitMetric)
		report.Breakdown = append(report.Breakdown, *item)
	}
	sort.Slice(report.Breakdown, func(i, j int) bool {
		if report.Breakdown[i].Profit == report.Breakdown[j].Profit {
			return report.Breakdown[i].Revenue > report.Breakdown[j].Revenue
		}
		return report.Breakdown[i].Profit > report.Breakdown[j].Profit
	})
	return report, nil
}

func queryProfitLogs(q ProfitQuery) ([]model.Log, error) {
	tx := model.LOG_DB.Where("type = ?", model.LogTypeConsume)
	tx = tx.Where("created_at >= ? AND created_at <= ?", q.StartTimestamp, q.EndTimestamp)
	if q.ChannelId != 0 {
		tx = tx.Where("channel_id = ?", q.ChannelId)
	}
	if q.ModelName != "" {
		tx = tx.Where("model_name = ?", q.ModelName)
	}
	var logs []model.Log
	if err := tx.Order("created_at asc, id asc").Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func loadProfitCostPriceResolver(logs []model.Log) (*profitCostPriceResolver, error) {
	resolver := &profitCostPriceResolver{versions: map[string][]profitCostPriceVersionEntry{}}
	if len(logs) == 0 {
		return resolver, nil
	}
	channelIds := make([]int, 0)
	modelNames := make([]string, 0)
	seenChannels := map[int]bool{}
	seenModels := map[string]bool{}
	for _, log := range logs {
		if log.ChannelId != 0 && !seenChannels[log.ChannelId] {
			seenChannels[log.ChannelId] = true
			channelIds = append(channelIds, log.ChannelId)
		}
		modelName := strings.TrimSpace(log.ModelName)
		if modelName != "" && !seenModels[modelName] {
			seenModels[modelName] = true
			modelNames = append(modelNames, modelName)
		}
	}
	if len(channelIds) == 0 || len(modelNames) == 0 {
		return resolver, nil
	}
	var rows []struct {
		model.ProfitCostPriceVersion
		Disabled   bool  `gorm:"column:disabled"`
		DisabledAt int64 `gorm:"column:disabled_at"`
	}
	if err := model.DB.Table("profit_cost_price_versions").
		Select("profit_cost_price_versions.*, profit_cost_prices.disabled, profit_cost_prices.disabled_at").
		Joins("JOIN profit_cost_prices ON profit_cost_prices.id = profit_cost_price_versions.cost_price_id").
		Where("profit_cost_price_versions.channel_id IN ? AND profit_cost_price_versions.model_name IN ?", channelIds, modelNames).
		Order("profit_cost_price_versions.channel_id asc, profit_cost_price_versions.model_name asc, profit_cost_price_versions.effective_from desc, profit_cost_price_versions.id desc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Disabled && row.DisabledAt == 0 {
			continue
		}
		key := profitBreakdownKey(row.ChannelId, row.ModelName)
		resolver.versions[key] = append(resolver.versions[key], profitCostPriceVersionEntry{
			version:    row.ProfitCostPriceVersion,
			disabledAt: row.DisabledAt,
		})
	}
	return resolver, nil
}

func (resolver *profitCostPriceResolver) find(log model.Log) (*model.ProfitCostPriceVersion, bool) {
	if resolver == nil {
		return nil, false
	}
	versions := resolver.versions[profitBreakdownKey(log.ChannelId, log.ModelName)]
	for i := range versions {
		entry := &versions[i]
		if entry.version.EffectiveFrom > log.CreatedAt {
			continue
		}
		if entry.disabledAt != 0 && entry.disabledAt <= log.CreatedAt {
			continue
		}
		return &entry.version, true
	}
	return nil, false
}

func loadProfitChannelNames(logs []model.Log) (map[int]string, error) {
	ids := make([]int, 0)
	seen := map[int]bool{}
	for _, log := range logs {
		if log.ChannelId != 0 && !seen[log.ChannelId] {
			seen[log.ChannelId] = true
			ids = append(ids, log.ChannelId)
		}
	}
	result := map[int]string{}
	if len(ids) == 0 {
		return result, nil
	}
	var channels []struct {
		Id   int
		Name string
	}
	if err := model.DB.Table("channels").Select("id, name").Where("id IN ?", ids).Find(&channels).Error; err != nil {
		return result, err
	}
	for _, channel := range channels {
		result[channel.Id] = channel.Name
	}
	return result, nil
}

func calculateLogProfit(log model.Log, resolver *profitCostPriceResolver) ProfitMetric {
	revenue := quotaToUSD(log.Quota)
	metric := ProfitMetric{
		Revenue:          revenue,
		RequestCount:     1,
		PromptTokens:     int64(log.PromptTokens),
		CompletionTokens: int64(log.CompletionTokens),
	}
	cost, priced, err := calculateLogCostUSD(log, resolver)
	if err != nil {
		metric.ErrorCount = 1
	}
	if !priced {
		metric.UnpricedRevenue = revenue
		metric.UnpricedCount = 1
		return metric
	}
	metric.PricedRevenue = revenue
	metric.PricedCount = 1
	metric.Cost = cost
	metric.Profit = revenue - cost
	return metric
}

func calculateLogCostUSD(log model.Log, resolver *profitCostPriceResolver) (float64, bool, error) {
	version, ok := resolver.find(log)
	if !ok {
		return 0, false, nil
	}
	plain, err := common.DecryptString(version.CipherText, version.Nonce, version.EncryptionVer)
	if err != nil {
		return 0, false, err
	}
	switch version.PriceType {
	case model.ProfitPriceTypeSimpleToken:
		var payload model.ProfitSimpleTokenPrice
		if err := common.UnmarshalJsonStr(plain, &payload); err != nil {
			return 0, false, err
		}
		cost := calculateSimpleTokenCostUSD(log, payload)
		cost += payload.RequestPrice
		cost += profitLogBillableSeconds(log) * payload.SecondPrice
		return cost, true, nil
	case model.ProfitPriceTypeTieredExpr:
		params := buildProfitTokenParams(log, plain)
		cost, _, err := billingexpr.RunExpr(plain, params)
		if err != nil {
			return 0, false, err
		}
		return cost / 1_000_000, true, nil
	case model.ProfitPriceTypeFixedPrice:
		var payload struct {
			Amount float64 `json:"amount"`
			Unit   string  `json:"unit"`
		}
		if err := common.Unmarshal([]byte(plain), &payload); err != nil {
			return 0, false, err
		}
		switch payload.Unit {
		case model.ProfitFixedUnitRequest:
			return payload.Amount, true, nil
		case model.ProfitFixedUnitSecond:
			return payload.Amount * profitLogBillableSeconds(log), true, nil
		default:
			return 0, false, errors.New("unsupported fixed cost unit")
		}
	default:
		return 0, false, errors.New("unsupported profit price type")
	}
}

func calculateSimpleTokenCostUSD(log model.Log, payload model.ProfitSimpleTokenPrice) float64 {
	promptTokens := math.Max(float64(log.PromptTokens), 0)
	if payload.CacheReadPrice <= 0 {
		return (promptTokens*payload.InputPrice + float64(log.CompletionTokens)*payload.OutputPrice) / 1_000_000
	}
	other := profitLogOther{}
	_ = common.UnmarshalJsonStr(log.Other, &other)
	cacheTokens := math.Min(math.Max(float64(other.CacheTokens), 0), promptTokens)
	inputTokens := promptTokens - cacheTokens
	return (inputTokens*payload.InputPrice + cacheTokens*payload.CacheReadPrice + float64(log.CompletionTokens)*payload.OutputPrice) / 1_000_000
}

func profitLogBillableSeconds(log model.Log) float64 {
	other := profitLogOther{}
	_ = common.UnmarshalJsonStr(log.Other, &other)
	if other.Seconds > 0 {
		return other.Seconds
	}
	return math.Max(float64(log.UseTime), 0)
}

func buildProfitTokenParams(log model.Log, expr string) billingexpr.TokenParams {
	other := profitLogOther{}
	_ = common.UnmarshalJsonStr(log.Other, &other)

	usage := &dto.Usage{
		PromptTokens:     log.PromptTokens,
		CompletionTokens: log.CompletionTokens,
		TotalTokens:      log.PromptTokens + log.CompletionTokens,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         other.CacheTokens,
			CachedCreationTokens: other.CacheCreationTokens,
			AudioTokens:          firstNonZero(other.AudioInput, other.AudioInputTokenCount),
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			AudioTokens: other.AudioOutput,
			ImageTokens: other.ImageOutput,
		},
		ClaudeCacheCreation5mTokens: other.CacheCreationTokens5m,
		ClaudeCacheCreation1hTokens: other.CacheCreationTokens1h,
	}
	if usage.PromptTokensDetails.CachedCreationTokens == 0 {
		usage.PromptTokensDetails.CachedCreationTokens = other.CacheCreationTokens5m
	}
	if other.ExprB64 != "" {
		if decoded, err := base64.StdEncoding.DecodeString(other.ExprB64); err == nil && strings.TrimSpace(string(decoded)) != "" {
			other.BillingMode = "tiered_expr"
		}
	}
	isClaude := other.BillingMode == "tiered_expr" && strings.Contains(strings.ToLower(log.Other), `"claude":true`)
	return BuildTieredTokenParams(usage, isClaude, billingexpr.UsedVars(expr))
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func quotaToUSD(quota int) float64 {
	if common.QuotaPerUnit <= 0 {
		return 0
	}
	value, _ := decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Float64()
	return value
}

func addProfitMetric(target *ProfitMetric, delta ProfitMetric) {
	target.Revenue += delta.Revenue
	target.PricedRevenue += delta.PricedRevenue
	target.UnpricedRevenue += delta.UnpricedRevenue
	target.Cost += delta.Cost
	target.Profit += delta.Profit
	target.RequestCount += delta.RequestCount
	target.PricedCount += delta.PricedCount
	target.UnpricedCount += delta.UnpricedCount
	target.ErrorCount += delta.ErrorCount
	target.PromptTokens += delta.PromptTokens
	target.CompletionTokens += delta.CompletionTokens
}

func finalizeProfitMetric(metric *ProfitMetric) {
	if metric.PricedRevenue > 0 {
		metric.ProfitMargin = metric.Profit / metric.PricedRevenue
	}
	if metric.RequestCount > 0 {
		metric.CoverageRate = float64(metric.PricedCount) / float64(metric.RequestCount)
	}
}

func profitPeriod(ts int64, r ProfitRange) (int64, string) {
	t := time.Unix(ts, 0).Local()
	switch r {
	case ProfitRangeMonth:
		start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
		return start.Unix(), start.Format("2006-01")
	case ProfitRangeWeek:
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, -(weekday - 1))
		return start.Unix(), start.Format("2006-01-02")
	default:
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		return start.Unix(), start.Format("2006-01-02")
	}
}

func profitBreakdownKey(channelId int, modelName string) string {
	return strings.TrimSpace(modelName) + "|" + common.GetJsonString(channelId)
}
