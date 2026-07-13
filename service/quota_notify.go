package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/go-redis/redis/v8"
)

type quotaWarningStage int

const (
	quotaWarningStageNone quotaWarningStage = iota
	quotaWarningStageThreshold
	quotaWarningStageHalf
	quotaWarningStageCritical
)

const quotaWarningStateTTL = 30 * 24 * time.Hour

type quotaWarningState struct {
	Threshold int64
	Stage     quotaWarningStage
	ExpiresAt time.Time
}

var quotaWarningMemoryState sync.Map

var quotaWarningClaimScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
local threshold = tostring(ARGV[1])
local stage = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])

if current then
    local separator = string.find(current, ':')
    if separator then
        local currentThreshold = string.sub(current, 1, separator - 1)
        local currentStage = tonumber(string.sub(current, separator + 1))
        if currentThreshold == threshold and currentStage and currentStage >= stage then
            redis.call('EXPIRE', KEYS[1], ttl)
            return 0
        end
    end
end

redis.call('SET', KEYS[1], threshold .. ':' .. tostring(stage), 'EX', ttl)
return 1
`)

func (stage quotaWarningStage) label() string {
	switch stage {
	case quotaWarningStageThreshold:
		return "100%"
	case quotaWarningStageHalf:
		return "50%"
	case quotaWarningStageCritical:
		return "20%"
	default:
		return ""
	}
}

func newQuotaWarningNotification(notifyType string, prompt string, stage quotaWarningStage, remaining int64, topUpLink string) dto.Notify {
	title := fmt.Sprintf("%s（余额低于预警阈值的 %s）", prompt, stage.label())
	formattedRemaining := logger.FormatQuota64(remaining)

	var content string
	var values []interface{}
	switch notifyType {
	case dto.NotifyTypeBark:
		content = "{{value}}，剩余额度：{{value}}，请及时充值"
		values = []interface{}{title, formattedRemaining}
	case dto.NotifyTypeGotify:
		content = "{{value}}，当前剩余额度为 {{value}}，请及时充值。"
		values = []interface{}{title, formattedRemaining}
	default:
		content = "{{value}}，当前剩余额度为 {{value}}，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='{{value}}'>{{value}}</a>"
		values = []interface{}{title, formattedRemaining, topUpLink, topUpLink}
	}

	return dto.NewNotify(dto.NotifyTypeQuotaExceed, title, content, values)
}

func sendQuotaWarningNotify(relayInfo *relaycommon.RelayInfo, source string, sourceID int, remaining int64, prompt string) {
	if relayInfo == nil {
		return
	}

	threshold := int64(common.QuotaRemindThreshold)
	if relayInfo.UserSetting.QuotaWarningThreshold > 0 {
		threshold = int64(common.QuotaFromFloat(relayInfo.UserSetting.QuotaWarningThreshold))
	}

	stage, claimed, err := claimQuotaWarningStage(relayInfo.UserId, source, sourceID, remaining, threshold)
	if err != nil {
		common.SysError(fmt.Sprintf("failed to claim quota warning stage for user %d: %s", relayInfo.UserId, err.Error()))
		return
	}
	if !claimed {
		return
	}

	notifyType := relayInfo.UserSetting.NotifyType
	if notifyType == "" {
		notifyType = dto.NotifyTypeEmail
	}
	notification := newQuotaWarningNotification(
		notifyType,
		prompt,
		stage,
		remaining,
		PaymentReturnURL("/console/topup"),
	)
	if err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, notification); err != nil {
		common.SysError(fmt.Sprintf("failed to send %s quota warning to user %d: %s", source, relayInfo.UserId, err.Error()))
	}
}

func calculateQuotaWarningStage(remaining, threshold int64) quotaWarningStage {
	if threshold <= 0 || remaining >= threshold {
		return quotaWarningStageNone
	}

	criticalBoundary := threshold / 5
	if threshold%5 != 0 {
		criticalBoundary++
	}
	if remaining < criticalBoundary {
		return quotaWarningStageCritical
	}

	halfBoundary := threshold/2 + threshold%2
	if remaining < halfBoundary {
		return quotaWarningStageHalf
	}
	return quotaWarningStageThreshold
}

func quotaWarningStateKey(userID int, source string, sourceID int) string {
	return fmt.Sprintf("quota_warning_stage:%d:%s:%d", userID, source, sourceID)
}

func claimQuotaWarningStage(userID int, source string, sourceID int, remaining, threshold int64) (quotaWarningStage, bool, error) {
	key := quotaWarningStateKey(userID, source, sourceID)
	stage := calculateQuotaWarningStage(remaining, threshold)
	if stage == quotaWarningStageNone {
		if common.RedisEnabled && common.RDB != nil {
			if err := common.RedisDel(key); err != nil {
				return stage, false, fmt.Errorf("failed to reset quota warning state: %w", err)
			}
		} else {
			resetMemoryQuotaWarningStage(key)
		}
		return stage, false, nil
	}

	if common.RedisEnabled && common.RDB != nil {
		claimed, err := quotaWarningClaimScript.Run(
			context.Background(),
			common.RDB,
			[]string{key},
			threshold,
			int(stage),
			int64(quotaWarningStateTTL/time.Second),
		).Int()
		if err != nil {
			return stage, false, fmt.Errorf("failed to claim quota warning stage: %w", err)
		}
		return stage, claimed == 1, nil
	}

	claimed, err := claimMemoryQuotaWarningStage(key, threshold, stage)
	return stage, claimed, err
}

func claimMemoryQuotaWarningStage(key string, threshold int64, stage quotaWarningStage) (bool, error) {
	now := time.Now()
	next := quotaWarningState{
		Threshold: threshold,
		Stage:     stage,
		ExpiresAt: now.Add(quotaWarningStateTTL),
	}

	for {
		currentValue, loaded := quotaWarningMemoryState.Load(key)
		if !loaded {
			_, loaded = quotaWarningMemoryState.LoadOrStore(key, next)
			if !loaded {
				return true, nil
			}
			continue
		}

		current, ok := currentValue.(quotaWarningState)
		if !ok || !current.ExpiresAt.After(now) {
			quotaWarningMemoryState.CompareAndDelete(key, currentValue)
			continue
		}

		if current.Threshold == threshold && current.Stage >= stage {
			current.ExpiresAt = next.ExpiresAt
			quotaWarningMemoryState.CompareAndSwap(key, currentValue, current)
			return false, nil
		}

		if quotaWarningMemoryState.CompareAndSwap(key, currentValue, next) {
			return true, nil
		}
	}
}

func resetMemoryQuotaWarningStage(key string) {
	quotaWarningMemoryState.Delete(key)
}
