package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"maps"
	"strings"
	"time"
)

type Log struct {
	Id                int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2"`
	UserId            int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1;index:idx_user_token_usage,priority:1"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type;index:idx_user_token_usage,priority:3"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type;index:idx_user_token_usage,priority:2"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	UseTime           int    `json:"use_time" gorm:"default:0"`
	IsStream          bool   `json:"is_stream"`
	ChannelId         int    `json:"channel" gorm:"index"`
	ChannelName       string `json:"channel_name" gorm:"->"`
	TokenId           int    `json:"token_id" gorm:"default:0;index;index:idx_user_token_usage,priority:4"`
	Group             string `json:"group" gorm:"index"`
	Ip                string `json:"ip" gorm:"index;default:''"`
	RequestId         string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);index:idx_logs_upstream_request_id;default:''"`
	Other             string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown            = 0
	LogTypeTopup              = 1
	LogTypeConsume            = 2
	LogTypeManage             = 3
	LogTypeSystem             = 4
	LogTypeError              = 5
	LogTypeRefund             = 6
	LogTypeLogin              = 7
	LogTypePreConsumeRollback = 8
)

type RecordConsumeLogParams struct {
	ChannelId        int       `json:"channel_id"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	ModelName        string    `json:"model_name"`
	TokenName        string    `json:"token_name"`
	Quota            int       `json:"quota"`
	Content          string    `json:"content"`
	TokenId          int       `json:"token_id"`
	UseTimeSeconds   int       `json:"use_time_seconds"`
	IsStream         bool      `json:"is_stream"`
	Group            string    `json:"group"`
	Other            *LogOther `json:"other"`
}

type RecordTaskBillingLogParams struct {
	UserId           int
	LogType          int
	Content          string
	ChannelId        int
	ModelName        string
	Quota            int
	PromptTokens     int
	CompletionTokens int
	TokenId          int
	Group            string
	UseTimeSeconds   int
	Other            *LogOther
	NodeName         string // 任务发起节点；为空时回退当前节点
}

const logSearchCountLimit = 10000

type Stat struct {
	Quota int `json:"quota"`
	Rpm   int `json:"rpm"`
	Tpm   int `json:"tpm"`
}

type TokenUsageStat struct {
	TokenId       int `json:"token_id" gorm:"column:token_id"`
	Quota         int `json:"quota" gorm:"column:quota"`
	ConsumeQuota  int `json:"consume_quota" gorm:"column:consume_quota"`
	RefundQuota   int `json:"refund_quota" gorm:"column:refund_quota"`
	RollbackQuota int `json:"rollback_quota" gorm:"column:rollback_quota"`
}

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if strings.Contains(value, "%") {
		condition, pattern, err := buildLogLikeCondition(column, value)
		if err != nil {
			return nil, err
		}
		return tx.Where(condition, pattern), nil
	}
	return tx.Where(column+" = ?", value), nil
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern, err := sanitizeClickHouseLikePattern(value)
		if err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}

	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func sanitizeClickHouseLikePattern(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `_`, `\_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

func createLog(log *Log) error {
	ensureLogRequestId(log)
	return LOG_DB.Create(log).Error
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "request_id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].Id = startIdx + i + 1
	}
}

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].ChannelId = 0
		logs[i].ChannelName = ""
		logs[i].UpstreamRequestId = ""
		logs[i].Group = ""
		var publicError *common.PublicError
		var diagnostic struct {
			Code        string `json:"error_code"`
			Type        string `json:"error_type"`
			Status      int    `json:"status_code"`
			RequestPath string `json:"request_path"`
			AdminInfo   struct {
				MidjourneyError *string `json:"midjourney_error"`
			} `json:"admin_info"`
		}
		_ = common.UnmarshalJsonStr(logs[i].Other, &diagnostic)
		path := strings.TrimPrefix(diagnostic.RequestPath, "/")
		_, modePath, _ := strings.Cut(path, "/")
		isMidjourney := diagnostic.AdminInfo.MidjourneyError != nil || strings.HasPrefix(path, "mj/") || strings.HasPrefix(modePath, "mj/")
		// Free-form relay and settlement text can embed provider identities,
		// retry paths and upstream payloads, including in historical logs.
		switch logs[i].Type {
		case LogTypeError:
			classified := common.ClassifyPublicError(diagnostic.Code, logs[i].Content, diagnostic.Status, diagnostic.Type == "new_api_error")
			if isMidjourney {
				reason := logs[i].Content
				if diagnostic.AdminInfo.MidjourneyError != nil {
					reason = *diagnostic.AdminInfo.MidjourneyError
				}
				classified = common.MidjourneyPublicError(diagnostic.Code, reason, 0, diagnostic.Type == "new_api_error")
			}
			publicError = &classified
			logs[i].Content = classified.Summary()
		case LogTypeConsume, LogTypeRefund, LogTypePreConsumeRollback:
			logs[i].Content = ""
			if logs[i].Type != LogTypeConsume && diagnostic.AdminInfo.MidjourneyError != nil {
				classified := common.MidjourneyPublicError("", *diagnostic.AdminInfo.MidjourneyError, 0, false)
				publicError = &classified
				logs[i].Content = classified.Summary()
			}
		}
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityUser, logs[i].Type)
		if publicError != nil {
			// Only insert canonical fields after removing raw provider codes.
			other := make(map[string]any)
			_ = common.UnmarshalJsonStr(logs[i].Other, &other)
			if other == nil {
				other = make(map[string]any)
			}
			other["error_code"] = publicError.Code
			other["status_code"] = publicError.StatusCode
			if encoded, err := common.Marshal(other); err == nil {
				logs[i].Other = string(encoded)
			}
		}
	}
	assignDisplayLogIds(logs, startIdx)
}

