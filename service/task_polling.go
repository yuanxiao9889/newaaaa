package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"
)

// TaskPollingAdaptor 定义轮询所需的最小适配器接口，避免 service -> relay 的循环依赖
type TaskPollingAdaptor interface {
	Init(info *relaycommon.RelayInfo)
	FetchTask(ctx context.Context, baseURL string, key string, task *model.Task, proxy string) (*http.Response, error)
	ParseTaskResult(task *model.Task, resp *http.Response, body []byte) (*relaycommon.TaskInfo, error)
	// AdjustBillingOnComplete 在任务到达终态（成功/失败）时由轮询循环调用。
	// 返回正数触发差额结算（补扣/退还），返回 0 保持预扣费金额不变。
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

type BatchTaskPollingAdaptor interface {
	TaskPollingAdaptor
	FetchMode() string
	FetchBatchTasks(ctx context.Context, baseURL, key string, tasks []*model.Task, proxy string) (*http.Response, error)
	ParseBatchResult(tasks []*model.Task, resp *http.Response, body []byte) (map[string]*BatchTaskResult, error)
}

const (
	taskStatusQueryTimeout = 15 * time.Second

	pollClassOK           = "ok"
	pollClassOtherClient  = "other_client"
	pollClassNotFound     = "not_found"
	pollClassAuth         = "auth"
	pollClassTransient    = "transient"
	pollClassUnrecognized = "unrecognized"
	pollClassHookError    = "hook_error"
	pollClassTransport    = "transport_error"
)

type BatchTaskResult struct {
	TaskInfo   relaycommon.TaskInfo
	Action     string
	SubmitTime int64
	StartTime  int64
	FinishTime int64
	Data       any
}

// GetTaskAdaptorFunc 由 main 包注入，用于获取指定平台的任务适配器。
// 打破 service -> relay -> relay/channel -> service 的循环依赖。
var GetTaskAdaptorFunc func(platform constant.TaskPlatform) TaskPollingAdaptor

// TaskPollSummary is the result recorded on an async_task_poll system task row,
// summarizing one polling pass.
type TaskPollSummary struct {
	UnfinishedTasks  int `json:"unfinished_tasks"`
	PlatformsScanned int `json:"platforms_scanned"`
	NullTasksFailed  int `json:"null_tasks_failed"`
}

// sweepTimedOutTasks 在主轮询之前独立清理超时任务。
// 每次最多处理 100 条，剩余的下个周期继续处理。
// 使用 per-task CAS (UpdateWithStatus) 防止覆盖被正常轮询已推进的任务。
func sweepTimedOutTasks(ctx context.Context) {
	now := time.Now().Unix()
	var globalCutoff, imageCutoff int64
	if constant.TaskTimeoutMinutes > 0 {
		globalCutoff = now - int64(constant.TaskTimeoutMinutes)*60
	}
	imageTimeout := common.GetAsyncImageTaskTimeoutSeconds()
	if imageTimeout > 0 {
		imageCutoff = now - int64(imageTimeout)
	}
	if globalCutoff == 0 && imageCutoff == 0 {
		return
	}
	tasks := model.FindUnfinishedTasksPastDeadline(max(globalCutoff, imageCutoff), 100, func(task *model.Task) bool {
		if task.PrivateData.AudioQueue != nil {
			return false
		}
		if globalCutoff != 0 && task.PrivateData.MediaRetry.ExecutionTimeoutStart(task.SubmitTime) < globalCutoff {
			return true
		}
		deadline := pluginImageDeadline(task, imageTimeout)
		return deadline > 0 && now >= deadline
	})
	timedOutCount := 0
	for _, task := range tasks {
		if retry := task.PrivateData.VideoRetry; retry != nil && (retry.Submitting ||
			!retry.Ready && retry.AcceptedAt > 0 && now-retry.AcceptedAt < VideoTaskRetrySubmitTimeoutSeconds) {
			if _, err := prepareVideoTaskForPolling(ctx, task); err != nil {
				logger.LogError(ctx, fmt.Sprintf("recover video task %s resubmission failed: %v", task.TaskID, err))
			}
			continue
		}
		won, err := expirePolledTask(ctx, task, now)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("expire task %s: %v", task.TaskID, err))
		}
		if won {
			timedOutCount++
		}
	}

	if timedOutCount > 0 {
		logger.LogInfo(ctx, common.LogText("sweepTimedOutTasks: timed out %d tasks", timedOutCount))
	}
}

// The host's persisted submission time also covers tasks that stay queued
// upstream and never receive a start time.
func pluginImageDeadline(task *model.Task, timeout int) int64 {
	if task.PrivateData.InternalAsync || task.PrivateData.AudioQueue != nil ||
		task.Platform == constant.TaskPlatformInternalImage || task.Platform == constant.TaskPlatformMidjourney {
		return 0
	}
	submitted := task.SubmitTime
	if submitted <= 0 {
		submitted = task.CreatedAt
	}
	return PluginImageExecutionDeadline(taskPollingPath(task), task.Action, submitted, task.PrivateData.MediaRetry, timeout)
}

