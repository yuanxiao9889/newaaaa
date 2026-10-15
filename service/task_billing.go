package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"maps"
	"math"
	"strings"
	"time"
)

const (
	TaskBillingStatePending  = "pending"
	TaskBillingStateSettled  = "settled"
	TaskBillingStateRefunded = "refunded"
)

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) error {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) && !info.PriceData.UsePrice {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		var contents []string
		if otherRatios := info.PriceData.OtherRatios(); len(otherRatios) > 0 {
			for key, ra := range otherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
		}
		if snap := info.TieredBillingSnapshot; snap != nil {
			for key, value := range snap.UsageFacts {
				contents = append(contents, fmt.Sprintf("%s: %v", key, value))
			}
		}
		if len(contents) > 0 {
			logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
		}
	}
	other := model.NewLogOther()
	other.SetPublic("is_task", true)
	if info.TaskRelayInfo != nil && info.TaskRelayInfo.PublicTaskID != "" {
		other.SetPublic("task_id", info.TaskRelayInfo.PublicTaskID)
	}
	other.SetPublic("request_path", c.Request.URL.Path)
	if taskDeliveredInline(c, task) {
		other.SetPublic("task_sync", true)
	}
	other.SetPublic("model_price", info.PriceData.ModelPrice)
	if info.PriceData.FixedPriceUnit != "" {
		other.SetPublic("fixed_price_unit", info.PriceData.FixedPriceUnit)
	}
	if info.PriceData.ModelRatio > 0 {
		other.SetPublic("model_ratio", info.PriceData.ModelRatio)
	}
	other.SetPublic("group_ratio", info.PriceData.GroupRatioInfo.GroupRatio)
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other.SetPublic("user_group_ratio", info.PriceData.GroupRatioInfo.GroupSpecialRatio)
	}
	if info.IsModelMapped {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", info.UpstreamModelName)
	}
	if snap := info.TieredBillingSnapshot; snap != nil {
		other.SetPublic("billing_mode", "tiered_expr")
		other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
		other.SetPublic("matched_tier", snap.EstimatedTier)
		if len(snap.UsageFacts) > 0 {
			other.SetPublic("usage_facts", snap.UsageFacts)
		}
		setTaskImageCount(other, snap.UsageFacts["image_count"])
	} else {
		setTaskImageCount(other, info.PriceData.OtherRatios()["image_count"])
	}
	appendTaskLogInfo(task, other)
	attachQuotaSaturation(c, info, other)
	var promptTokens, completionTokens int
	if task != nil {
		promptTokens, completionTokens = task.PrivateData.PromptTokens, task.PrivateData.CompletionTokens
		if task.PrivateData.TotalTokens > 0 {
			other.SetPublic("total_tokens", task.PrivateData.TotalTokens)
			other.SetPublic("usage_details", task.PrivateData.UsageDetails)
		}
	}
	if task != nil && task.ID > 0 {
		previous := task.PrivateData
		task.PrivateData.UsageAccountingMode = model.TaskUsageAccountingSubmit
		task.PrivateData.TokenUsageLogged = task.PrivateData.TotalTokens > 0 || promptTokens > 0 || completionTokens > 0
		if err := model.ApplyTaskBillingAccounting(task, info.PriceData.Quota, 1); err != nil {
			task.PrivateData = previous
			return err
		}
	} else if info.ForcePreConsume {
		if err := model.AdjustTaskUsageAccounting(info.UserId, info.ChannelId, info.PriceData.Quota, 1); err != nil {
			return err
		}
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
		model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
	}
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ChannelId:        info.ChannelId,
		ModelName:        info.OriginModelName,
		TokenName:        tokenName,
		Quota:            info.PriceData.Quota,
		Content:          logContent,
		TokenId:          info.TokenId,
		Group:            info.UsingGroup,
		Other:            other,
	})
	return nil
}