// FormatAdminLogs removes root-only diagnostics while retaining operational
// admin_info. Root callers must not pass their results through this formatter.
func FormatAdminLogs(logs []*Log) {
	for i := range logs {
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityAdmin, logs[i].Type)
	}
}

// FormatRootLogs normalizes legacy metadata into the current scoped shape
// without removing root-only diagnostics.
func FormatRootLogs(logs []*Log) {
	for i := range logs {
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityRoot, logs[i].Type)
	}
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order(order).Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLogWithAdminInfo stores operator metadata under other.admin_info and
// an optional, user-visible operation descriptor under other.op for localization.
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo *AuditAdminInfo, operation *AuditOperation, request ...*gin.Context) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if logType == LogTypeManage {
		var c *gin.Context
		if len(request) > 0 {
			c = request[0]
		}
		actorRole := 0
		if c != nil {
			actorRole = c.GetInt("role")
		}
		RecordAuditLog(c, AuditLog{UserId: userId, Username: username, ActorRole: actorRole, Category: AuditCategoryOperation, Content: content, Other: AuditOther{AdminInfo: adminInfo, Op: operation}, Success: true})
		return
	}
	if len(request) > 0 && request[0] != nil {
		log.RequestId = request[0].GetString(common.RequestIdKey)
	}
	if adminInfo != nil || operation != nil {
		data, err := common.Marshal(AuditOther{AdminInfo: adminInfo, Op: operation})
		if err != nil {
			common.SysError("failed to encode log admin info: " + err.Error())
			return
		}
		log.Other = string(data)
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLoginLog writes new login events to the independent audit table.
// username 由调用方传入（登录流程已持有用户对象），避免额外的数据库查询。
// content 为英文兜底文本（用于导出）；action+params 供前端本地化渲染。
// other 包含 login_method、user_agent 等结构化信息。
func RecordLoginLog(userId, actorRole int, username string, content string, ip string, action string, params map[string]any, other AuditOther, request ...*gin.Context) {
	other.Op = &AuditOperation{Action: action, Params: params}
	var c *gin.Context
	if len(request) > 0 {
		c = request[0]
	}
	RecordAuditLog(c, AuditLog{UserId: userId, Username: username, ActorRole: actorRole, Category: AuditCategoryLogin, Action: action, Content: content, Ip: ip, Other: other, Success: true})
}

// RecordOperationAuditLog writes new operation/security events to the audit table.
// logUserId 为日志归属者，管理审计日志应归属实际操作者；目标资源/用户放入
// action params。username 内部按 logUserId 查询。content 为英文兜底文本（供导出使用）。
// action+params 写入 Other.op，供前端本地化渲染（普通用户可见，不含敏感信息）。
// adminInfo 存放操作者身份（写入 Other.admin_info，普通用户查询时剥离）；
// auditInfo 存放路由/方法/结果等中间件兜底信息（写入 Other.audit_info，普通用户查询时剥离）。
func RecordOperationAuditLog(logUserId, actorRole int, content string, ip string, action string, params map[string]any, adminInfo *AuditAdminInfo, auditInfo *AuditRequestInfo, request ...*gin.Context) {
	username, _ := GetUsernameById(logUserId, false)
	other := AuditOther{
		Op:        &AuditOperation{Action: action, Params: params},
		AdminInfo: adminInfo,
		AuditInfo: auditInfo,
	}
	var c *gin.Context
	if len(request) > 0 {
		c = request[0]
	}
	category := AuditCategoryOperation
	if adminInfo == nil {
		category = AuditCategorySecurity
	}
	status, success := 200, true
	if auditInfo != nil {
		status = auditInfo.Status
		success = auditInfo.Success
	}
	RecordAuditLog(c, AuditLog{UserId: logUserId, Username: username, ActorRole: actorRole, Category: category, Action: action, Content: content, Ip: ip, Status: status, Success: success, Other: other})
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	other := NewLogOther()
	other.MergeAdmin(map[string]any{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	})
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Ip:        callerIp,
		Other:     other.JSONString(),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other *LogOther) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(content)))
	if other == nil {
		other = NewLogOther()
	}
	other.CaptureModerationRejection(c, content)
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := other.JSONString()
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	// Attach after console logging: prompt snapshots belong exclusively to the
	// role-filtered usage log, not the application's general log output.
	if params.Other != nil {
		if reason, ok := params.Other.adminInfo["reject_reason"].(string); ok {
			params.Other.CaptureModerationRejection(c, reason)
		}
	}
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	createdAt := common.GetTimestamp()
	otherStr := params.Other.JSONString()
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	if common.DataExportEnabled {
		LogQuotaData(QuotaDataLogParams{
			UserID:    userId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			TokenUsed: params.PromptTokens + params.CompletionTokens,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  common.NodeName,
		})
	}
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) error {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return nil
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	createdAt := common.GetTimestamp()
	log := &Log{
		UserId:           params.UserId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             params.LogType,
		Content:          params.Content,
		TokenName:        tokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		Group:            params.Group,
		Other:            params.Other.JSONString(),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
		return err
	}
	if common.DataExportEnabled {
		nodeName := params.NodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		quotaData := QuotaDataLogParams{
			UserID:    params.UserId,
			Username:  username,
			ModelName: params.ModelName,
			CreatedAt: createdAt,
			TokenUsed: params.PromptTokens + params.CompletionTokens,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  nodeName,
		}
		switch params.LogType {
		case LogTypeConsume:
			quotaData.Quota = params.Quota
			LogQuotaData(quotaData)
		case LogTypeRefund:
			quotaData.Quota = -params.Quota
			LogQuotaDataDelta(quotaData, 0)
		}
	}
	return nil
}

