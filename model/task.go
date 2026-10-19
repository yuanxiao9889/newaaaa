package model

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	rootdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	commonRelay "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
	"reflect"
	"strings"
	"time"
)

type TaskStatus string

// UserError derives a public diagnostic without changing the stored failure.
// Retry details can retain a structured code after FailReason lost its envelope.
func (t *Task) UserError() common.PublicError {
	if t.Platform == constant.TaskPlatformMidjourney {
		return common.MidjourneyPublicError("", t.FailReason, 0, false)
	}
	reason, _, _ := strings.Cut(t.FailReason, "\nAsync Channel Retry Path:")
	publicError := common.ClassifyPublicError("", reason, 0, false)
	if publicError.Code != "generation_failed" && publicError.Code != "content_safety_unknown" {
		return publicError
	}
	var details []AsyncImageRetryDetail
	if retry := t.PrivateData.ImageRetry; retry != nil {
		details = retry.Details
	} else if retry := t.PrivateData.VideoRetry; retry != nil {
		details = retry.Details
	}
	for i := len(details) - 1; i >= 0; i-- {
		detail := details[i]
		if t.ChannelId > 0 && detail.ChannelID != t.ChannelId {
			continue
		}
		if detail.Error == "" && detail.ErrorCode == "" {
			break
		}
		// An earlier attempt's failure is not evidence for a later accepted
		// job. Only enrich the final diagnostic with its matching retry record.
		if reason == "" || (detail.Error != "" && (strings.Contains(detail.Error, reason) || strings.Contains(reason, detail.Error))) {
			diagnostic := common.ClassifyPublicError(detail.ErrorCode, detail.Error, detail.StatusCode, false)
			if diagnostic.Code != "generation_failed" {
				return diagnostic
			}
			return publicError
		}
		break
	}
	return publicError
}

const (
	TaskStatusNotStart   TaskStatus = "NOT_START"
	TaskStatusSubmitted             = "SUBMITTED"
	TaskStatusQueued                = "QUEUED"
	TaskStatusInProgress            = "IN_PROGRESS"
	TaskStatusFailure               = "FAILURE"
	TaskStatusSuccess               = "SUCCESS"
	TaskStatusUnknown               = "UNKNOWN"
)

// TaskRefundLegacyCutoff separates tasks created before timeout refunds were
// introduced. Those legacy tasks are failed without an automatic refund.
const TaskRefundLegacyCutoff int64 = 1771718400 // 2026-02-22 00:00:00 UTC