// resolveTokenKey obtains a token key at runtime for legacy task accounting.
// Token keys are intentionally not persisted in task private data.
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	if tokenId <= 0 {
		return ""
	}
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("get token key failed (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包或订阅），delta > 0 表示扣费，delta < 0 表示退还。
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		return model.PostConsumeUserSubscriptionDelta(task.PrivateData.SubscriptionId, int64(delta))
	}
	return model.AdjustUserQuotaImmediately(task.UserId, -delta)
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) error {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return nil
	}
	token, err := model.GetTokenById(task.PrivateData.TokenId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		logger.LogWarn(ctx, fmt.Sprintf("token was deleted, skip token quota adjustment (tokenId=%d, task=%s)", task.PrivateData.TokenId, task.TaskID))
		return nil
	}
	if err != nil {
		return fmt.Errorf("get token for quota adjustment failed (tokenId=%d, task=%s): %w", task.PrivateData.TokenId, task.TaskID, err)
	}
	if token.Key == "" {
		return fmt.Errorf("token key is empty (tokenId=%d, task=%s)", task.PrivateData.TokenId, task.TaskID)
	}
	if delta > 0 {
		err = model.DecreaseTokenQuotaImmediately(task.PrivateData.TokenId, token.Key, delta)
	} else {
		err = model.IncreaseTokenQuotaImmediately(task.PrivateData.TokenId, token.Key, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("adjust token quota failed (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
		return err
	}
	return nil
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) *model.LogOther {
	other := model.NewLogOther()
	if delivery := AsyncImageDeliverySnapshot(task.TaskID, task.PrivateData.WorkerAttempts); len(delivery) > 0 {
		other.SetAdmin("upstream_delivery", delivery)
	}
	if bc := task.PrivateData.BillingContext; bc != nil {
		other.SetPublic("model_price", bc.ModelPrice)
		if bc.FixedPriceUnit != "" {
			other.SetPublic("fixed_price_unit", bc.FixedPriceUnit)
		}
		if bc.ModelRatio > 0 {
			other.SetPublic("model_ratio", bc.ModelRatio)
		}
		other.SetPublic("group_ratio", bc.GroupRatio)
		if priceData := taskBillingContextPriceData(bc); priceData != nil {
			for k, v := range priceData.OtherRatios() {
				if !other.SetPublic(k, v) {
					common.SysError("task billing other ratio key rejected: " + k)
				}
			}
		}
		if snap := bc.TieredSnapshot; snap != nil {
			other.SetPublic("billing_mode", "tiered_expr")
			other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
			other.SetPublic("matched_tier", snap.EstimatedTier)
			if len(snap.UsageFacts) > 0 {
				other.SetPublic("usage_facts", snap.UsageFacts)
			}
			setTaskImageCount(other, snap.UsageFacts["image_count"])
		} else if priceData := taskBillingContextPriceData(bc); priceData != nil {
			setTaskImageCount(other, priceData.OtherRatios()["image_count"])
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", props.UpstreamModelName)
	}
	if len(task.PrivateData.ChannelRetryPath) > 0 {
		retryPath := append([]string(nil), task.PrivateData.ChannelRetryPath...)
		other.SetAdmin("async_channel_retry_path", retryPath)
		if retry := task.PrivateData.ImageRetry; retry != nil && len(retry.Details) > 0 {
			other.SetAdmin("async_channel_retry_details", retry.Details)
		}
		if retry := task.PrivateData.VideoRetry; retry != nil {
			if len(retry.Details) > 0 {
				other.SetAdmin("async_channel_retry_details", retry.Details)
			}
			if len(retry.QuotaSaturation) > 0 {
				other.SetAdmin("quota_saturation", retry.QuotaSaturation)
			}
		}
		other.SetAdmin("use_channel", retryPath)
	}
	if task.PrivateData.PromptTokens > 0 {
		other.SetPublic("prompt_tokens", task.PrivateData.PromptTokens)
	}
	if task.PrivateData.CompletionTokens > 0 {
		other.SetPublic("completion_tokens", task.PrivateData.CompletionTokens)
	}
	if task.PrivateData.TotalTokens > 0 {
		other.SetPublic("total_tokens", task.PrivateData.TotalTokens)
	}
	if task.PrivateData.UsageDetails != nil {
		usageDetails := task.PrivateData.UsageDetails
		other.SetPublic("usage_details", usageDetails)
		if usageDetails.PromptTokensDetails.CachedTokens > 0 {
			other.SetPublic("cache_tokens", usageDetails.PromptTokensDetails.CachedTokens)
		}
		if cacheCreationTokens := usageDetails.PromptTokensDetails.CacheCreationTokensTotal(); cacheCreationTokens > 0 {
			other.SetPublic("cache_creation_tokens", cacheCreationTokens)
		}
		if usageDetails.PromptTokensDetails.ImageTokens > 0 || usageDetails.CompletionTokenDetails.ImageTokens > 0 {
			other.SetPublic("image", true)
			other.SetPublic("image_input", usageDetails.PromptTokensDetails.ImageTokens)
		}
		if usageDetails.CompletionTokenDetails.ImageTokens > 0 {
			other.SetPublic("image_output", usageDetails.CompletionTokenDetails.ImageTokens)
		}
		if usageDetails.PromptTokensDetails.AudioTokens > 0 {
			other.SetPublic("audio_input", usageDetails.PromptTokensDetails.AudioTokens)
		}
		if usageDetails.CompletionTokenDetails.AudioTokens > 0 {
			other.SetPublic("audio_output", usageDetails.CompletionTokenDetails.AudioTokens)
		}
	}
	appendTaskLogInfo(task, other)
	return other
}

// setTaskImageCount publishes the billed image quantity of an image task as
// other.image_count, the same field the HTTP image relay writes, so the log
// detail shows one "billable image count" regardless of the serving path. The
// value is a host-validated count fact (at most dto.MaxImageN), never a quota.
func setTaskImageCount(other *model.LogOther, value any) {
	count, ok := value.(float64)
	if !ok || count < 0 || count > float64(kitdto.MaxImageN) {
		return
	}
	other.SetPublic("image_count", common.QuotaRound(count))
}

// taskDeliveredInline reports whether the submitting HTTP request itself
// returns the task deliverable: the upstream completed immediately, or the
// request came through the synchronous OpenAI Images protocol, where the host
// waits for an asynchronous upstream task before answering. The usage log
// presents such requests as synchronous instead of as asynchronous jobs.
func taskDeliveredInline(c *gin.Context, task *model.Task) bool {
	if task != nil && (task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure) {
		return true
	}
	pinnedValue, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	if !exists {
		return false
	}
	pinned, ok := pinnedValue.(jsplugin.PinnedEndpoint)
	return ok && pinned.Protocol == jsplugin.ProtocolOpenAIImage
}

func appendTaskLogInfo(task *model.Task, other *model.LogOther) {
	if task == nil || other == nil {
		return
	}
	if task.TaskID != "" {
		other.SetPublic("task_id", task.TaskID)
	}
	if task.PrivateData.ResultDiscarded {
		// The result was delivered inline and the upstream snapshot was not
		// persisted, so no artifact can be retrieved for this task.
		other.SetPublic("result_discarded", true)
	}
	if task.PrivateData.Execution != nil {
		AppendTaskPluginAuditInfo(other, task.PrivateData.Execution.TaskPlugin)
	}
	if task.PrivateData.UpstreamTaskID == "" && task.PrivateData.NodeName == "" {
		return
	}
	if task.PrivateData.UpstreamTaskID != "" {
		other.SetRoot("upstream_task_id", task.PrivateData.UpstreamTaskID)
	}
	if task.PrivateData.NodeName != "" {
		other.SetRoot("node_name", task.PrivateData.NodeName)
	}
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *types.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &types.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，将预扣的 quota 退还给用户（支持钱包和订阅），并退还令牌额度。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) error {
	if task == nil {
		return nil
	}
	if task.PrivateData.BillingState == TaskBillingStateRefunded {
		return nil
	}
	if task.PrivateData.BillingState == TaskBillingStateSettled {
		err := fmt.Errorf("任务 %s 已完成结算，跳过退款: %s", task.TaskID, reason)
		logger.LogWarn(ctx, err.Error())
		return err
	}

	ensureTaskBillingSnapshot(task)
	quota := task.Quota
	if quota == 0 {
		return markTaskBillingRefunded(ctx, task, reason)
	}

	if err := taskAdjustFunding(task, -quota); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		markTaskBillingError(ctx, task, err.Error())
		return err
	}

	if err := taskAdjustTokenQuota(ctx, task, -quota); err != nil {
		if compensationErr := taskAdjustFunding(task, quota); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("令牌退款失败后的资金补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		markTaskBillingError(ctx, task, err.Error())
		return err
	}

	other := taskBillingOther(task)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("reason", reason)
	other.SetPublic("billing_state", TaskBillingStateRefunded)
	other.SetPublic("pre_consumed_quota", quota)
	other.SetPublic("actual_quota", 0)
	logType := model.LogTypeRefund
	usedQuotaDelta := -quota
	if task.PrivateData.GetUsageAccountingMode() == model.TaskUsageAccountingFinal {
		logType = model.LogTypePreConsumeRollback
		usedQuotaDelta = 0
	}
	previousPrivateData := task.PrivateData
	previousQuota := task.Quota
	task.Quota = 0
	task.PrivateData.BillingState = TaskBillingStateRefunded
	task.PrivateData.ActualQuota = 0
	task.PrivateData.BillingError = reason
	task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	if err := model.ApplyTaskBillingAccounting(task, usedQuotaDelta, 0); err != nil {
		task.PrivateData = previousPrivateData
		task.Quota = previousQuota
		if compensationErr := taskAdjustTokenQuota(ctx, task, quota); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("退款状态持久化失败后的令牌补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		if compensationErr := taskAdjustFunding(task, quota); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("退款状态持久化失败后的资金补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		markTaskBillingError(ctx, task, "persist task refund accounting failed: "+err.Error())
		return err
	}
	if err := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:         task.UserId,
		LogType:        logType,
		Content:        "",
		ChannelId:      task.ChannelId,
		ModelName:      taskModelName(task),
		Quota:          quota,
		TokenId:        task.PrivateData.TokenId,
		Group:          task.Group,
		UseTimeSeconds: taskBillingUseTimeSeconds(task),
		Other:          other,
		NodeName:       task.PrivateData.NodeName,
	}); err != nil {
		markTaskBillingError(ctx, task, "记录退款日志失败: "+err.Error())
		return err
	}
	return nil
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
// clamps 可选：若计算 actualQuota 时发生额度饱和，将其记入日志 admin_info（仅管理员可见）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) error {
	if task == nil || actualQuota < 0 {
		return nil
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return nil
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// 调整资金来源
	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		markTaskBillingError(ctx, task, err.Error())
		return err
	}

	// 调整令牌额度
	if err := taskAdjustTokenQuota(ctx, task, quotaDelta); err != nil {
		if compensationErr := taskAdjustFunding(task, -quotaDelta); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("令牌差额调整失败后的资金补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		markTaskBillingError(ctx, task, err.Error())
		return err
	}

	task.Quota = actualQuota
	usedQuotaDelta := quotaDelta
	if task.PrivateData.GetUsageAccountingMode() == model.TaskUsageAccountingFinal {
		usedQuotaDelta = 0
	}
	if err := model.ApplyTaskQuotaAccounting(task, usedQuotaDelta); err != nil {
		task.Quota = preConsumedQuota
		if compensationErr := taskAdjustTokenQuota(ctx, task, -quotaDelta); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("任务 quota 持久化失败后的令牌补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		if compensationErr := taskAdjustFunding(task, -quotaDelta); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("任务 quota 持久化失败后的资金补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		markTaskBillingError(ctx, task, err.Error())
		return err
	}

	if task.PrivateData.GetUsageAccountingMode() == model.TaskUsageAccountingFinal {
		return nil
	}

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("pre_consumed_quota", preConsumedQuota)
	other.SetPublic("actual_quota", actualQuota)
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	if err := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:         task.UserId,
		LogType:        logType,
		Content:        reason,
		ChannelId:      task.ChannelId,
		ModelName:      taskModelName(task),
		Quota:          logQuota,
		TokenId:        task.PrivateData.TokenId,
		Group:          task.Group,
		UseTimeSeconds: taskBillingUseTimeSeconds(task),
		Other:          other,
		NodeName:       task.PrivateData.NodeName,
	}); err != nil {
		markTaskBillingError(ctx, task, "记录差额结算日志失败: "+err.Error())
		return err
	}
	return nil
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) error {
	if totalTokens <= 0 {
		return nil
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return nil
	}

	// 获取用户和组的倍率信息
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return nil
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	var finalGroupRatio float64
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	} else {
		finalGroupRatio = groupRatio
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	return RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}

func taskBillingUseTimeSeconds(task *model.Task) int {
	if task == nil {
		return 0
	}
	finishTime := task.FinishTime
	if finishTime <= 0 {
		finishTime = time.Now().Unix()
	}
	startTime := task.StartTime
	if startTime <= 0 {
		startTime = task.SubmitTime
	}
	if startTime <= 0 || finishTime <= startTime {
		return 0
	}
	return int(finishTime - startTime)
}

func BuildTaskUsageDetails(usage *dto.Usage) *dto.TaskUsageDetails {
	if usage == nil {
		return nil
	}
	return &dto.TaskUsageDetails{
		PromptTokens:           usage.PromptTokens,
		CompletionTokens:       usage.CompletionTokens,
		TotalTokens:            usage.TotalTokens,
		PromptTokensDetails:    usage.PromptTokensDetails,
		CompletionTokenDetails: usage.CompletionTokenDetails,
	}
}

func ensureTaskBillingSnapshot(task *model.Task) {
	if task == nil {
		return
	}
	if task.PrivateData.PreConsumedQuota == 0 && task.Quota != 0 {
		task.PrivateData.PreConsumedQuota = task.Quota
	}
	if task.PrivateData.BillingUpdatedAt == 0 {
		task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	}
}

func persistTaskBillingSnapshot(ctx context.Context, task *model.Task) error {
	if task == nil || task.ID <= 0 {
		return nil
	}
	err := model.DB.Model(&model.Task{}).
		Where("id = ?", task.ID).
		Update("private_data", task.PrivateData).Error
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("更新任务计费快照失败 task %s: %s", task.TaskID, err.Error()))
	}
	return err
}