// SettleSubmittedTaskUsage records completion without touching paid quota.
// A stale caller cannot recreate a checkpoint already delivered by another
// process; the persisted billing state, not its in-memory copy, is authoritative.
func SettleSubmittedTaskUsage(ctx context.Context, task *Task) error {
	var settled Task
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&settled, task.ID).Error; err != nil {
			return err
		}
		if settled.PrivateData.BillingState == "settled" {
			return nil
		}
		if settled.PrivateData.BillingState == "refunded" || settled.Quota != task.Quota || settled.PrivateData.GetUsageAccountingMode() != TaskUsageAccountingSubmit {
			return fmt.Errorf("task %s billing changed before usage settlement", task.TaskID)
		}
		// Delivery can finish after the terminal CAS but before settlement.
		// Preserve its receipt when this caller still holds an older snapshot.
		if settled.PrivateData.TokenUsageLogged {
			task.PrivateData.TokenUsageLogged = true
			task.PrivateData.PendingUsageLog = nil
		} else if settled.PrivateData.PendingUsageLog != nil {
			task.PrivateData.PendingUsageLog = settled.PrivateData.PendingUsageLog
		}
		settled.PrivateData = task.PrivateData
		return tx.Model(&Task{}).Where("id = ?", settled.ID).Update("private_data", settled.PrivateData).Error
	})
	if err == nil {
		*task = settled
	}
	return err
}