// PluginImageExecutionDeadline binds both the submission HTTP request and subsequent
// polling to the original image request's execution and retry allowance.
func PluginImageExecutionDeadline(path, action string, submitted int64, retry *model.MediaRetryState, timeout int) int64 {
	if submitted <= 0 || timeout <= 0 || requestPollingKind(path, action) != pollImage {
		return 0
	}
	return retry.ExecutionTimeoutStart(submitted) + int64(timeout)
}

func expirePluginImageIfDue(ctx context.Context, task *model.Task, deadline int64) (bool, error) {
	now := time.Now().Unix()
	if deadline == 0 || now < deadline {
		return false, nil
	}
	_, err := expirePolledTask(ctx, task, now)
	return true, err
}

func expirePolledTask(ctx context.Context, task *model.Task, now int64) (bool, error) {
	oldStatus := task.Status
	if oldStatus == model.TaskStatusSuccess || oldStatus == model.TaskStatusFailure {
		return false, nil
	}
	expired := *task
	expired.Status = model.TaskStatusFailure
	expired.Progress = "100%"
	expired.FinishTime = now
	expired.FailReason = "Task timed out"
	legacy := task.SubmitTime > 0 && task.SubmitTime < model.TaskRefundLegacyCutoff
	if legacy {
		expired.FailReason = "Task timed out (a task left by the old system is not refunded; contact the administrator)"
		expired.Quota = 0
	}
	won, err := expired.UpdateWithStatus(oldStatus)
	if err != nil || !won {
		return false, err
	}
	*task = expired
	RecordAutoSwitchTaskFailure(ctx, task, 0, "upstream_poll_timeout", task.FailReason)
	if !legacy && task.Quota != 0 {
		err = RefundTaskQuota(ctx, task, task.FailReason)
	}
	return true, err
}

type taskPollKind uint8

const (
	pollImage taskPollKind = iota
	pollVideo
	pollOther
	pollKindCount
)

// The original submission path identifies host protocols even when an older
// plugin persisted an empty or vendor-specific action.
func taskPollingPath(task *model.Task) string {
	if execution := task.PrivateData.Execution; execution != nil && execution.RequestPath != "" {
		return execution.RequestPath
	}
	return task.PrivateData.RequestPath
}

func pollingKind(task *model.Task) taskPollKind {
	return requestPollingKind(taskPollingPath(task), task.Action)
}

func requestPollingKind(path, action string) taskPollKind {
	switch strings.TrimSuffix(path, "/") {
	case "/v1/images/generations", "/v1/images/edits":
		return pollImage
	case "/v1/videos", "/v1/video/generations":
		return pollVideo
	}
	action = constant.NormalizeTaskAction(action)
	if IsImageTaskAction(action) {
		return pollImage
	}
	switch action {
	case "text_to_image", "image_to_image", "image_generation":
		return pollImage
	case constant.TaskActionImageToVideo, constant.TaskActionTextToVideo, constant.TaskActionFirstTailToVideo,
		constant.TaskActionReferenceToVideo, constant.TaskActionRemix, "video_generation", "video_edit", "video_extension":
		return pollVideo
	default:
		return pollOther
	}
}