func persistTaskQuota(ctx context.Context, task *model.Task) error {
	if task == nil || task.ID <= 0 {
		return nil
	}
	err := model.DB.Model(&model.Task{}).
		Where("id = ?", task.ID).
		Update("quota", task.Quota).Error
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("更新任务 quota 失败 task %s: %s", task.TaskID, err.Error()))
	}
	return err
}

func markTaskBillingError(ctx context.Context, task *model.Task, reason string) {
	if task == nil {
		return
	}
	ensureTaskBillingSnapshot(task)
	task.PrivateData.BillingError = reason
	task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	persistTaskBillingSnapshot(ctx, task)
}

func markTaskBillingRefunded(ctx context.Context, task *model.Task, reason string) error {
	if task == nil {
		return nil
	}
	ensureTaskBillingSnapshot(task)
	previousPrivateData := task.PrivateData
	task.PrivateData.BillingState = TaskBillingStateRefunded
	task.PrivateData.ActualQuota = 0
	task.PrivateData.BillingError = reason
	task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	if err := persistTaskBillingSnapshot(ctx, task); err != nil {
		task.PrivateData = previousPrivateData
		return err
	}
	return nil
}

// ConfirmTaskBillingSettled records the final successful billing point for
// async tasks whose quota was already pre-consumed at submit time.
func ConfirmTaskBillingSettled(ctx context.Context, task *model.Task, actualQuota int, content string, clamps ...*common.QuotaClamp) error {
	if task == nil {
		return nil
	}
	if task.PrivateData.BillingState == TaskBillingStateSettled {
		return nil
	}
	if task.PrivateData.BillingState == TaskBillingStateRefunded {
		err := fmt.Errorf("任务 %s 已退款，无法确认成功结算", task.TaskID)
		logger.LogWarn(ctx, err.Error())
		return err
	}
	if actualQuota < 0 {
		actualQuota = 0
	}
	if content == "" {
		content = "异步任务完成"
	}

	ensureTaskBillingSnapshot(task)
	previousPrivateData := task.PrivateData
	task.PrivateData.BillingState = TaskBillingStateSettled
	task.PrivateData.ActualQuota = actualQuota
	task.PrivateData.BillingError = ""
	task.PrivateData.BillingUpdatedAt = time.Now().Unix()
	usedQuotaDelta := actualQuota
	requestCountDelta := 1
	if task.PrivateData.GetUsageAccountingMode() == model.TaskUsageAccountingSubmit {
		usedQuotaDelta = 0
		requestCountDelta = 0
	}
	if err := model.ApplyTaskBillingAccounting(task, usedQuotaDelta, requestCountDelta); err != nil {
		task.PrivateData = previousPrivateData
		markTaskBillingError(ctx, task, "persist final task accounting failed: "+err.Error())
		return err
	}
	logQuota := actualQuota
	usageOnly := task.PrivateData.GetUsageAccountingMode() == model.TaskUsageAccountingSubmit
	if usageOnly {
		if task.PrivateData.TokenUsageLogged || (task.PrivateData.TotalTokens == 0 && task.PrivateData.PromptTokens == 0 && task.PrivateData.CompletionTokens == 0) {
			return nil
		}
		// The submit log already accounted for the charge. Record late usage
		// without charging or incrementing the request count a second time.
		logQuota = 0
		content = "异步任务用量补录"
	}

	other := taskBillingOther(task)
	if usageOnly {
		other.SetPublic("usage_only", true)
	}
	other.SetPublic("is_task", true)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("billing_state", TaskBillingStateSettled)
	other.SetPublic("pre_consumed_quota", task.PrivateData.PreConsumedQuota)
	other.SetPublic("actual_quota", actualQuota)
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	if err := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UsageOnly:        usageOnly,
		UserId:           task.UserId,
		LogType:          model.LogTypeConsume,
		Content:          content,
		ChannelId:        task.ChannelId,
		ModelName:        taskModelName(task),
		Quota:            logQuota,
		PromptTokens:     task.PrivateData.PromptTokens,
		CompletionTokens: task.PrivateData.CompletionTokens,
		TokenId:          task.PrivateData.TokenId,
		Group:            task.Group,
		UseTimeSeconds:   taskBillingUseTimeSeconds(task),
		Other:            other,
		NodeName:         task.PrivateData.NodeName,
	}); err != nil {
		markTaskBillingError(ctx, task, "记录最终结算日志失败: "+err.Error())
		return err
	}
	return nil
}