// MergeTaskUsageLog delivers an already-paid task's durable usage checkpoint.
// Log storage and the primary database may be independent. Log writes are
// idempotent; the statistics delta and checkpoint acknowledgment commit together.
func MergeTaskUsageLog(ctx context.Context, task *Task) error {
	var fresh Task
	if err := DB.WithContext(ctx).First(&fresh, task.ID).Error; err != nil {
		return err
	}
	pending := fresh.PrivateData.PendingUsageLog
	if pending == nil {
		*task = fresh
		return nil
	}
	if fresh.Status != TaskStatusSuccess || fresh.PrivateData.BillingState == "refunded" {
		return fmt.Errorf("task %s usage delivery requires a successful task", fresh.TaskID)
	}
	if !common.LogConsumeEnabled {
		return fmt.Errorf("task usage delivery is paused while consume logging is disabled")
	}
	delta, err := mergeTaskUsageLog(ctx, &fresh, pending)
	if err != nil {
		return err
	}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Task
		if err := lockForUpdate(tx).First(&current, fresh.ID).Error; err != nil {
			return err
		}
		if current.PrivateData.PendingUsageLog == nil {
			fresh = current
			return nil
		}
		if *current.PrivateData.PendingUsageLog != *pending {
			return fmt.Errorf("task %s usage checkpoint changed; retry required", fresh.TaskID)
		}
		if pending.ExportQuotaData && delta.TokenUsed != 0 {
			if err := applyQuotaDataDelta(tx, delta); err != nil {
				return err
			}
		}
		current.PrivateData.PendingUsageLog = nil
		current.PrivateData.TokenUsageLogged = true
		if err := tx.Model(&Task{}).Where("id = ?", current.ID).Update("private_data", current.PrivateData).Error; err != nil {
			return err
		}
		fresh = current
		return nil
	})
	if err == nil {
		*task = fresh
	}
	return err
}