type Task struct {
	ID          int64                 `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt   int64                 `json:"created_at" gorm:"index"`
	UpdatedAt   int64                 `json:"updated_at"`
	TaskID      string                `json:"task_id" gorm:"type:varchar(191);index"` // 第三方id，不一定有/ song id\ Task id
	Platform    constant.TaskPlatform `json:"platform" gorm:"type:varchar(30);index"` // 平台
	UserId      int                   `json:"user_id" gorm:"index"`
	Group       string                `json:"group" gorm:"type:varchar(50)"` // 修正计费用
	ChannelId   int                   `json:"channel_id" gorm:"index"`
	TokenId     int                   `json:"-" gorm:"index"`
	TokenName   string                `json:"-" gorm:"type:varchar(50);index"`
	Quota       int                   `json:"quota"`
	Action      string                `json:"action" gorm:"type:varchar(40);index"` // 任务类型, song, lyrics, description-mode
	Status      TaskStatus            `json:"status" gorm:"type:varchar(20);index"` // 任务状态
	FailReason  string                `json:"fail_reason"`
	SubmitTime  int64                 `json:"submit_time" gorm:"index"`
	StartTime   int64                 `json:"start_time" gorm:"index"`
	FinishTime  int64                 `json:"finish_time" gorm:"index"`
	Progress    string                `json:"progress" gorm:"type:varchar(20);index"`
	Properties  Properties            `json:"properties" gorm:"type:json"`
	Username    string                `json:"username,omitempty" gorm:"-"`
	ChannelName string                `json:"channel_name,omitempty" gorm:"-"`
	// 禁止返回给用户，内部可能包含key等隐私信息
	PrivateData TaskPrivateData `json:"-" gorm:"column:private_data;type:json"`
	Data        json.RawMessage `json:"data" gorm:"type:json"`
}

type Properties struct {
	Input             string `json:"input"`
	UpstreamModelName string `json:"upstream_model_name,omitempty"`
	OriginModelName   string `json:"origin_model_name,omitempty"`
}

// AsyncImageAsset is one cached result in an async image task, in upstream order.
type AsyncImageAsset struct {
	LocalPath     string `json:"local_path"`
	MimeType      string `json:"mime_type"`
	FileSize      int64  `json:"file_size"`
	SourceURL     string `json:"source_url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type TaskPrivateData struct {
	Key            string `json:"key,omitempty"`
	UpstreamTaskID string `json:"upstream_task_id,omitempty"` // 上游真实 task ID
	ResultURL      string `json:"result_url,omitempty"`       // 任务成功后的结果 URL（视频地址等）
	// Execution records safe, immutable request provenance. It lives next to
	// other private task state so public task DTOs cannot expose it by accident.
	Execution *TaskExecutionSnapshot `json:"execution,omitempty"`
	// 计费上下文：用于异步退款/差额结算（轮询阶段读取）
	BillingSource  string              `json:"billing_source,omitempty"`  // "wallet" 或 "subscription"
	SubscriptionId int                 `json:"subscription_id,omitempty"` // 订阅 ID，用于订阅退款
	TokenId        int                 `json:"token_id,omitempty"`        // 令牌 ID，用于令牌额度退款
	NodeName       string              `json:"node_name,omitempty"`       // 发起任务的节点名，轮询结算阶段据此归属日志而非最后查询节点
	BillingContext *TaskBillingContext `json:"billing_context,omitempty"` // 计费参数快照（用于轮询阶段重新计算）

	AssetType   string            `json:"asset_type,omitempty"`
	LocalPath   string            `json:"local_path,omitempty"`
	MimeType    string            `json:"mime_type,omitempty"`
	FileSize    int64             `json:"file_size,omitempty"`
	StoredAt    int64             `json:"stored_at,omitempty"`
	ExpiresAt   int64             `json:"expires_at,omitempty"`
	SourceURL   string            `json:"source_url,omitempty"`
	ImageAssets []AsyncImageAsset `json:"image_assets,omitempty"`

	InternalAsync      bool                     `json:"internal_async,omitempty"`
	RequestBodyPath    string                   `json:"request_body_path,omitempty"`
	RequestContentType string                   `json:"request_content_type,omitempty"`
	RequestBodySize    int64                    `json:"request_body_size,omitempty"`
	RequestMethod      string                   `json:"request_method,omitempty"`
	RequestPath        string                   `json:"request_path,omitempty"`
	RequestQuery       string                   `json:"request_query,omitempty"`
	RequestRelayFormat string                   `json:"request_relay_format,omitempty"`
	WorkerAttempts     int                      `json:"worker_attempts,omitempty"`
	WorkerHeartbeatAt  int64                    `json:"worker_heartbeat_at,omitempty"`
	ChannelRetryPath   []string                 `json:"channel_retry_path,omitempty"`
	ImageRetry         *AsyncImageRetryState    `json:"image_retry,omitempty"`
	PluginImage        *AsyncPluginImageState   `json:"plugin_image,omitempty"`
	VideoRetry         *VideoTaskRetryState     `json:"video_retry,omitempty"`
	AutoSwitch         *ChannelAutoSwitchTicket `json:"auto_switch,omitempty"`

	BillingState        string                    `json:"billing_state,omitempty"`
	UsageAccountingMode string                    `json:"usage_accounting_mode,omitempty"`
	PreConsumedQuota    int                       `json:"pre_consumed_quota,omitempty"`
	ActualQuota         int                       `json:"actual_quota,omitempty"`
	BillingError        string                    `json:"billing_error,omitempty"`
	BillingUpdatedAt    int64                     `json:"billing_updated_at,omitempty"`
	PromptTokens        int                       `json:"prompt_tokens,omitempty"`
	CompletionTokens    int                       `json:"completion_tokens,omitempty"`
	TotalTokens         int                       `json:"total_tokens,omitempty"`
	TokenUsageLogged    bool                      `json:"token_usage_logged,omitempty"`
	UsageDetails        *rootdto.TaskUsageDetails `json:"usage_details,omitempty"`
	// ResponsesBackground records that the openai_responses create request
	// asked for background:true. Every task is durable and survives client
	// disconnect regardless; this only echoes the protocol-level request
	// attribute back on retrieval snapshots.
	ResponsesBackground bool `json:"responses_background,omitempty"`
	// PluginState is plugin-owned cross-round data. Unlike Task.Data it is
	// only replaced when a hook explicitly returns state.
	PluginState json.RawMessage `json:"plugin_state,omitempty"`
	// PollFailures counts consecutive unrecognized or transient poll outcomes.
	PollFailures int `json:"poll_failures,omitempty"`
	// ResultDiscarded marks an immediate terminal result whose submit route
	// declared retainResult: false. The upstream snapshot was never written
	// and every retrieval surface treats the task as not found. The zero
	// value keeps historical rows retained and retrievable.
	ResultDiscarded bool `json:"result_discarded,omitempty"`
}

// AsyncPluginImageState preserves the provider outcome while the host owns
// queue status, result storage and final settlement.
type AsyncPluginImageState struct {
	Platform  constant.TaskPlatform `json:"platform"`
	Status    TaskStatus            `json:"status"`
	ResultURL string                `json:"result_url,omitempty"`
}