func taskFinalGroupRatio(task *model.Task) float64 {
	if task == nil {
		return 0
	}
	if bc := task.PrivateData.BillingContext; bc != nil && bc.GroupRatio != 0 {
		return bc.GroupRatio
	}
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return 0
	}
	groupRatio := ratio_setting.GetGroupRatio(group)
	if userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group); hasUserGroupRatio {
		return userGroupRatio
	}
	return groupRatio
}

func taskModelRatio(task *model.Task, modelName string) (float64, bool) {
	if task != nil {
		if bc := task.PrivateData.BillingContext; bc != nil && bc.ModelRatio > 0 {
			return bc.ModelRatio, true
		}
	}
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	return modelRatio, hasRatioSetting && modelRatio > 0
}

// CalculateTaskQuotaByUsage mirrors normal text/image token billing for async
// task settlement, including completion_ratio for generated image tokens.
func CalculateTaskQuotaByUsage(task *model.Task, usage *dto.Usage) int {
	quota, _ := CalculateTaskQuotaByUsageChecked(task, usage)
	return quota
}

func CalculateTaskQuotaByUsageChecked(task *model.Task, usage *dto.Usage) (int, *common.QuotaClamp) {
	if task == nil || usage == nil {
		return 0, nil
	}
	if usage.TotalTokens <= 0 && usage.PromptTokens+usage.CompletionTokens <= 0 {
		return 0, nil
	}

	modelName := taskModelName(task)
	modelRatio, ok := taskModelRatio(task, modelName)
	if !ok {
		return 0, nil
	}
	groupRatio := taskFinalGroupRatio(task)
	if groupRatio == 0 {
		return 0, nil
	}

	promptTokens := usage.PromptTokens
	completionTokens := usage.CompletionTokens
	if completionTokens <= 0 && usage.TotalTokens > promptTokens {
		completionTokens = usage.TotalTokens - promptTokens
	}
	if completionTokens <= 0 {
		completionTokens = usage.CompletionTokenDetails.TextTokens +
			usage.CompletionTokenDetails.ImageTokens +
			usage.CompletionTokenDetails.AudioTokens +
			usage.CompletionTokenDetails.ReasoningTokens
	}

	cacheTokens := usage.PromptTokensDetails.CachedTokens
	cacheCreationTokens := usage.PromptTokensDetails.CachedCreationTokens
	imageTokens := usage.PromptTokensDetails.ImageTokens
	audioTokens := usage.PromptTokensDetails.AudioTokens

	baseTokens := decimal.NewFromInt(int64(promptTokens))
	cacheQuota := decimal.Zero
	if cacheTokens > 0 {
		cacheRatio, _ := ratio_setting.GetCacheRatio(modelName)
		baseTokens = baseTokens.Sub(decimal.NewFromInt(int64(cacheTokens)))
		cacheQuota = decimal.NewFromInt(int64(cacheTokens)).Mul(decimal.NewFromFloat(cacheRatio))
	}

	cacheCreationQuota := decimal.Zero
	if cacheCreationTokens > 0 {
		cacheCreationRatio, _ := ratio_setting.GetCreateCacheRatio(modelName)
		baseTokens = baseTokens.Sub(decimal.NewFromInt(int64(cacheCreationTokens)))
		cacheCreationQuota = decimal.NewFromInt(int64(cacheCreationTokens)).Mul(decimal.NewFromFloat(cacheCreationRatio))
	}

	imageQuota := decimal.Zero
	if imageTokens > 0 {
		imageRatio, _ := ratio_setting.GetImageRatio(modelName)
		baseTokens = baseTokens.Sub(decimal.NewFromInt(int64(imageTokens)))
		imageQuota = decimal.NewFromInt(int64(imageTokens)).Mul(decimal.NewFromFloat(imageRatio))
	}

	audioInputQuota := decimal.Zero
	if audioTokens > 0 {
		audioRatio := ratio_setting.GetAudioRatio(modelName)
		baseTokens = baseTokens.Sub(decimal.NewFromInt(int64(audioTokens)))
		audioInputQuota = decimal.NewFromInt(int64(audioTokens)).Mul(decimal.NewFromFloat(audioRatio))
	}

	if baseTokens.LessThan(decimal.Zero) {
		baseTokens = decimal.Zero
	}

	completionQuota := decimal.NewFromInt(int64(completionTokens)).
		Mul(decimal.NewFromFloat(ratio_setting.GetCompletionRatio(modelName)))

	quota := baseTokens.
		Add(cacheQuota).
		Add(cacheCreationQuota).
		Add(imageQuota).
		Add(audioInputQuota).
		Add(completionQuota).
		Mul(decimal.NewFromFloat(modelRatio)).
		Mul(decimal.NewFromFloat(groupRatio))

	if bc := task.PrivateData.BillingContext; bc != nil {
		for _, otherRatio := range bc.OtherRatios {
			if otherRatio > 0 && otherRatio != 1 {
				quota = quota.Mul(decimal.NewFromFloat(otherRatio))
			}
		}
	}

	if quota.LessThanOrEqual(decimal.Zero) {
		return 0, nil
	}
	return common.QuotaFromDecimalChecked(quota)
}

