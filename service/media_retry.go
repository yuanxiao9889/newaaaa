package service

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const mediaRetryContextKey = "media_retry_state"

// IsMediaTaskRequest also works before channel selection initializes a budget.
// Native plugin routes keep their protocol constraints but share task-local
// failure isolation with the public image and video endpoints.
func IsMediaTaskRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if MediaRetry(c) != nil {
		return true
	}
	if c.Request != nil {
		switch c.Request.URL.Path {
		case "/v1/images/generations", "/v1/images/edits", "/v1/videos", "/v1/video/generations":
			return true
		}
		if c.Query("image_task") == "true" {
			return true
		}
		path := c.Request.URL.Path
		if (strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/")) &&
			(strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")) {
			var request struct {
				GenerationConfig struct {
					ResponseModalities []string `json:"responseModalities"`
				} `json:"generationConfig"`
			}
			if common.UnmarshalBodyReusable(c, &request) == nil {
				for _, modality := range request.GenerationConfig.ResponseModalities {
					if strings.EqualFold(strings.TrimSpace(modality), "IMAGE") {
						return true
					}
				}
			}
		}
	}
	if _, exists := c.Get(jsplugin.ContextKeyPinnedRoute); exists {
		return true
	}
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		if pinned, ok := value.(jsplugin.PinnedEndpoint); ok {
			return pinned.Protocol == jsplugin.ProtocolOpenAIImage || pinned.Protocol == "openai_video"
		}
	}
	return false
}

func MediaRetry(c *gin.Context) *model.MediaRetryState {
	v, _ := c.Get(mediaRetryContextKey)
	s, _ := v.(*model.MediaRetryState)
	return s
}

func BindMediaRetry(c *gin.Context, state *model.MediaRetryState) {
	c.Set(mediaRetryContextKey, state)
}

// PersistMediaRetryBeforeDispatch attaches the durable owner's CAS checkpoint.
// Local validation and reservation failures do not consume an upstream attempt.
func PersistMediaRetryBeforeDispatch(c *gin.Context, persist func() error) {
	c.Set("media_retry_persist", persist)
}

func BeginMediaRetryDispatch(c *gin.Context) *types.NewAPIError {
	state := MediaRetry(c)
	if state == nil || c.GetBool("media_retry_dispatched") {
		return nil
	}
	if !state.BeginAttempt(time.Now()) {
		state.StopReason = "retry_budget_exhausted"
		return types.NewErrorWithStatusCode(errors.New("media retry budget exhausted"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	state.History = append(state.History, model.MediaRetryAttempt{ChannelID: c.GetInt("channel_id"), Round: state.Round + 1, StartedAtMS: time.Now().UnixMilli()})
	state.StopReason = ""
	if value, ok := c.Get("media_retry_persist"); ok {
		if err := value.(func() error)(); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeUpdateDataError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		}
	}
	c.Set("media_retry_dispatched", true)
	return nil
}

// InitializeMediaRetry is called only for new requests. Reconstructed legacy
// tasks bind their persisted (possibly nil) state instead of adopting settings.
func InitializeMediaRetry(c *gin.Context, info *relaycommon.RelayInfo) {
	if _, initialized := c.Get(mediaRetryContextKey); initialized {
		return
	}
	BindMediaRetry(c, nil)
	channelType := c.GetInt("channel_type")
	if channelType == constant.ChannelTypeMidjourney || channelType == constant.ChannelTypeMidjourneyPlus {
		return
	}
	// Transfer identity deduplicates the client request, not confirmed failed
	// upstream attempts within that request. Dispatch decisions still require
	// explicit rejection and keep unknown acceptance outcomes non-replayable.
	if ShouldSkipRetryAfterChannelAffinityFailure(c) || GetChannelConstraints(c).SuppressesRetry() {
		return
	}
	if info.TaskRelayInfo != nil && (info.LockedChannel != nil || info.OriginTaskID != "" || len(info.OriginTasks) > 0) {
		return
	}
	isMedia := info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits || info.RelayMode == relayconstant.RelayModeVideoSubmit
	action := ""
	if info.TaskRelayInfo != nil {
		action = info.Action
	}
	switch constant.NormalizeTaskAction(action) {
	case constant.TaskActionTextToVideo, constant.TaskActionImageToVideo, constant.TaskActionFirstTailToVideo, constant.TaskActionReferenceToVideo:
		isMedia = true
	case constant.TaskActionImageGenerate, constant.TaskActionImageEdit, constant.TaskActionGeminiImage, "text_to_image", "image_to_image":
		isMedia = true
	}
	if request, ok := info.Request.(*dto.GeminiChatRequest); ok {
		for _, modality := range request.GenerationConfig.ResponseModalities {
			if strings.EqualFold(strings.TrimSpace(modality), "IMAGE") {
				isMedia = true
			}
		}
	}
	if c.Request != nil {
		switch c.Request.URL.Path {
		case "/v1/videos", "/v1/video/generations", "/v1/images/generations", "/v1/images/edits":
			isMedia = true
		case "/v1/chat/completions", "/v1/responses":
			isMedia = c.Query("image_task") == "true"
		}
	}
	if !isMedia {
		return
	}
	settings, ok := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	if !ok || !settings.AutoSwitchEnabled {
		return
	}
	minutes, attempts := 10, 3
	if settings.AutoSwitchRetryWindowMinutes != nil {
		minutes = *settings.AutoSwitchRetryWindowMinutes
	}
	if settings.AutoSwitchMaxAttempts != nil {
		attempts = *settings.AutoSwitchMaxAttempts
	}
	BindMediaRetry(c, &model.MediaRetryState{Version: 1, WindowMinutes: minutes, MaxAttempts: attempts})
}

func MediaRetryBudget(state *model.MediaRetryState, start time.Time) RetryBudget {
	if state != nil {
		return RetryBudget{Media: state}
	}
	return NewRetryBudget(start)
}

// DecideMediaRetry receives only an explicitly established upstream failure.
// Callers must retain transport, local validation and delivery barriers.
func DecideMediaRetry(c *gin.Context, channelID int, code, message string) PolicyDecision {
	state := MediaRetry(c)
	decision := PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "channel"}
	if state == nil {
		return decision
	}
	category := state.RecordFailure(channelID, code, message)
	switch {
	case common.ShouldStopRetryForSafety(code, message):
		// A task-local retry allowance never overrides the safety stop rule.
		decision.Reason = "safety_related_error"
	case c.Request.Context().Err() != nil:
		decision.Reason = "request_cancelled"
	case GetChannelConstraints(c).SuppressesRetry():
		decision.Reason, decision.Source = "pinned_channel", "channel_constraint"
	case ShouldSkipRetryAfterChannelAffinityFailure(c):
		decision.Reason, decision.Source = "strict_session", "session_rule"
	case state.Remaining(time.Now()) > 0:
		decision.Action, decision.Reason = "retry", category
	case state.DeadlineMS > 0 && time.Now().UnixMilli() >= state.DeadlineMS:
		decision.Reason = "retry_window_expired"
	}
	if decision.Action == "stop" {
		state.StopReason = decision.Reason
	}
	return decision
}