// AsyncImageRetryState keeps the original client request separate from the
// current upstream polling snapshot. Submitting is persisted before each POST;
// an interrupted POST must never be replayed without knowing its outcome.
type AsyncImageRetryState struct {
	BodyPath    string                     `json:"body_path,omitempty"`
	BodySize    int64                      `json:"body_size,omitempty"`
	RelayFormat string                     `json:"relay_format,omitempty"`
	Constraints rootdto.ChannelConstraints `json:"constraints"`
	Submitting  bool                       `json:"submitting,omitempty"`
	Details     []AsyncImageRetryDetail    `json:"details,omitempty"`
}

type AsyncImageRetryDetail struct {
	Attempt     int    `json:"attempt"`
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"`
	ChannelType int    `json:"channel_type,omitempty"`
	Status      string `json:"status"`
	StatusCode  int    `json:"status_code,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Error       string `json:"error,omitempty"`
	AttemptedAt int64  `json:"attempted_at"`
}

type TaskExecutionSnapshot struct {
	RequestID   string              `json:"request_id,omitempty"`
	RequestPath string              `json:"request_path,omitempty"`
	TaskPlugin  *TaskPluginSnapshot `json:"task_plugin,omitempty"`
}

// TaskPluginSnapshot contains credential-free identity only. Plugin source,
// request/response payloads, and channel secrets must never be added here.
type TaskPluginSnapshot struct {
	Key        string                    `json:"key"`
	Name       string                    `json:"name"`
	Version    string                    `json:"version"`
	Author     *TaskPluginAuthorSnapshot `json:"author,omitempty"`
	APIVersion int                       `json:"api_version"`
	Generation uint64                    `json:"generation"`
}

type TaskPluginAuthorSnapshot struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

const (
	TaskUsageAccountingSubmit = "submit"
	TaskUsageAccountingFinal  = "final"
)

// TaskBillingContext 记录任务提交时的计费参数，以便轮询阶段可以重新计算额度。
type TaskBillingContext struct {
	ModelPrice              float64                      `json:"model_price,omitempty"`       // 模型单价
	GroupRatio              float64                      `json:"group_ratio,omitempty"`       // 分组倍率
	ModelRatio              float64                      `json:"model_ratio,omitempty"`       // 模型倍率
	OtherRatios             map[string]float64           `json:"other_ratios,omitempty"`      // 附加倍率（时长、分辨率等）
	OriginModelName         string                       `json:"origin_model_name,omitempty"` // 模型名称，必须为OriginModelName
	PerCallBilling          bool                         `json:"per_call_billing,omitempty"`  // 按次计费：跳过轮询阶段的差额结算
	TieredSnapshot          *billingexpr.BillingSnapshot `json:"tiered_snapshot,omitempty"`
	FixedPriceUnit          string                       `json:"fixed_price_unit,omitempty"`
	ImageTokenFallbackQuota *int                         `json:"image_token_fallback_quota,omitempty"`
	ImageTokenReserveUSD    float64                      `json:"image_token_reserve_usd,omitempty"`
}

// SyncTaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type SyncTaskQueryParams struct {
	Platform       constant.TaskPlatform
	ChannelID      string
	TaskID         string
	UserID         string
	Action         string
	Status         string
	ModelName      string
	TokenName      string
	StartTimestamp int64
	EndTimestamp   int64
	UserIDs        []int
}

type taskSnapshot struct {
	Status       TaskStatus
	Progress     string
	StartTime    int64
	FinishTime   int64
	FailReason   string
	ResultURL    string
	Data         json.RawMessage
	PluginState  json.RawMessage
	PollFailures int
}

type TaskQuotaUsage struct {
	Mode  string  `json:"mode"`
	Count float64 `json:"count"`
}

func (t TaskStatus) ToVideoStatus() string {
	var status string
	switch t {
	case TaskStatusNotStart, TaskStatusQueued, TaskStatusSubmitted:
		status = dto.VideoStatusQueued
	case TaskStatusInProgress:
		status = dto.VideoStatusInProgress
	case TaskStatusSuccess:
		status = dto.VideoStatusCompleted
	case TaskStatusFailure:
		status = dto.VideoStatusFailed
	default:
		status = dto.VideoStatusUnknown // Default fallback
	}
	return status
}

func (t *Task) SetData(data any) {
	b, _ := common.Marshal(data)
	t.Data = json.RawMessage(b)
}

func (t *Task) GetData(v any) error {
	return common.Unmarshal(t.Data, &v)
}

func (m *Properties) Scan(val any) error {
	bytesValue := jsonScanBytes(val)
	if len(bytesValue) == 0 {
		*m = Properties{}
		return nil
	}
	return common.Unmarshal(bytesValue, m)
}

func (m Properties) Value() (driver.Value, error) {
	if m == (Properties{}) {
		return nil, nil
	}
	// 必须返回 string 而非 []byte:PG simple protocol 下 []byte 按 bytea 编码,
	// 写 json 列会触发 SQLSTATE 22P02。
	b, err := common.Marshal(m)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// ResultRetrievable reports whether retrieval surfaces (native query routes,
// protocol retrieve endpoints, artifact projection) may serve this task.
func (t *Task) ResultRetrievable() bool {
	return !t.PrivateData.ResultDiscarded
}

// GetUpstreamTaskID 获取上游真实 task ID（用于与 provider 通信）
// 旧数据没有 UpstreamTaskID 时，TaskID 本身就是上游 ID
func (t *Task) GetUpstreamTaskID() string {
	if t.PrivateData.UpstreamTaskID != "" {
		return t.PrivateData.UpstreamTaskID
	}
	return t.TaskID
}

// GetResultURL 获取任务结果 URL（视频地址等）
// 新数据存在 PrivateData.ResultURL 中；旧数据回退到 FailReason（历史兼容）
func (t *Task) GetResultURL() string {
	if t.PrivateData.ResultURL != "" {
		return t.PrivateData.ResultURL
	}
	return t.FailReason
}

// GenerateTaskID 生成对外暴露的 task_xxxx 格式 ID
func GenerateTaskID() string {
	key, _ := common.GenerateRandomCharsKey(32)
	return "task_" + key
}

func (p *TaskPrivateData) Scan(val any) error {
	bytesValue := jsonScanBytes(val)
	if len(bytesValue) == 0 {
		return nil
	}
	return common.Unmarshal(bytesValue, p)
}

func (p TaskPrivateData) Value() (driver.Value, error) {
	if reflect.DeepEqual(p, TaskPrivateData{}) {
		return nil, nil
	}
	// 同 Properties.Value:string 避免 PG simple protocol 的 bytea 编码。
	b, err := common.Marshal(p)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func InitTask(platform constant.TaskPlatform, relayInfo *commonRelay.RelayInfo) *Task {
	properties := Properties{}
	privateData := TaskPrivateData{}
	userID := 0
	usingGroup := ""
	channelID := 0
	if relayInfo != nil {
		userID = relayInfo.UserId
		usingGroup = relayInfo.UsingGroup
		privateData.TokenId = relayInfo.TokenId
		if relayInfo.ChannelMeta != nil {
			channelID = relayInfo.ChannelId
			if relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeGemini ||
				relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeVertexAi ||
				relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeNewAPI {
				privateData.Key = relayInfo.ChannelMeta.ApiKey
			}
			if relayInfo.UpstreamModelName != "" {
				properties.UpstreamModelName = relayInfo.UpstreamModelName
			}
		}
		if relayInfo.OriginModelName != "" {
			properties.OriginModelName = relayInfo.OriginModelName
		}
	}

	// 使用预生成的公开 ID（如果有），否则新生成
	taskID := ""
	if relayInfo != nil && relayInfo.TaskRelayInfo != nil && relayInfo.TaskRelayInfo.PublicTaskID != "" {
		taskID = relayInfo.TaskRelayInfo.PublicTaskID
	} else {
		taskID = GenerateTaskID()
	}

	t := &Task{
		TaskID:      taskID,
		UserId:      userID,
		Group:       usingGroup,
		TokenId:     privateData.TokenId,
		SubmitTime:  time.Now().Unix(),
		Status:      TaskStatusNotStart,
		Progress:    "0%",
		ChannelId:   channelID,
		Platform:    platform,
		Properties:  properties,
		PrivateData: privateData,
	}
	return t
}

func TaskGetAllUserTask(userId int, startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB.Omit("data", "channel_id").Where("user_id = ?", userId)
	query = applySyncTaskQueryFilters(query, queryParams, false)

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func TaskGetAllTasks(startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB.Omit("data")
	query = applySyncTaskQueryFilters(query, queryParams, true)

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetTimedOutUnfinishedTasks(cutoffUnix int64, limit int) []*Task {
	var tasks []*Task
	err := DB.Where("progress != ?", "100%").
		Where("status NOT IN ?", []string{TaskStatusFailure, TaskStatusSuccess}).
		Where("submit_time < ?", cutoffUnix).
		Order("submit_time").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetAllUnFinishSyncTasks(limit int) []*Task {
	var tasks []*Task
	var err error
	// get all tasks progress is not 100%
	err = DB.Where("progress != ?", "100%").Where("status != ?", TaskStatusFailure).Where("status != ?", TaskStatusSuccess).Limit(limit).Order("id").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

// HasUnfinishedSyncTasks reports whether at least one async (Suno/video) task is
// still in progress. It is a cheap existence check (LIMIT 1) used to decide
// whether the async_task_poll system task needs to run; when no task is pending
// the scheduler skips creating a row entirely.
func HasUnfinishedSyncTasks() bool {
	var id int64
	err := DB.Model(&Task{}).
		Where("progress != ?", "100%").
		Where("status != ?", TaskStatusFailure).
		Where("status != ?", TaskStatusSuccess).
		Limit(1).
		Pluck("id", &id).Error
	return err == nil && id != 0
}

func GetByOnlyTaskId(taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("task_id = ?", taskId).First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

// GetUniqueByOnlyTaskId resolves a public task identifier only when exactly one
// row owns it. Historical task identifiers were not globally unique, so
// capability-based reads must fail closed instead of selecting an arbitrary
// tenant's row.
func GetUniqueByOnlyTaskId(taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var tasks []*Task
	if err := DB.Where("task_id = ?", taskId).Order("id").Limit(2).Find(&tasks).Error; err != nil {
		return nil, false, err
	}
	if len(tasks) != 1 {
		return nil, false, nil
	}
	return tasks[0], true, nil
}

func GetByTaskId(userId int, taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("user_id = ? and task_id = ?", userId, taskId).
		First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskIdsForPlatforms(userID int, platforms []constant.TaskPlatform, taskIDs []string) ([]*Task, error) {
	if len(platforms) == 0 || len(taskIDs) == 0 {
		return nil, nil
	}
	var tasks []*Task
	err := DB.
		Where("user_id = ? AND platform IN ? AND task_id IN ?", userID, platforms, taskIDs).
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// GetTaskForProtocolObservation reloads one public task through the ownership
// boundary used by long-lived plugin protocol observers. A missing task,
// foreign user, and wrong plugin platform are deliberately indistinguishable.
func GetTaskForProtocolObservation(ctx context.Context, userID int, platform constant.TaskPlatform, taskID string) (*Task, bool, error) {
	if taskID == "" {
		return nil, false, nil
	}
	var task Task
	err := DB.WithContext(ctx).
		Where("user_id = ? AND platform = ? AND task_id = ?", userID, platform, taskID).
		First(&task).Error
	exists, err := RecordExist(err)
	if err != nil || !exists {
		return nil, exists, err
	}
	return &task, true, nil
}

func (Task *Task) Insert() error {
	return Task.InsertWithContext(context.Background())
}

// InsertWithContext creates the row. omitColumns are left out of the INSERT
// (for example "data" when the submit route discards the upstream snapshot)
// while the in-memory task keeps its values for presentation.
func (Task *Task) InsertWithContext(ctx context.Context, omitColumns ...string) error {
	tx := DB.WithContext(ctx)
	if len(omitColumns) > 0 {
		tx = tx.Omit(omitColumns...)
	}
	return tx.Create(Task).Error
}

func (s taskSnapshot) Equal(other taskSnapshot) bool {
	return s.Status == other.Status &&
		s.Progress == other.Progress &&
		s.StartTime == other.StartTime &&
		s.FinishTime == other.FinishTime &&
		s.FailReason == other.FailReason &&
		s.ResultURL == other.ResultURL &&
		bytes.Equal(s.Data, other.Data) &&
		bytes.Equal(s.PluginState, other.PluginState) &&
		s.PollFailures == other.PollFailures
}

func (t *Task) Snapshot() taskSnapshot {
	return taskSnapshot{
		Status:       t.Status,
		Progress:     t.Progress,
		StartTime:    t.StartTime,
		FinishTime:   t.FinishTime,
		FailReason:   t.FailReason,
		ResultURL:    t.PrivateData.ResultURL,
		Data:         t.Data,
		PluginState:  t.PrivateData.PluginState,
		PollFailures: t.PrivateData.PollFailures,
	}
}

func (Task *Task) Update() error {
	var err error
	err = DB.Save(Task).Error
	return err
}

func (t *Task) UpdateQuota() error {
	return DB.Model(t).Update("quota", t.Quota).Error
}

// UpdateWithStatus performs a conditional UPDATE guarded by fromStatus (CAS).
// Returns (true, nil) if this caller won the update, (false, nil) if
// another process already moved the task out of fromStatus. MySQL commonly
// reports changed rows rather than matched rows, so a same-value no-op update
// can also return false even when the status predicate still matched.
//
// Uses Model().Select("*").Updates() instead of Save() because GORM's Save
// falls back to INSERT ON CONFLICT when the WHERE-guarded UPDATE matches
// zero rows, which silently bypasses the CAS guard.
func (t *Task) UpdateWithStatus(fromStatus TaskStatus) (bool, error) {
	if t.PrivateData.VideoRetry != nil {
		return t.updateVideoTaskWithStatus(fromStatus)
	}
	result := DB.Model(t).Where("status = ?", fromStatus).Select("*").Updates(t)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// UpdateInternalAsyncImageTask fences resumed workers as well as terminal
// transitions. A stale worker cannot overwrite a newer attempt or a refund.
func (t *Task) UpdateInternalAsyncImageTask(fromStatus TaskStatus, workerAttempts int) (bool, error) {
	if t.PrivateData.ImageRetry == nil {
		return t.UpdateWithStatus(fromStatus)
	}
	won := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current Task
		if err := lockForUpdate(tx).First(&current, t.ID).Error; err != nil {
			return err
		}
		if current.Status != fromStatus || current.PrivateData.WorkerAttempts != workerAttempts {
			return nil
		}
		if t.PrivateData.WorkerAttempts != workerAttempts && fromStatus == TaskStatusInProgress &&
			current.PrivateData.WorkerHeartbeatAt > time.Now().Unix()-int64(common.GetAsyncImageWorkerStaleMinutes()*60) {
			return nil
		}
		if err := tx.Model(t).Select("*").Updates(t).Error; err != nil {
			return err
		}
		won = true
		return nil
	})
	return won, err
}

// TaskBulkUpdateByID performs an unconditional bulk UPDATE by primary key IDs.
// WARNING: This function has NO CAS (Compare-And-Swap) guard — it will overwrite
// any concurrent status changes. DO NOT use in billing/quota lifecycle flows
// (e.g., timeout, success, failure transitions that trigger refunds or settlements).
// For status transitions that involve billing, use Task.UpdateWithStatus() instead.
func TaskBulkUpdateByID(ids []int64, params map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("id in (?)", ids).
		Updates(params).Error
}

// TaskCountAllTasks returns total tasks that match the given query params (admin usage)
func TaskCountAllTasks(queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{})
	query = applySyncTaskQueryFilters(query, queryParams, true)
	_ = query.Count(&total).Error
	return total
}

// TaskCountAllUserTask returns total tasks for given user
func TaskCountAllUserTask(userId int, queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{}).Where("user_id = ?", userId)
	query = applySyncTaskQueryFilters(query, queryParams, false)
	_ = query.Count(&total).Error
	return total
}

func (t *Task) ToOpenAIVideo() *dto.OpenAIVideo {
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = t.TaskID
	openAIVideo.Status = t.Status.ToVideoStatus()
	openAIVideo.Model = t.Properties.OriginModelName
	openAIVideo.SetProgressStr(t.Progress)
	openAIVideo.CreatedAt = t.CreatedAt
	if t.Status == TaskStatusSuccess {
		if t.FinishTime != 0 {
			openAIVideo.CompletedAt = t.FinishTime
		} else {
			openAIVideo.CompletedAt = t.UpdatedAt
		}
	}
	return openAIVideo
}

// ApplyTaskBillingAccounting persists the task billing snapshot together with
// the user's and channel's usage counters. It intentionally bypasses the batch
// updater so asynchronous billing state and wallet usage remain atomic in the
// main database.
func ApplyTaskBillingAccounting(task *Task, usedQuotaDelta int, requestCountDelta int) error {
	if task == nil {
		return nil
	}
	if task.UserId <= 0 {
		return fmt.Errorf("invalid task billing user id: %d", task.UserId)
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		if task.ID > 0 {
			result := tx.Model(&Task{}).
				Where("id = ?", task.ID).
				Updates(map[string]interface{}{"private_data": task.PrivateData, "quota": task.Quota})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		}

		return adjustTaskUsageAccounting(tx, task.UserId, task.ChannelId, usedQuotaDelta, requestCountDelta)
	})
}

// ApplyTaskQuotaAccounting persists a reconciled task quota and its usage
// delta in one main-database transaction.
func ApplyTaskQuotaAccounting(task *Task, usedQuotaDelta int) error {
	if task == nil {
		return nil
	}
	if task.UserId <= 0 {
		return fmt.Errorf("invalid task billing user id: %d", task.UserId)
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		if task.ID > 0 {
			result := tx.Model(&Task{}).Where("id = ?", task.ID).Update("quota", task.Quota)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		}

		return adjustTaskUsageAccounting(tx, task.UserId, task.ChannelId, usedQuotaDelta, 0)
	})
}

// AdjustTaskUsageAccounting synchronously applies usage counters for task
// billing paths that do not have a model.Task row, such as legacy tasks.
func AdjustTaskUsageAccounting(userId int, channelId int, usedQuotaDelta int, requestCountDelta int) error {
	if userId <= 0 {
		return fmt.Errorf("invalid task billing user id: %d", userId)
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return adjustTaskUsageAccounting(tx, userId, channelId, usedQuotaDelta, requestCountDelta)
	})
}

func adjustTaskUsageAccounting(tx *gorm.DB, userId int, channelId int, usedQuotaDelta int, requestCountDelta int) error {
	if usedQuotaDelta == 0 && requestCountDelta == 0 {
		return nil
	}
	result := tx.Model(&User{}).Where("id = ?", userId).Updates(map[string]interface{}{
		"used_quota":    gorm.Expr("used_quota + ?", usedQuotaDelta),
		"request_count": gorm.Expr("request_count + ?", requestCountDelta),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	if channelId <= 0 || usedQuotaDelta == 0 {
		return nil
	}
	return tx.Model(&Channel{}).
		Where("id = ?", channelId).
		Update("used_quota", gorm.Expr("used_quota + ?", usedQuotaDelta)).Error
}

func (p TaskPrivateData) GetUsageAccountingMode() string {
	if p.UsageAccountingMode == TaskUsageAccountingSubmit || p.UsageAccountingMode == TaskUsageAccountingFinal {
		return p.UsageAccountingMode
	}
	if p.InternalAsync {
		return TaskUsageAccountingFinal
	}
	return TaskUsageAccountingSubmit
}

func (t *Task) GetTokenID() int {
	if t.TokenId > 0 {
		return t.TokenId
	}
	return t.PrivateData.TokenId
}

func HydrateTaskTokenNames(tasks []*Task) error {
	tokenIDs := make([]int, 0)
	seenTokenIDs := make(map[int]struct{})
	for _, task := range tasks {
		if task == nil {
			continue
		}
		task.TokenId = task.GetTokenID()
		if task.TokenName != "" || task.TokenId <= 0 {
			continue
		}
		if _, exists := seenTokenIDs[task.TokenId]; exists {
			continue
		}
		seenTokenIDs[task.TokenId] = struct{}{}
		tokenIDs = append(tokenIDs, task.TokenId)
	}
	if len(tokenIDs) == 0 {
		return nil
	}

	var tokens []struct {
		Id   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := DB.Model(&Token{}).
		Select("id, name").
		Where("id IN ?", tokenIDs).
		Find(&tokens).Error; err != nil {
		return err
	}

	tokenNames := make(map[int]string, len(tokens))
	for _, token := range tokens {
		tokenNames[token.Id] = token.Name
	}
	for _, task := range tasks {
		if task != nil && task.TokenName == "" {
			task.TokenName = tokenNames[task.TokenId]
		}
	}
	return nil
}

func CountUnfinishedInternalAsyncImageTasks(limit int) int {
	if limit <= 0 {
		limit = 1000
	}
	var count int64
	query := DB.Model(&Task{}).
		Where("platform = ?", constant.TaskPlatformInternalImage).
		Where("action IN ?", internalAsyncImageTaskActions()).
		Where("status NOT IN ?", []TaskStatus{TaskStatusFailure, TaskStatusSuccess})
	if err := query.Limit(limit).Count(&count).Error; err != nil {
		return 0
	}
	if count > int64(limit) {
		return limit
	}
	return int(count)
}

func internalAsyncImageTaskActions() []string {
	return []string{
		constant.TaskActionImageGenerate,
		constant.TaskActionImageEdit,
		constant.TaskActionGeminiImage,
	}
}

func GetRunnableInternalAsyncImageTasks(limit int, staleBefore int64) []*Task {
	if limit <= 0 {
		limit = 10
	}
	scanLimit := limit * 20
	if scanLimit < 1024 {
		scanLimit = 1024
	}
	candidates := getUnfinishedImageTasksForInternalAsync(scanLimit)
	tasks := make([]*Task, 0, limit)
	for _, task := range candidates {
		if task == nil || !task.PrivateData.InternalAsync {
			continue
		}
		if task.Status == TaskStatusInProgress && task.PrivateData.WorkerHeartbeatAt > staleBefore {
			continue
		}
		tasks = append(tasks, task)
		if len(tasks) >= limit {
			break
		}
	}
	return tasks
}

func getUnfinishedImageTasksForInternalAsync(limit int) []*Task {
	var tasks []*Task
	if limit <= 0 {
		limit = 1000
	}
	err := DB.Where("action IN ?", internalAsyncImageTaskActions()).
		Where("platform = ?", constant.TaskPlatformInternalImage).
		Where("status NOT IN ?", []TaskStatus{TaskStatusFailure, TaskStatusSuccess}).
		Order("id asc").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetTimedOutInternalAsyncImageTasks(cutoff int64, limit int) []*Task {
	var tasks []*Task
	if limit <= 0 {
		limit = 100
	}
	err := DB.Where("action IN ?", internalAsyncImageTaskActions()).
		Where("platform = ?", constant.TaskPlatformInternalImage).
		Where("status = ?", TaskStatusInProgress).
		Where("start_time > 0").
		Where("start_time < ?", cutoff).
		Order("id asc").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetSuccessfulImageTasksForCleanup(startIdx int, num int) []*Task {
	if num <= 0 {
		num = 500
	}
	var tasks []*Task
	err := DB.Where("status = ?", TaskStatusSuccess).
		Where("action IN ?", internalAsyncImageTaskActions()).
		Order("finish_time asc, id asc").
		Limit(num).
		Offset(startIdx).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetSuccessfulImageTasksForCleanupAfter(lastFinishTime int64, lastID int64, num int) []*Task {
	if num <= 0 {
		num = 500
	}
	var tasks []*Task
	err := DB.Where("status = ?", TaskStatusSuccess).
		Where("action IN ?", internalAsyncImageTaskActions()).
		Where("(finish_time > ? OR (finish_time = ? AND id > ?))", lastFinishTime, lastFinishTime, lastID).
		Order("finish_time asc, id asc").
		Limit(num).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetFailedImageTasksForDataCleanup(cutoff int64, startIdx int, num int) []*Task {
	if num <= 0 {
		num = 500
	}
	var tasks []*Task
	err := DB.Where("status = ?", TaskStatusFailure).
		Where("platform = ?", constant.TaskPlatformInternalImage).
		Where("action IN ?", internalAsyncImageTaskActions()).
		Where("finish_time > 0").
		Where("finish_time < ?", cutoff).
		Order("finish_time asc, id asc").
		Limit(num).
		Offset(startIdx).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetFailedImageTasksForDataCleanupAfter(cutoff int64, lastFinishTime int64, lastID int64, num int) []*Task {
	if num <= 0 {
		num = 500
	}
	var tasks []*Task
	err := DB.Where("status = ?", TaskStatusFailure).
		Where("platform = ?", constant.TaskPlatformInternalImage).
		Where("action IN ?", internalAsyncImageTaskActions()).
		Where("finish_time > 0 AND finish_time < ?", cutoff).
		Where("(finish_time > ? OR (finish_time = ? AND id > ?))", lastFinishTime, lastFinishTime, lastID).
		Order("finish_time asc, id asc").
		Limit(num).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func UpdateTaskPrivateData(id int64, privateData TaskPrivateData) error {
	if id == 0 {
		return nil
	}
	return DB.Model(&Task{}).Where("id = ?", id).Update("private_data", privateData).Error
}

func UpdateTaskData(id int64, data []byte) error {
	if id == 0 {
		return nil
	}
	return DB.Model(&Task{}).Where("id = ?", id).Update("data", json.RawMessage(data)).Error
}

func applySyncTaskQueryFilters(query *gorm.DB, queryParams SyncTaskQueryParams, includeAdminFilters bool) *gorm.DB {
	if includeAdminFilters {
		if queryParams.ChannelID != "" {
			query = query.Where("channel_id = ?", queryParams.ChannelID)
		}
		if queryParams.UserID != "" {
			query = query.Where("user_id = ?", queryParams.UserID)
		}
		if len(queryParams.UserIDs) != 0 {
			query = query.Where("user_id in (?)", queryParams.UserIDs)
		}
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		if queryParams.Status == string(TaskStatusQueued) {
			query = query.Where("status in (?)", []TaskStatus{TaskStatusQueued, TaskStatusSubmitted})
		} else {
			query = query.Where("status = ?", queryParams.Status)
		}
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.ModelName != "" {
		query = applyTaskModelNameFilter(query, queryParams.ModelName)
	}
	if queryParams.TokenName != "" {
		query = applyTaskTokenNameFilter(query, queryParams.TokenName)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	return query
}

func applyTaskTokenNameFilter(query *gorm.DB, tokenName string) *gorm.DB {
	tokenIDs := DB.Model(&Token{}).Select("id").Where("name = ?", tokenName)
	legacyTokenIDExpression := "CAST(json_extract(private_data, '$.token_id') AS INTEGER)"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		legacyTokenIDExpression = "CAST(NULLIF(private_data::json ->> 'token_id', '') AS INTEGER)"
	} else if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		legacyTokenIDExpression = "CAST(JSON_UNQUOTE(JSON_EXTRACT(private_data, '$.token_id')) AS UNSIGNED)"
	}
	return query.Where(
		"(token_name = ? OR token_id IN (?) OR "+legacyTokenIDExpression+" IN (?))",
		tokenName,
		tokenIDs,
		tokenIDs,
	)
}

func applyTaskModelNameFilter(query *gorm.DB, modelName string) *gorm.DB {
	pattern := containsLikePattern(modelName)
	if pattern == "%%" {
		return query
	}
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		return query.Where("(properties::jsonb ->> 'origin_model_name') LIKE ? ESCAPE '!'", pattern)
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		return query.Where("JSON_UNQUOTE(JSON_EXTRACT(properties, '$.origin_model_name')) LIKE ? ESCAPE '!'", pattern)
	default:
		return query.Where("json_extract(properties, '$.origin_model_name') LIKE ? ESCAPE '!'", pattern)
	}
}

func containsLikePattern(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	value = strings.ReplaceAll(value, "_", "!_")
	return "%" + value + "%"
}
