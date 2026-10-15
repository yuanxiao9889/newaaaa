package service

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// ImageTokenReservationQuota adds a per-image risk reserve without changing
// token prices, usage quantities, or the missing-usage charge estimate.
func ImageTokenReservationQuota(info *relaycommon.RelayInfo, estimatedQuota, count int) (int, error) {
	if count < 1 || count > dto.MaxImageN {
		return 0, fmt.Errorf("image_count must be between 1 and %d", dto.MaxImageN)
	}
	if info.PriceData.UsePrice || info.PriceData.GroupRatioInfo.GroupRatio == 0 {
		return estimatedQuota, nil
	}
	if snap := info.TieredBillingSnapshot; snap != nil {
		if snap.EstimatedBillingUnit == billingexpr.BillingUnitRequest {
			return estimatedQuota, nil
		}
	} else if info.PriceData.ModelRatio == 0 {
		return estimatedQuota, nil
	}
	minimum := info.ImageTokenReserveUSD
	if minimum == 0 {
		minimum = operation_setting.GetQuotaSetting().ImageTokenPreConsumeUSD
	}
	if minimum <= 0 || math.IsNaN(minimum) || math.IsInf(minimum, 0) {
		return 0, fmt.Errorf("image token pre-consume amount must be finite and greater than zero")
	}
	floor, err := common.QuotaFromFloatStrict(minimum * float64(count) * common.QuotaPerUnit * info.PriceData.GroupRatioInfo.GroupRatio)
	if err != nil {
		return 0, err
	}
	info.ImageTokenFallbackQuota = &estimatedQuota
	info.ImageTokenReserveUSD = minimum
	return max(estimatedQuota, floor), nil
}

// PrepareImageBillingForRequest reserves the effective outbound image quantity
// before each attempt, including channel retries and parameter overrides. The
// client request body stays frozen; only the independent quantity is refreshed.
func PrepareImageBillingForRequest(c *gin.Context, info *relaycommon.RelayInfo, count int) *types.NewAPIError {
	if count < 1 || count > dto.MaxImageN {
		return types.NewErrorWithStatusCode(fmt.Errorf("image_count must be an integer between 1 and %d", dto.MaxImageN), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.ImageRequestCount = count
	if c.GetBool("async_image_worker") && info.ImageTokenFallbackQuota == nil {
		// Jobs accepted before this policy, and fixed-price jobs, keep their
		// existing reservation. Only newly reserved token images use the floor.
		return nil
	}
	var quota int
	var err error
	if snap := info.TieredBillingSnapshot; snap != nil && snap.BillingMode == "tiered_expr" {
		if snap.EstimatedImageCount == nil {
			// Quantity scales the reservation floor, never token settlement.
			quota, err = billingexpr.QuotaRoundStrict(snap.EstimatedQuotaBeforeGroup * info.PriceData.GroupRatioInfo.GroupRatio)
		} else {
			request := billingexpr.RequestInput{}
			if info.BillingRequestInput != nil {
				request = *info.BillingRequestInput
			}
			request.ImageCount = &count
			cost, trace, runErr := billingexpr.RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, billingexpr.TokenParams{
				P: float64(snap.EstimatedPromptTokens), C: float64(snap.EstimatedCompletionTokens), Len: float64(snap.EstimatedPromptTokens),
			}, request)
			if runErr != nil {
				return types.NewErrorWithStatusCode(runErr, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			beforeGroup := cost / 1_000_000 * snap.QuotaPerUnit
			if trace.BillingUnit != billingexpr.BillingUnitRequest && snap.PreConsumeMultiplier != 0 {
				beforeGroup *= snap.PreConsumeMultiplier
			}
			quota, err = billingexpr.QuotaRoundStrict(beforeGroup * info.PriceData.GroupRatioInfo.GroupRatio)
			if err == nil {
				snap.EstimatedImageCount = trace.ImageCount
				snap.EstimatedQuotaBeforeGroup = beforeGroup
				snap.EstimatedQuotaAfterGroup = quota
				snap.EstimatedTier = trace.MatchedTier
				snap.EstimatedBillingUnit = trace.BillingUnit
				snap.EstimatedFixedPrice = trace.FixedPrice
				snap.GroupRatio = info.PriceData.GroupRatioInfo.GroupRatio
			}
		}
	} else {
		quantity := 1
		if info.PriceData.UsePrice {
			quantity = count
		}
		// Overwrite the per-attempt ratio so a failed attempt cannot leak its
		// quantity into another channel.
		info.PriceData.AddOtherRatio("n", float64(quantity))
		base := info.ImageQuotaBeforeGroup
		if info.PriceData.UsePrice {
			base = info.PriceData.ModelPrice * common.QuotaPerUnit
		}
		quota, err = common.QuotaFromFloatStrict(info.PriceData.ApplyOtherRatiosToFloat(base * info.PriceData.GroupRatioInfo.GroupRatio))
	}
	if err == nil {
		quota, err = ImageTokenReservationQuota(info, quota, count)
	}
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.PriceData.QuotaToPreConsume = quota
	if c.GetBool("async_image_worker") {
		// The durable task already owns its reservation. Do not open another
		// billing session or submit an override exceeding that reservation.
		if quota > info.FinalPreConsumedQuota {
			return types.NewErrorWithStatusCode(fmt.Errorf("outbound image request exceeds reserved quota"), types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
		}
		return nil
	}
	if quota == 0 && info.Billing == nil {
		return nil
	}
	info.PriceData.FreeModel = false
	if info.Billing == nil {
		return PreConsumeBilling(c, quota, info)
	}
	if err := info.Billing.Reserve(quota); err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	return nil
}

// NormalizeOpenAIImageUsage maps the OpenAI Images usage shape (input_tokens /
// output_tokens / input_tokens_details / output_tokens_details) onto the
// canonical prompt/completion fields for synchronous and asynchronous images.
// Non-zero input/output counts take precedence over compatibility aliases;
// they are never added together. Chat/embedding usage has different semantics.
func NormalizeOpenAIImageUsage(usage *dto.Usage) {
	if usage == nil {
		return
	}
	if usage.InputTokens != 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	if usage.InputTokensDetails != nil {
		usage.PromptTokensDetails = usage.InputTokensDetails.Clone()
	}
	if usage.OutputTokensDetails != nil {
		usage.CompletionTokenDetails = *usage.OutputTokensDetails
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
}
