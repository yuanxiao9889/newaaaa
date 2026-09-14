package dto

import (
	"encoding/json"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
)

type TaskError struct {
	// NoRetry prevents duplicate upstream work after a response has been accepted.
	NoRetry    bool   `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Data       any    `json:"data"`
	StatusCode int    `json:"-"`
	LocalError bool   `json:"-"`
	Error      error  `json:"-"`
}

type TaskData interface {
	SunoDataResponse | []SunoDataResponse | string | any
}

const TaskSuccessCode = "success"

type TaskResponse[T TaskData] struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type TaskDto struct {
	ID                int64           `json:"id"`
	CreatedAt         int64           `json:"created_at"`
	UpdatedAt         int64           `json:"updated_at"`
	TaskID            string          `json:"task_id"`
	Platform          string          `json:"platform"`
	UserId            int             `json:"user_id"`
	Group             string          `json:"group"`
	TokenId           int             `json:"token_id,omitempty"`
	TokenName         string          `json:"token_name,omitempty"`
	ChannelId         int             `json:"channel_id"`
	ChannelName       string          `json:"channel_name,omitempty"`
	ModelName         string          `json:"model_name,omitempty"`
	UpstreamModelName string          `json:"upstream_model_name,omitempty"`
	Quota             int             `json:"quota"`
	Action            string          `json:"action"`
	Status            string          `json:"status"`
	FailReason        string          `json:"fail_reason"`
	ResultURL         string          `json:"result_url,omitempty"` // 任务结果 URL（视频地址等）
	SubmitTime        int64           `json:"submit_time"`
	StartTime         int64           `json:"start_time"`
	FinishTime        int64           `json:"finish_time"`
	Progress          string          `json:"progress"`
	Properties        any             `json:"properties"`
	Username          string          `json:"username,omitempty"`
	Data              json.RawMessage `json:"data"`

	InternalAsync        bool              `json:"internal_async,omitempty"`
	RequestPath          string            `json:"request_path,omitempty"`
	WorkerAttempts       int               `json:"worker_attempts,omitempty"`
	ChannelRetryPath     []string          `json:"channel_retry_path,omitempty"`
	BillingState         string            `json:"billing_state,omitempty"`
	PreConsumedQuota     int               `json:"pre_consumed_quota,omitempty"`
	ActualQuota          int               `json:"actual_quota,omitempty"`
	BillingError         string            `json:"billing_error,omitempty"`
	PromptTokens         int               `json:"prompt_tokens,omitempty"`
	CompletionTokens     int               `json:"completion_tokens,omitempty"`
	TotalTokens          int               `json:"total_tokens,omitempty"`
	UsageDetails         *TaskUsageDetails `json:"usage_details,omitempty"`
	LegacyVideoAvailable bool              `json:"legacy_video_available,omitempty"`
	AdminInfo            *TaskAdminInfo    `json:"admin_info,omitempty"`
	RootInfo             *TaskRootInfo     `json:"root_info,omitempty"`
}

type TaskUsageDetails struct {
	PromptTokens           int                         `json:"prompt_tokens,omitempty"`
	CompletionTokens       int                         `json:"completion_tokens,omitempty"`
	TotalTokens            int                         `json:"total_tokens,omitempty"`
	PromptTokensDetails    relaydto.InputTokenDetails  `json:"prompt_tokens_details,omitempty"`
	CompletionTokenDetails relaydto.OutputTokenDetails `json:"completion_tokens_details,omitempty"`
}

type TaskPluginInfo struct {
	Key     string                `json:"key"`
	Name    string                `json:"name"`
	Version string                `json:"version,omitempty"`
	Author  *TaskPluginAuthorInfo `json:"author,omitempty"`
}

type TaskPluginAuthorInfo struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type TaskPluginRuntimeInfo struct {
	Key        string `json:"key"`
	Version    string `json:"version"`
	APIVersion int    `json:"api_version"`
	Generation uint64 `json:"generation"`
}

type TaskAdminInfo struct {
	RequestID   string          `json:"request_id,omitempty"`
	RequestPath string          `json:"request_path,omitempty"`
	TaskPlugin  *TaskPluginInfo `json:"task_plugin,omitempty"`
}

type TaskRootInfo struct {
	TaskPlugin     *TaskPluginRuntimeInfo `json:"task_plugin,omitempty"`
	UpstreamTaskID string                 `json:"upstream_task_id,omitempty"`
	NodeName       string                 `json:"node_name,omitempty"`
}

type FetchReq struct {
	IDs []string `json:"ids"`
}

func (t *TaskResponse[T]) IsSuccess() bool {
	return t.Code == TaskSuccessCode
}