func RecalculateTaskQuotaByUsage(ctx context.Context, task *model.Task, usage *dto.Usage) error {
	actualQuota, clamp := CalculateTaskQuotaByUsageChecked(task, usage)
	if actualQuota <= 0 {
		return nil
	}
	return RecalculateTaskQuota(ctx, task, actualQuota, fmt.Sprintf("usage重算：prompt=%d, completion=%d, total=%d", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens), clamp)
}

// AdjustTaskQuotaForFinalSettlement reconciles pre-consumed funds to actual
// quota before ConfirmTaskBillingSettled writes the final task billing log.
// It intentionally does not update used-quota stats or create a delta log.
func AdjustTaskQuotaForFinalSettlement(ctx context.Context, task *model.Task, actualQuota int, reason string) error {
	if task == nil || actualQuota < 0 {
		return nil
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota
	if quotaDelta == 0 {
		task.Quota = actualQuota
		return persistTaskQuota(ctx, task)
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 最终差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("最终差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		markTaskBillingError(ctx, task, err.Error())
		return err
	}
	if err := taskAdjustTokenQuota(ctx, task, quotaDelta); err != nil {
		if compensationErr := taskAdjustFunding(task, -quotaDelta); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("令牌差额调整失败后的资金补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		markTaskBillingError(ctx, task, err.Error())
		return err
	}

	task.Quota = actualQuota
	if err := persistTaskQuota(ctx, task); err != nil {
		task.Quota = preConsumedQuota
		if compensationErr := taskAdjustTokenQuota(ctx, task, -quotaDelta); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("任务 quota 持久化失败后的令牌补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		if compensationErr := taskAdjustFunding(task, -quotaDelta); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("任务 quota 持久化失败后的资金补偿也失败 task %s: %s", task.TaskID, compensationErr.Error()))
		}
		markTaskBillingError(ctx, task, err.Error())
		return err
	}
	return nil
}

// EvaluateTaskCompletionUsage evaluates actual facts against the frozen task
// expression. It neither mutates the snapshot nor moves funds: synchronous
// submission and polling have different persistence and settlement barriers.
func EvaluateTaskCompletionUsage(snap *billingexpr.BillingSnapshot, facts map[string]any) (billingexpr.TieredResult, map[string]any, error) {
	if snap == nil {
		return billingexpr.TieredResult{}, nil, fmt.Errorf("task billing snapshot is missing")
	}
	usage := make(map[string]any, len(snap.UsageFacts)+len(facts))
	maps.Copy(usage, snap.UsageFacts)
	maps.Copy(usage, facts)
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: usage})
	if err == nil && (result.ActualQuotaBeforeGroup < 0 || math.IsNaN(result.ActualQuotaBeforeGroup)) {
		err = fmt.Errorf("task completion expression produced an invalid cost")
	}
	return result, usage, err
}