// RunTaskPollingOnce performs one async-task (Suno/video) polling pass
// synchronously. It honors ctx cancellation (the system-task runner cancels it
// when the lease is lost) and, when report is non-nil, reports progress as
// (processedPlatforms, totalPlatforms). Durable usage delivery does not need an
// adaptor; upstream polling waits until the adaptor factory has been wired.
func RunTaskPollingOnce(ctx context.Context, report func(processed, total int)) TaskPollSummary {
	summary := TaskPollSummary{}
	if ctx == nil {
		ctx = context.Background()
	}
	// Completed tasks no longer enter upstream polling. Deliver their durable
	// usage checkpoints separately, without invoking any charging hooks.
	pendingUsage, err := model.GetPendingTaskUsageLogs(ctx, constant.TaskQueryLimit)
	if err != nil {
		logger.LogError(ctx, "load pending task usage failed: "+err.Error())
	}
	for _, task := range pendingUsage {
		if ctx.Err() != nil {
			return summary
		}
		if err := model.MergeTaskUsageLog(ctx, task); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("task %s usage delivery failed: %v", task.TaskID, err))
			// Rotate failed deliveries so one missing original cannot starve
			// other tasks when the queue exceeds this pass's batch limit.
			if updateErr := model.DB.WithContext(ctx).Model(&model.Task{}).Where("id = ?", task.ID).Update("updated_at", common.GetTimestamp()).Error; updateErr != nil {
				logger.LogWarn(ctx, "update usage delivery attempt failed: "+updateErr.Error())
			}
		}
	}
	if GetTaskAdaptorFunc == nil {
		return summary
	}

	common.SysLog(common.LogText("task progress polling started"))
	if err := model.PruneTaskRetryBodies(ctx, time.Now().Unix()); err != nil {
		logger.LogWarn(ctx, "prune expired retry request snapshots: "+err.Error())
	}
	sweepTimedOutTasks(ctx)
	allTasks := model.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
	summary.UnfinishedTasks = len(allTasks)
	type groupKey struct {
		platform  constant.TaskPlatform
		channelID int
		kind      taskPollKind
	}
	type pollGroup struct {
		groupKey
		ids   []string
		tasks map[string]*model.Task
	}

	var queues [pollKindCount][]*pollGroup
	groups := make(map[groupKey]*pollGroup)
	pending := make(map[constant.TaskPlatform]int)
	var nullIDs []int64
	for _, task := range allTasks {
		if ctx.Err() != nil {
			return summary
		}
		if task.PrivateData.AudioQueue != nil {
			continue
		}
		if _, seen := pending[task.Platform]; !seen {
			pending[task.Platform] = 0
		}
		poll, err := prepareVideoTaskForPolling(ctx, task)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("prepare video task %s polling failed: %v", task.TaskID, err))
		}
		if err != nil || !poll {
			continue
		}
		id := task.GetUpstreamTaskID()
		if id == "" {
			nullIDs = append(nullIDs, task.ID)
			continue
		}
		if task.Platform == constant.TaskPlatformInternalImage || task.Platform == constant.TaskPlatformMidjourney {
			continue
		}
		key := groupKey{task.Platform, task.ChannelId, pollingKind(task)}
		group := groups[key]
		if group == nil {
			group = &pollGroup{groupKey: key, tasks: make(map[string]*model.Task)}
			groups[key] = group
			queues[key.kind] = append(queues[key.kind], group)
			pending[key.platform]++
		}
		group.ids = append(group.ids, id)
		group.tasks[id] = task
	}
	if len(nullIDs) > 0 {
		summary.NullTasksFailed = len(nullIDs)
		err := model.TaskBulkUpdateByID(nullIDs, map[string]any{"status": model.TaskStatusFailure, "progress": "100%"})
		if err != nil {
			logger.LogError(ctx, common.LogText("Fix null task_id task error: %v", err))
		} else {
			logger.LogInfo(ctx, common.LogText("Fix null task_id task success: %v", nullIDs))
		}
	}

	summary.PlatformsScanned = len(pending)
	processed := 0
	for _, count := range pending {
		if count == 0 {
			processed++
		}
	}
	if report != nil {
		report(processed, summary.PlatformsScanned)
	}
	// A pass has a fixed snapshot. A closed, drained queue therefore has no
	// further work this pass, even if another worker is still polling a group.
	var jobs [pollKindCount]chan *pollGroup
	for kind, groups := range queues {
		jobs[kind] = make(chan *pollGroup, len(groups))
		for _, group := range groups {
			jobs[kind] <- group
		}
		close(jobs[kind])
	}

	var wg sync.WaitGroup
	var progressMu sync.Mutex
	startWorkers := func(count int, preferred taskPollKind, fallback ...taskPollKind) {
		order := append([]taskPollKind{preferred}, fallback...)
		available := 0
		for _, kind := range order {
			available += len(queues[kind])
		}
		for range min(count, available) {
			wg.Add(1)
			gopool.Go(func() {
				defer wg.Done()
				for _, kind := range order {
					for group := range jobs[kind] {
						if ctx.Err() != nil {
							return
						}
						DispatchPlatformUpdate(ctx, group.platform, map[int][]string{group.channelID: group.ids}, group.tasks)
						if report != nil {
							progressMu.Lock()
							pending[group.platform]--
							if pending[group.platform] == 0 && ctx.Err() == nil {
								processed++
								report(processed, summary.PlatformsScanned)
							}
							progressMu.Unlock()
						}
					}
				}
			})
		}
	}
	startWorkers(3, pollImage)
	startWorkers(2, pollVideo)
	startWorkers(1, pollOther)
	startWorkers(1, pollImage, pollVideo)
	startWorkers(1, pollVideo, pollImage)
	wg.Wait()
	common.SysLog(common.LogText("task progress polling finished"))
	return summary
}

