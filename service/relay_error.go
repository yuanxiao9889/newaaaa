package service

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	var rejection *upstreamFailure
	confirmed := errors.As(err, &rejection)
	if confirmed && rejection.terminalTask && (rejection.statusCode == http.StatusForbidden || err.StatusCode == http.StatusForbidden) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if c.GetString("transfer_execution_id") != "" {
		// Only a full rejection or confirmed terminal failure permits resubmission.
		// A local status or provider-like message is not evidence of rejection.
		if !confirmed || rejection.statusCode == http.StatusForbidden || err.StatusCode == http.StatusForbidden ||
			(!rejection.terminalTask && (rejection.statusCode == http.StatusRequestTimeout || err.StatusCode == http.StatusRequestTimeout)) ||
			operation_setting.IsAlwaysSkipRetryStatusCode(rejection.statusCode) {
			return PolicyDecision{Action: "stop", Reason: "transfer_dispatch_once", Source: "system"}
		}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if !confirmed {
		// Local setup/validation/storage failures cannot improve by replaying
		// generation. Do not let status matching or a time budget override them.
		if types.IsChannelError(err) {
			return PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}
		}
		switch err.GetErrorCode() {
		case types.ErrorCodeInvalidRequest, types.ErrorCodeSensitiveWordsDetected,
			types.ErrorCodeCountTokenFailed, types.ErrorCodeModelPriceError,
			types.ErrorCodeInvalidApiType, types.ErrorCodeJsonMarshalFailed,
			types.ErrorCodeClientRequestCanceled, types.ErrorCodeGetChannelFailed, types.ErrorCodeModelNotFound,
			types.ErrorCodeGenRelayInfoFailed, types.ErrorCodeReadRequestBodyFailed,
			types.ErrorCodeConvertRequestFailed, types.ErrorCodeAccessDenied,
			types.ErrorCodeBadRequestBody, types.ErrorCodeQueryDataError,
			types.ErrorCodeUpdateDataError, types.ErrorCodeInsufficientUserQuota,
			types.ErrorCodePreConsumeTokenQuotaFailed:
			return PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}
		}
	}
	if common.ShouldStopRetryForSafety(string(err.GetErrorCode()), err.Error()) ||
		common.ShouldStopRetryForSafety(err.ToOpenAIError().Type, "") {
		return PolicyDecision{Action: "stop", Reason: "safety_related_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if ((!confirmed || !rejection.terminalTask) && operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode())) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode())))
	if ShouldDisableChannelForRequest(c, channelError.ChannelId, err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode()
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}