// mergeTaskUsageLog changes only usage fields, preserving the original charge
// and request identity. The persisted delta lets primary-database delivery
// resume after a process restart without counting the same tokens twice.
func mergeTaskUsageLog(ctx context.Context, task *Task, pending *TaskUsageLog) (*QuotaData, error) {
	if task.TaskID == "" {
		return nil, fmt.Errorf("task usage merge requires a task ID")
	}
	encodedID, err := common.Marshal(task.TaskID)
	if err != nil {
		return nil, err
	}
	var candidates []Log
	// LIKE is only a portable prefilter; exact decoded IDs are checked below.
	query := LOG_DB.WithContext(ctx).Where("user_id = ? AND model_name = ? AND type = ? AND other LIKE ?",
		task.UserId, pending.ModelName, LogTypeConsume, "%"+string(encodedID)+"%")
	if err := query.Order("created_at ASC").Order("id ASC").Find(&candidates).Error; err != nil {
		return nil, err
	}
	for _, original := range candidates {
		var identity struct {
			TaskID          string `json:"task_id"`
			UsageOnly       bool   `json:"usage_only"`
			UsageMerged     bool   `json:"usage_merged"`
			UsageTokenDelta *int   `json:"usage_token_delta"`
			ActualQuota     *int   `json:"actual_quota"`
			RootInfo        struct {
				NodeName string `json:"node_name"`
			} `json:"root_info"`
		}
		if common.UnmarshalJsonStr(original.Other, &identity) != nil || identity.TaskID != task.TaskID || identity.UsageOnly {
			continue
		}
		// Difference settlements are independent accounting events.
		if identity.ActualQuota != nil && !identity.UsageMerged {
			continue
		}
		nodeName := identity.RootInfo.NodeName
		if nodeName == "" {
			nodeName = task.PrivateData.NodeName
		}
		if nodeName == "" {
			nodeName = common.NodeName
		}
		delta := &QuotaData{
			UserID: original.UserId, Username: original.Username, ModelName: original.ModelName,
			CreatedAt: original.CreatedAt - original.CreatedAt%3600,
			TokenUsed: pending.PromptTokens + pending.CompletionTokens - original.PromptTokens - original.CompletionTokens,
			UseGroup:  original.Group, TokenID: original.TokenId, ChannelID: original.ChannelId, NodeName: nodeName,
		}
		if identity.UsageMerged {
			if identity.UsageTokenDelta == nil || original.PromptTokens != pending.PromptTokens || original.CompletionTokens != pending.CompletionTokens {
				return nil, fmt.Errorf("task %s merged usage differs from its checkpoint", task.TaskID)
			}
			delta.TokenUsed = *identity.UsageTokenDelta
			return delta, nil
		}
		var merged, supplement map[string]json.RawMessage
		if err := common.UnmarshalJsonStr(original.Other, &merged); err != nil {
			return nil, err
		}
		if err := common.UnmarshalJsonStr(pending.Other, &supplement); err != nil {
			return nil, err
		}
		for _, scope := range []string{logOtherAdminInfoKey, logOtherRootInfoKey, logOtherAuditInfoKey} {
			if supplement[scope] == nil {
				continue
			}
			values := make(map[string]json.RawMessage)
			if raw := merged[scope]; raw != nil {
				if err := common.Unmarshal(raw, &values); err != nil {
					return nil, err
				}
			}
			var additions map[string]json.RawMessage
			if err := common.Unmarshal(supplement[scope], &additions); err != nil {
				return nil, err
			}
			if values == nil {
				values = make(map[string]json.RawMessage)
			}
			maps.Copy(values, additions)
			supplement[scope], err = common.Marshal(values)
			if err != nil {
				return nil, err
			}
		}
		maps.Copy(merged, supplement)
		delete(merged, "usage_only")
		merged["usage_merged"] = json.RawMessage("true")
		merged["usage_token_delta"], err = common.Marshal(delta.TokenUsed)
		if err != nil {
			return nil, err
		}
		encoded, err := common.Marshal(merged)
		if err != nil {
			return nil, err
		}
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			// ClickHouse mutations do not report matched rows. Wait for the
			// mutation and verify the exact event before acknowledging delivery.
			err = LOG_DB.WithContext(ctx).Exec("ALTER TABLE logs UPDATE prompt_tokens = ?, completion_tokens = ?, use_time = ?, other = ? WHERE user_id = ? AND created_at = ? AND request_id = ? AND other = ? SETTINGS mutations_sync = 1",
				pending.PromptTokens, pending.CompletionTokens, pending.UseTimeSeconds, string(encoded), original.UserId, original.CreatedAt, original.RequestId, original.Other).Error
			if err != nil {
				return nil, err
			}
			var verified Log
			err = LOG_DB.WithContext(ctx).Where("user_id = ? AND created_at = ? AND request_id = ?", original.UserId, original.CreatedAt, original.RequestId).Take(&verified).Error
			if err != nil {
				return nil, err
			}
			if verified.Other != string(encoded) || verified.PromptTokens != pending.PromptTokens || verified.CompletionTokens != pending.CompletionTokens {
				return nil, fmt.Errorf("task %s usage mutation was not applied; retry required", task.TaskID)
			}
		} else {
			result := LOG_DB.WithContext(ctx).Model(&Log{}).Where("id = ? AND user_id = ? AND other = ?", original.Id, original.UserId, original.Other).
				Updates(map[string]any{"prompt_tokens": pending.PromptTokens, "completion_tokens": pending.CompletionTokens, "use_time": pending.UseTimeSeconds, "other": string(encoded)})
			if result.Error != nil {
				return nil, result.Error
			}
			if result.RowsAffected != 1 {
				return nil, fmt.Errorf("task %s consume log changed during usage merge; retry required", task.TaskID)
			}
		}
		return delta, nil
	}
	return nil, fmt.Errorf("original consume log for task %s: %w", task.TaskID, gorm.ErrRecordNotFound)
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", username); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	order := "logs.created_at desc, logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, startIdx)
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	hydrateLogDisplayNames(logs)
	return logs, total, err
}

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("logs.user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError("failed to count user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError("failed to search user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}

	hydrateLogDisplayNames(logs)
	formatUserLogs(logs, startIdx)
	return logs, total, err
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat, err error) {
	tx := LOG_DB.Table("logs").Select(
		"COALESCE(SUM(CASE WHEN type = ? THEN quota WHEN type = ? THEN -quota ELSE 0 END), 0) quota",
		LogTypeConsume,
		LogTypeRefund,
	)

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery := LOG_DB.Table("logs").Select("count(*) rpm, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) tpm")

	if tx, err = applyExplicitLogTextFilter(tx, "username", username); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "username", username); err != nil {
		return stat, err
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", modelName); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "model_name", modelName); err != nil {
		return stat, err
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type IN ?", []int{LogTypeConsume, LogTypeRefund})
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	if err := tx.Scan(&stat).Error; err != nil {
		common.SysError("failed to query log stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	var rateStat struct {
		Rpm int
		Tpm int
	}
	if err := rpmTpmQuery.Scan(&rateStat).Error; err != nil {
		common.SysError("failed to query rpm/tpm stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	stat.Rpm = rateStat.Rpm
	stat.Tpm = rateStat.Tpm

	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if nil != ctx.Err() {
		return 0, ctx.Err()
	}

	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse DELETE is a heavy mutation that rewrites data parts, so
		// per-batch mutations would be pathologically slow. Remove all matching
		// rows in a single synchronous mutation regardless of limit; the reported
		// count lets the caller's progress loop complete in one pass.
		total, err := CountOldLog(ctx, targetTimestamp)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			"ALTER TABLE logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	result := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
	if nil != result.Error {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func FormatUserLogsForResponse(logs []*Log, startIdx int) {
	formatUserLogs(logs, startIdx)
}

func hydrateLogDisplayNames(logs []*Log) {
	if len(logs) == 0 {
		return
	}

	userIds := types.NewSet[int]()
	tokenIds := types.NewSet[int]()
	for _, log := range logs {
		if log.Username == "" && log.UserId > 0 {
			userIds.Add(log.UserId)
		}
		if log.TokenName == "" && log.TokenId > 0 {
			tokenIds.Add(log.TokenId)
		}
	}

	usernames := make(map[int]string, userIds.Len())
	if userIds.Len() > 0 {
		var users []struct {
			Id       int    `gorm:"column:id"`
			Username string `gorm:"column:username"`
		}
		if err := DB.Model(&User{}).Select("id, username").Where("id IN ?", userIds.Items()).Find(&users).Error; err != nil {
			common.SysLog("failed to hydrate log usernames: " + err.Error())
		}
		for _, user := range users {
			usernames[user.Id] = user.Username
		}
	}

	tokenNames := make(map[int]string, tokenIds.Len())
	if tokenIds.Len() > 0 {
		var tokens []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if err := DB.Model(&Token{}).Select("id, name").Where("id IN ?", tokenIds.Items()).Find(&tokens).Error; err != nil {
			common.SysLog("failed to hydrate log token names: " + err.Error())
		}
		for _, token := range tokens {
			tokenNames[token.Id] = token.Name
		}
	}

	for i := range logs {
		if logs[i].Username == "" {
			logs[i].Username = usernames[logs[i].UserId]
		}
		if logs[i].TokenName == "" {
			logs[i].TokenName = tokenNames[logs[i].TokenId]
		}
	}
}

func SumUserTokenUsageStats(userId int, tokenIds []int, startTimestamp int64, endTimestamp int64) ([]TokenUsageStat, error) {
	if userId <= 0 || len(tokenIds) == 0 {
		return []TokenUsageStat{}, nil
	}

	var stats []TokenUsageStat
	err := LOG_DB.Model(&Log{}).
		Select(
			`token_id,
			COALESCE(SUM(CASE WHEN type = ? THEN quota WHEN type = ? THEN -quota ELSE 0 END), 0) AS quota,
			COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0) AS consume_quota,
			COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0) AS refund_quota,
			COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0) AS rollback_quota`,
			LogTypeConsume, LogTypeRefund, LogTypeConsume, LogTypeRefund, LogTypePreConsumeRollback,
		).
		Where("user_id = ?", userId).
		Where("token_id IN ?", tokenIds).
		Where("type IN ?", []int{LogTypeConsume, LogTypeRefund, LogTypePreConsumeRollback}).
		Where("created_at >= ? AND created_at < ?", startTimestamp, endTimestamp).
		Group("token_id").
		Scan(&stats).Error
	return stats, err
}