// DispatchPlatformUpdate 按平台分发轮询更新
func DispatchPlatformUpdate(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) {
	if ctx == nil {
		ctx = context.Background()
	}
	if platform == constant.TaskPlatformMidjourney || platform == constant.TaskPlatformInternalImage {
		// MJ 轮询由其自身处理，这里预留入口
		return
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if batchAdaptor, ok := adaptor.(BatchTaskPollingAdaptor); ok && batchAdaptor.FetchMode() == "batch" {
		if err := UpdateBatchTasks(ctx, batchAdaptor, taskChannelM, taskM); err != nil {
			common.SysLog(common.LogText("UpdateBatchTasks fail: %s", err))
		}
		return
	}
	if err := UpdateVideoTasks(ctx, platform, taskChannelM, taskM); err != nil {
		common.SysLog(common.LogText("UpdateVideoTasks fail: %s", err))
	}
}

func UpdateBatchTasks(ctx context.Context, adaptor BatchTaskPollingAdaptor, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	for channelId, taskIds := range taskChannelM {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := updateBatchTasks(ctx, adaptor, channelId, taskIds, taskM)
		if err != nil {
			logger.LogError(ctx, common.LogText("channel #%d failed to update async tasks: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateBatchTasks(ctx context.Context, adaptor BatchTaskPollingAdaptor, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, common.LogText("channel #%d has %d unfinished tasks", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	ch, err := model.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(common.LogText("CacheGetChannel: %v", err))
		return err
	}
	proxy := ch.GetSetting().Proxy
	baseURL := ch.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(ch.Type)
	}
	imageTimeout := common.GetAsyncImageTaskTimeoutSeconds()
	tasks := make([]*model.Task, 0, len(taskIds))
	polledTasks := make(map[string]*model.Task, len(taskIds))
	for _, upstreamID := range taskIds {
		if task := taskM[upstreamID]; task != nil {
			if expired, err := expirePluginImageIfDue(ctx, task, pluginImageDeadline(task, imageTimeout)); expired {
				if err != nil {
					return err
				}
				continue
			}
			poll, err := prepareVideoTaskForPolling(ctx, task)
			if err != nil {
				return err
			}
			if !poll {
				continue
			}
			tasks = append(tasks, task)
			polledTasks[upstreamID] = task
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	// The channel type tells plugin adaptors whether the upstream is another
	// New API gateway, the same signal submission derives from the request.
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelType:     ch.Type,
		ChannelId:       ch.Id,
		ChannelBaseUrl:  baseURL,
		ChannelSetting:  ch.GetSetting(),
		HeadersOverride: ch.GetHeaderOverride(),
	}
	info.ApiKey = ch.Key
	adaptor.Init(info)
	queryCtx, cancel := context.WithTimeout(ctx, taskStatusQueryTimeout)
	defer cancel()
	resp, err := adaptor.FetchBatchTasks(queryCtx, baseURL, ch.Key, tasks, proxy)
	if err != nil {
		common.SysLog(common.LogText("Get Task Do req error: %v", err))
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassTransport, 0, err.Error())
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		common.SysLog(common.LogText("Get Suno Task parse body error: %v", err))
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassTransport, resp.StatusCode, err.Error())
	}
	switch classifyPollHTTP(resp.StatusCode) {
	case pollClassNotFound:
		for _, task := range tasks {
			if task != nil {
				logger.LogWarn(ctx, common.LogText("task %s poll %s (failures=%d, http=%d): %s", task.TaskID, pollClassNotFound, task.PrivateData.PollFailures, resp.StatusCode, unrecognizedPollDetail("", responseBody)))
			}
		}
		return failTasksFromPoll(ctx, adaptor, tasks, "Upstream task not found")
	case pollClassAuth:
		logger.LogWarn(ctx, common.LogText("task poll auth failure channel_id=%d http=%d", channelId, resp.StatusCode))
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassAuth, resp.StatusCode, "")
	case pollClassTransient:
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassTransient, resp.StatusCode, "")
	}
	responseItems, err := adaptor.ParseBatchResult(tasks, resp, responseBody)
	if err != nil {
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassHookError, resp.StatusCode, err.Error())
	}
	for upstreamID, responseItem := range responseItems {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		task := polledTasks[upstreamID]
		if task == nil {
			logger.LogWarn(ctx, common.LogText("Batch task response ignored: unknown task_id=%s", upstreamID))
			continue
		}
		deadline := pluginImageDeadline(task, imageTimeout)
		if expired, err := expirePluginImageIfDue(ctx, task, deadline); expired {
			if err != nil {
				return err
			}
			continue
		}
		snap := task.Snapshot()
		httpClass := classifyPollHTTP(resp.StatusCode)
		parsedStatus := model.TaskStatus(responseItem.TaskInfo.Status)
		if parsedStatus == model.TaskStatusUnknown || parsedStatus == "" || !knownPollStatus(parsedStatus) {
			if err := recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, responseItem.TaskInfo.Reason); err != nil {
				common.SysLog(common.LogText("UpdateSunoTask task error: %s", err.Error()))
			}
			continue
		}
		if httpClass == pollClassOtherClient && isNonTerminalPollStatus(parsedStatus) {
			if err := recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, responseItem.TaskInfo.Reason); err != nil {
				common.SysLog(common.LogText("UpdateSunoTask task error: %s", err.Error()))
			}
			continue
		}
		if parsedStatus == model.TaskStatusFailure && task.PrivateData.VideoRetry != nil && RetryVideoTaskFunc != nil {
			handled, err := RetryVideoTaskFunc(ctx, task, &responseItem.TaskInfo)
			if err != nil {
				return err
			}
			if handled {
				continue
			}
		}
		if isNonTerminalPollStatus(parsedStatus) {
			task.PrivateData.PollFailures = 0
		}
		if len(responseItem.TaskInfo.PluginState) > 0 {
			task.PrivateData.PluginState = responseItem.TaskInfo.PluginState
		}
		task.Status = lo.If(parsedStatus != "", parsedStatus).Else(task.Status)
		task.FailReason = lo.If(responseItem.TaskInfo.Reason != "", responseItem.TaskInfo.Reason).Else(task.FailReason)
		// Keep the host clock stable across batch polls and restarts.
		if responseItem.SubmitTime != 0 && pollingKind(task) != pollImage {
			task.SubmitTime = responseItem.SubmitTime
		}
		task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
		task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
		if responseItem.TaskInfo.Progress != "" {
			task.Progress = responseItem.TaskInfo.Progress
		}
		if responseItem.TaskInfo.Reason != "" || task.Status == model.TaskStatusFailure {
			logger.LogInfo(ctx, common.LogText("%s failed: %s", task.TaskID, task.FailReason))
			task.Status = model.TaskStatusFailure
			task.Progress = "100%"
		}
		if responseItem.TaskInfo.Status == model.TaskStatusSuccess {
			task.Progress = "100%"
		}
		if responseItem.Data != nil {
			task.SetData(responseItem.Data)
		} else if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
			logger.LogWarn(ctx, common.LogText(
				"Batch task %s reached terminal status without data; preserving existing task data",
				task.TaskID,
			))
		}
		if responseItem.TaskInfo.Url != "" {
			task.PrivateData.ResultURL = responseItem.TaskInfo.Url
		}

		isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
		terminalTransition := isDone && snap.Status != task.Status
		if task.Status == model.TaskStatusSuccess {
			captureTaskTokenUsage(task, responseItem.TaskInfo.TokenUsage)
		}
		won, updateErr := task.UpdateWithStatus(snap.Status)
		if updateErr != nil {
			common.SysLog(common.LogText("UpdateSunoTask task error: %s", updateErr.Error()))
			continue
		}
		if !won {
			logger.LogWarn(ctx, common.LogText("Batch task %s already transitioned by another process, skip billing", task.TaskID))
			continue
		}
		if terminalTransition {
			if err := finalizeTerminalTask(ctx, adaptor, task, &responseItem.TaskInfo); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdateVideoTasks 按渠道更新所有视频任务
func UpdateVideoTasks(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	channelIDs := make([]int, 0, len(taskChannelM))
	for channelID := range taskChannelM {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Ints(channelIDs)

	var wg sync.WaitGroup
	for _, channelId := range channelIDs {
		taskIds := taskChannelM[channelId]
		if len(taskIds) == 0 {
			continue
		}
		taskIds = append([]string(nil), taskIds...)

		wg.Add(1)
		gopool.Go(func() {
			defer wg.Done()
			if err := updateVideoTasks(ctx, platform, channelId, taskIds, taskM); err != nil {
				logger.LogError(ctx, common.LogText("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func updateVideoTasks(ctx context.Context, platform constant.TaskPlatform, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, common.LogText("Channel #%d pending video tasks: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	cacheGetChannel, err := model.CacheGetChannel(channelId)
	if err != nil {
		// Collect DB primary key IDs for bulk update (taskIds are upstream IDs, not task_id column values)
		var failedIDs []int64
		for _, upstreamID := range taskIds {
			if t, ok := taskM[upstreamID]; ok {
				if t.PrivateData.VideoRetry != nil {
					poll, prepareErr := prepareVideoTaskForPolling(ctx, t)
					if prepareErr != nil {
						logger.LogError(ctx, fmt.Sprintf("prepare video task %s polling failed: %v", t.TaskID, prepareErr))
						continue
					}
					if poll {
						if failErr := failVideoTaskRetry(ctx, t, fmt.Sprintf("Failed to get channel info, channel ID: %d", channelId)); failErr != nil {
							logger.LogError(ctx, fmt.Sprintf("fail video task %s after missing channel: %v", t.TaskID, failErr))
						}
					}
					continue
				}
				failedIDs = append(failedIDs, t.ID)
			}
		}
		errUpdate := model.TaskBulkUpdateByID(failedIDs, map[string]any{
			"fail_reason": "Failed to get the channel information. Please contact the administrator",
			"status":      "FAILURE",
			"progress":    "100%",
		})
		if errUpdate != nil {
			common.SysLog(common.LogText("UpdateVideoTask error: %v", errUpdate))
		}
		return fmt.Errorf("CacheGetChannel failed: %w", err)
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if adaptor == nil {
		return fmt.Errorf("video adaptor not found")
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelType:     cacheGetChannel.Type,
		ChannelId:       cacheGetChannel.Id,
		ChannelBaseUrl:  cacheGetChannel.GetBaseURL(),
		ChannelSetting:  cacheGetChannel.GetSetting(),
		HeadersOverride: cacheGetChannel.GetHeaderOverride(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	disablePollingSleep := cacheGetChannel.GetOtherSettings().DisableTaskPollingSleep
	for i, taskId := range taskIds {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, taskId, taskM); err != nil {
			logger.LogError(ctx, common.LogText("Failed to update video task %s: %s", taskId, err.Error()))
		}
		if disablePollingSleep || i == len(taskIds)-1 {
			continue
		}

		// sleep 1 second between tasks for this channel only.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return nil
}

func updateVideoSingleTask(ctx context.Context, adaptor TaskPollingAdaptor, ch *model.Channel, taskId string, taskM map[string]*model.Task) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	baseURL := constant.GetChannelBaseURL(ch.Type)
	if ch.GetBaseURL() != "" {
		baseURL = ch.GetBaseURL()
	}
	proxy := ch.GetSetting().Proxy

	task := taskM[taskId]
	if task == nil {
		logger.LogError(ctx, common.LogText("Task %s not found in taskM", taskId))
		return fmt.Errorf("task %s not found", taskId)
	}
	deadline := pluginImageDeadline(task, common.GetAsyncImageTaskTimeoutSeconds())
	if expired, err := expirePluginImageIfDue(ctx, task, deadline); expired {
		return err
	}
	poll, err := prepareVideoTaskForPolling(ctx, task)
	if err != nil || !poll {
		return err
	}
	key := ch.Key

	privateData := task.PrivateData
	if privateData.Key != "" {
		key = privateData.Key
	}
	snap := task.Snapshot()
	queryDeadline := time.Now().Add(taskStatusQueryTimeout)
	if deadline > 0 && deadline <= queryDeadline.Unix() {
		queryDeadline = time.Unix(deadline, 0)
	}
	queryCtx, cancel := context.WithDeadline(ctx, queryDeadline)
	defer cancel()
	resp, err := adaptor.FetchTask(queryCtx, baseURL, key, task, proxy)
	var responseBody []byte
	var statusCode int
	if err == nil {
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		responseBody, err = io.ReadAll(resp.Body)
	}
	if expired, err := expirePluginImageIfDue(ctx, task, deadline); expired {
		return err
	}
	if err != nil {
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassTransport, statusCode, err.Error())
	}

	logger.LogDebug(ctx, "updateVideoSingleTask response: %s", responseBody)

	switch classifyPollHTTP(resp.StatusCode) {
	case pollClassNotFound:
		logger.LogWarn(ctx, common.LogText("task %s poll %s (failures=%d, http=%d): %s", task.TaskID, pollClassNotFound, task.PrivateData.PollFailures, resp.StatusCode, unrecognizedPollDetail("", responseBody)))
		return failTaskFromPoll(ctx, adaptor, task, snap.Status, "Upstream task not found")
	case pollClassAuth:
		logger.LogWarn(ctx, common.LogText("task poll auth failure channel_id=%d task=%s http=%d", ch.Id, task.TaskID, resp.StatusCode))
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassAuth, resp.StatusCode, "")
	case pollClassTransient:
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassTransient, resp.StatusCode, "")
	}

	taskResult, parseErr := adaptor.ParseTaskResult(task, resp, responseBody)
	if parseErr == nil && taskResult != nil && taskResult.Status != "" {
		logger.LogDebug(ctx, "updateVideoSingleTask parsed by task adaptor: %+v", taskResult)
	} else {
		// Fall back to the generic New API task envelope for older upstreams.
		var responseItems taskdto.TaskResponse[model.Task]
		if err = common.Unmarshal(responseBody, &responseItems); err == nil && responseItems.IsSuccess() {
			logger.LogDebug(ctx, "updateVideoSingleTask parsed as new api response format: %+v", responseItems)
			t := responseItems.Data
			taskResult = &relaycommon.TaskInfo{
				TaskID:   t.TaskID,
				Status:   string(t.Status),
				Url:      t.GetResultURL(),
				Progress: t.Progress,
				Reason:   t.FailReason,
			}
			task.Data = t.Data
		} else if parseErr != nil {
			return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassHookError, resp.StatusCode, parseErr.Error())
		} else {
			taskResult = &relaycommon.TaskInfo{}
		}
	}

	logger.LogDebug(ctx, "updateVideoSingleTask taskResult: %+v", taskResult)

	if expired, err := expirePluginImageIfDue(ctx, task, deadline); expired {
		return err
	}
	parsedStatus := model.TaskStatus(taskResult.Status)
	if parsedStatus == model.TaskStatusUnknown || parsedStatus == "" || !knownPollStatus(parsedStatus) {
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, unrecognizedPollDetail(taskResult.Reason, responseBody))
	}
	if classifyPollHTTP(resp.StatusCode) == pollClassOtherClient && isNonTerminalPollStatus(parsedStatus) {
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, unrecognizedPollDetail(taskResult.Reason, responseBody))
	}
	if parsedStatus == model.TaskStatusFailure && task.PrivateData.VideoRetry != nil && RetryVideoTaskFunc != nil {
		handled, err := RetryVideoTaskFunc(ctx, task, taskResult)
		if err != nil || handled {
			return err
		}
	}

	task.Data = redactVideoResponseBody(responseBody)
	if len(taskResult.PluginState) > 0 {
		task.PrivateData.PluginState = taskResult.PluginState
	}
	if isNonTerminalPollStatus(parsedStatus) {
		task.PrivateData.PollFailures = 0
	}

	now := time.Now().Unix()
	shouldFinalizeBilling := false

	task.Status = parsedStatus
	switch parsedStatus {
	case model.TaskStatusSubmitted:
		task.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		task.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		task.Progress = taskcommon.ProgressInProgress
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case model.TaskStatusSuccess:
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if strings.HasPrefix(taskResult.Url, "data:") {
			// data: URI (e.g. Vertex base64 encoded video) — keep in Data, not in ResultURL
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		} else if taskResult.Url != "" {
			// Direct upstream URL (e.g. Kling, Ali, Doubao, etc.)
			task.PrivateData.ResultURL = taskResult.Url
		} else {
			// No URL from adaptor — construct proxy URL using public task ID
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
		shouldFinalizeBilling = true
	case model.TaskStatusFailure:
		logger.LogJson(ctx, common.LogText("Task %s failed", taskId), task)
		task.Status = model.TaskStatusFailure
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(ctx, common.LogText("Task %s failed: %s", task.TaskID, task.FailReason))
		taskResult.Progress = taskcommon.ProgressComplete
		shouldFinalizeBilling = true
	}
	if taskResult.Progress != "" {
		task.Progress = taskResult.Progress
	}

	isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if task.Status == model.TaskStatusSuccess {
		captureTaskTokenUsage(task, taskResult.TokenUsage)
	}
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			return fmt.Errorf("update task %s terminal status failed: %w", task.TaskID, err)
		}
		if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Task %s already transitioned by another process, skip billing", task.TaskID))
			return nil
		}
	} else if !snap.Equal(task.Snapshot()) {
		if _, err := task.UpdateWithStatus(snap.Status); err != nil {
			return fmt.Errorf("update task %s failed: %w", task.TaskID, err)
		}
	} else {
		// No changes, skip update
		logger.LogDebug(ctx, "No update needed for task %s", task.TaskID)
	}

	if shouldFinalizeBilling {
		if err := finalizeTerminalTask(ctx, adaptor, task, taskResult); err != nil {
			return err
		}
	}

	return nil
}

// finalizeTerminalTask 终态统一收尾（状态 CAS 赢家调用，恰好一次）：采样 + 结算 + 失败兜底退款。
func finalizeTerminalTask(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) error {
	perfmetrics.RecordTaskResult(task, taskResult)
	return SettleTaskBillingOnComplete(ctx, adaptor, task, taskResult)
}

func redactVideoResponseBody(body []byte) []byte {
	// Only the redacted members change; the rest keeps the upstream bytes.
	if !ojson.ValidBytes(body) || !ojson.GetBytesNoCopy(body, "@this").IsObject() {
		return body
	}
	doc := ojson.NewDoc(body)
	if resp := ojson.GetBytesNoCopy(body, "response"); resp.IsObject() {
		_ = doc.Delete("response.bytesBase64Encoded")
		if video := resp.Get("video"); video.Type == ojson.String {
			_ = doc.Set("response.video", truncateBase64(video.Str))
		}
		if videos := resp.Get("videos"); videos.IsArray() {
			for i, video := range videos.Array() {
				if video.IsObject() {
					_ = doc.Delete(fmt.Sprintf("response.videos.%d.bytesBase64Encoded", i))
				}
			}
		}
	}
	if steps := ojson.GetBytesNoCopy(body, "steps"); steps.IsArray() {
		for i, step := range steps.Array() {
			parts := step.Get("content")
			if !parts.IsArray() {
				continue
			}
			for j, part := range parts.Array() {
				if part.Get("type").String() == "video" {
					_ = doc.Delete(fmt.Sprintf("steps.%d.content.%d.data", i, j))
				}
			}
		}
	}
	return doc.Bytes()
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}

func SettleTaskBillingOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) error {
	if task == nil || taskResult == nil {
		return fmt.Errorf("missing task settlement result")
	}
	RecordAutoSwitchTaskResult(ctx, task, taskResult)
	if task.PrivateData.BillingState == TaskBillingStateSettled {
		if task.PrivateData.PendingUsageLog != nil {
			return model.MergeTaskUsageLog(ctx, task)
		}
		return nil
	}
	if task.PrivateData.BillingState == TaskBillingStateRefunded {
		return nil
	}
	failed := task.Status == model.TaskStatusFailure || taskResult.Status == string(model.TaskStatusFailure)
	if !failed && taskResult.TokenUsage != nil {
		captureTaskTokenUsage(task, taskResult.TokenUsage)
	}
	bc := task.PrivateData.BillingContext
	if !failed && task.PrivateData.BillingError != "" && task.PrivateData.ActualQuota >= 0 && task.PrivateData.ActualQuota != task.Quota {
		if err := RecalculateTaskQuota(ctx, task, task.PrivateData.ActualQuota, "retry deferred submit settlement"); err != nil {
			return err
		}
	}
	if bc != nil && bc.TieredSnapshot != nil {
		if failed {
			return RefundTaskQuota(ctx, task, task.FailReason)
		}
		result, usageFacts, err := EvaluateTaskCompletionUsage(bc.TieredSnapshot, taskResult.UsageFacts)
		if err != nil {
			markTaskBillingError(ctx, task, "task usage expression settlement failed: "+err.Error())
			return err
		}
		bc.TieredSnapshot.UsageFacts = usageFacts
		bc.TieredSnapshot.EstimatedTier = result.MatchedTier
		if err := RecalculateTaskQuota(ctx, task, result.ActualQuotaAfterGroup, "task usage expression settlement", result.Clamp); err != nil {
			return err
		}
	} else if bc != nil && bc.PerCallBilling {
		if failed {
			return RefundTaskQuota(ctx, task, task.FailReason)
		}
	} else {
		adjusted := false
		if task.PrivateData.BillingError != "" && task.PrivateData.ActualQuota > 0 && task.PrivateData.ActualQuota != task.Quota {
			if err := RecalculateTaskQuota(ctx, task, task.PrivateData.ActualQuota, "retry deferred submit settlement"); err != nil {
				return err
			}
			adjusted = true
		} else if actualQuota := adaptor.AdjustBillingOnComplete(task, taskResult); actualQuota > 0 {
			if err := RecalculateTaskQuota(ctx, task, actualQuota, "adaptor settlement"); err != nil {
				return err
			}
			adjusted = true
		} else {
			tokens := taskResult.TotalTokens
			if tokens == 0 {
				tokens = taskResult.CompletionTokens
			}
			modelRatio, found := taskModelRatio(task, taskModelName(task))
			if tokens > 0 && found && modelRatio > 0 {
				if err := RecalculateTaskQuotaByTokens(ctx, task, tokens); err != nil {
					return err
				}
				adjusted = true
			}
		}
		if failed && !adjusted {
			return RefundTaskQuota(ctx, task, task.FailReason)
		}
	}
	return ConfirmTaskBillingSettled(ctx, task, task.Quota, "异步任务完成")
}

func classifyPollHTTP(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return pollClassOK
	case statusCode == http.StatusNotFound || statusCode == http.StatusGone:
		return pollClassNotFound
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return pollClassAuth
	case statusCode == http.StatusTooManyRequests || statusCode >= 500:
		return pollClassTransient
	case statusCode >= 400 && statusCode < 500:
		return pollClassOtherClient
	default:
		return pollClassTransient
	}
}

func knownPollStatus(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued, model.TaskStatusInProgress, model.TaskStatusSuccess, model.TaskStatusFailure:
		return true
	default:
		return false
	}
}

func isNonTerminalPollStatus(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued, model.TaskStatusInProgress:
		return true
	default:
		return false
	}
}

// unrecognizedPollDetail pairs the plugin's reason with a bounded copy of the
// upstream body so the WARN line is enough to diagnose a parser gap.
func unrecognizedPollDetail(reason string, body []byte) string {
	const maxBodyChars = 512
	redacted := string(redactVideoResponseBody(body))
	if len(redacted) > maxBodyChars {
		redacted = redacted[:maxBodyChars] + "…"
	}
	if strings.TrimSpace(reason) == "" {
		return "body=" + redacted
	}
	return reason + "; body=" + redacted
}

func recordPollFailure(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, fromStatus model.TaskStatus, class string, statusCode int, detail string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	task.PrivateData.PollFailures++
	// TASK_POLL_MAX_FAILURES <= 0 disables the consecutive-failure cutoff, matching
	// TASK_TIMEOUT_MINUTES semantics; the 72h sweep (TASK_TIMEOUT_MINUTES default
	// 4320) remains the only backstop.
	giveUp := constant.TaskPollMaxFailures > 0 && task.PrivateData.PollFailures >= constant.TaskPollMaxFailures
	if giveUp || class == pollClassUnrecognized || class == pollClassHookError {
		// The redacted body is intentionally not persisted to Task.Data on these
		// paths, and the fail reason of a task that is given up is a fixed
		// sentence, so the WARN line is the only operator-visible copy of the
		// class, the status and what the plugin could not interpret.
		logger.LogWarn(ctx, common.LogText("task %s poll %s (failures=%d, http=%d): %s", task.TaskID, class, task.PrivateData.PollFailures, statusCode, detail))
	}
	if giveUp {
		return failTaskFromPoll(ctx, adaptor, task, fromStatus, "Failed to query the upstream task status")
	}
	if _, err := task.UpdateWithStatus(fromStatus); err != nil {
		return err
	}
	return nil
}

func recordPollFailureForTasks(ctx context.Context, adaptor TaskPollingAdaptor, tasks []*model.Task, class string, statusCode int, detail string) error {
	var firstErr error
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := recordPollFailure(ctx, adaptor, task, task.Status, class, statusCode, detail); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func failTaskFromPoll(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, fromStatus model.TaskStatus, reason string) error {
	now := time.Now().Unix()
	task.Status = model.TaskStatusFailure
	task.Progress = taskcommon.ProgressComplete
	if task.FinishTime == 0 {
		task.FinishTime = now
	}
	task.FailReason = reason
	won, err := task.UpdateWithStatus(fromStatus)
	if err != nil {
		return err
	}
	if !won {
		return nil
	}
	return finalizeTerminalTask(ctx, adaptor, task, relaycommon.FailTaskInfo(reason))
}

func failTasksFromPoll(ctx context.Context, adaptor TaskPollingAdaptor, tasks []*model.Task, reason string) error {
	var firstErr error
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := failTaskFromPoll(ctx, adaptor, task, task.Status, reason); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
