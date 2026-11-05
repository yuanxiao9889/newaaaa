package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// midjourneyPollSummary is the result recorded on a midjourney_poll system task
// row, summarizing one polling pass.
type midjourneyPollSummary struct {
	UnfinishedTasks int `json:"unfinished_tasks"`
	ChannelsScanned int `json:"channels_scanned"`
	NullTasksFailed int `json:"null_tasks_failed"`
	TasksFetched    int `json:"tasks_fetched"`
	TasksUpdated    int `json:"tasks_updated"`
	ChannelsFailed  int `json:"channels_failed"`
}

// runMidjourneyTaskUpdateOnce performs one Midjourney polling pass synchronously.
// It honors ctx cancellation (the system-task runner cancels it when the lease
// is lost) and, when report is non-nil, reports progress as (processedChannels,
// totalChannels) so the system task surfaces a percentage.
func runMidjourneyTaskUpdateOnce(ctx context.Context, report func(processed, total int)) (midjourneyPollSummary, error) {
	summary := midjourneyPollSummary{}
	var pollErrors []error
	if ctx == nil {
		ctx = context.Background()
	}

	tasks, err := model.GetAllUnFinishTasks()
	if err != nil {
		return summary, fmt.Errorf("read unfinished Midjourney tasks: %w", err)
	}
	if len(tasks) == 0 {
		return summary, errors.Join(pollErrors...)
	}
	summary.UnfinishedTasks = len(tasks)

	logger.LogInfo(ctx, common.LogText("unfinished tasks found: %v", len(tasks)))
	taskChannelM := make(map[int][]string)
	taskM := make(map[string]*model.Midjourney)
	nullTaskIds := make([]int, 0)
	for _, task := range tasks {
		if task.MjId == "" {
			// 统计失败的未完成任务
			nullTaskIds = append(nullTaskIds, task.Id)
			continue
		}
		taskM[task.MjId] = task
		taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], task.MjId)
	}
	if len(nullTaskIds) > 0 {
		summary.NullTasksFailed = len(nullTaskIds)
		err := model.MjBulkUpdateByTaskIds(nullTaskIds, map[string]any{
			"status":   "FAILURE",
			"progress": "100%",
		})
		if err != nil {
			logger.LogError(ctx, common.LogText("Fix null mj_id task error: %v", err))
			pollErrors = append(pollErrors, fmt.Errorf("repair tasks without upstream IDs: %w", err))
		} else {
			logger.LogInfo(ctx, common.LogText("Fix null mj_id task success: %v", nullTaskIds))
		}
	}
	if len(taskChannelM) == 0 {
		return summary, errors.Join(pollErrors...)
	}

	totalChannels := len(taskChannelM)
	processedChannels := 0
	for channelId, taskIds := range taskChannelM {
		if ctx.Err() != nil {
			pollErrors = append(pollErrors, ctx.Err())
			break
		}
		if report != nil {
			report(processedChannels, totalChannels)
		}
		processedChannels++
		summary.ChannelsScanned++
		logger.LogInfo(ctx, common.LogText("channel #%d has %d unfinished tasks", channelId, len(taskIds)))
		if len(taskIds) == 0 {
			continue
		}
		midjourneyChannel, err := model.CacheGetChannel(channelId)
		if err != nil {
			// A failed channel lookup is a synchronization failure, not proof
			// that the upstream generation failed.
			summary.ChannelsFailed++
			pollErrors = append(pollErrors, fmt.Errorf("channel %d lookup: %w", channelId, err))
			continue
		}
		channelCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		responseItems, queryErr := fetchMidjourneyTasks(channelCtx, midjourneyChannel, taskIds)
		cancel()
		if queryErr != nil {
			summary.ChannelsFailed++
			pollErrors = append(pollErrors, fmt.Errorf("channel %d task query: %w", channelId, queryErr))
			logger.LogError(ctx, fmt.Sprintf("Midjourney channel %d polling failed: %v", channelId, queryErr))
		}
		summary.TasksFetched += len(responseItems)

		for _, responseItem := range responseItems {
			task := taskM[responseItem.MjId]
			if task == nil || task.ChannelId != channelId {
				logger.LogWarn(ctx, common.LogText("Midjourney task response ignored: unknown mj_id=%s", responseItem.MjId))
				continue
			}

			// Elapsed wall time does not prove upstream failure. A query outage
			// must not cause a refund for a task that can still finish upstream.
			if responseItem.Status == "SUCCESS" || responseItem.Status == "FAILURE" {
				responseItem.Progress = "100%"
			}
			if !checkMjTaskNeedUpdate(task, responseItem) {
				continue
			}
			preStatus := task.Status
			task.Code = 1
			task.Progress = responseItem.Progress
			task.PromptEn = responseItem.PromptEn
			task.State = responseItem.State
			task.SubmitTime = responseItem.SubmitTime
			task.StartTime = responseItem.StartTime
			task.FinishTime = responseItem.FinishTime
			task.ImageUrl = responseItem.ImageUrl
			task.Status = responseItem.Status
			task.FailReason = responseItem.FailReason
			if responseItem.Properties != nil {
				propertiesStr, _ := common.Marshal(responseItem.Properties)
				task.Properties = string(propertiesStr)
			}
			if responseItem.Buttons != nil {
				buttonStr, _ := common.Marshal(responseItem.Buttons)
				task.Buttons = string(buttonStr)
			}
			// 映射 VideoUrl
			task.VideoUrl = responseItem.VideoUrl

			// 映射 VideoUrls - 将数组序列化为 JSON 字符串
			if responseItem.VideoUrls != nil && len(responseItem.VideoUrls) > 0 {
				videoUrlsStr, err := common.Marshal(responseItem.VideoUrls)
				if err != nil {
					logger.LogError(ctx, common.LogText("failed to serialize VideoUrls: %v", err))
					task.VideoUrls = "[]" // 失败时设置为空数组
				} else {
					task.VideoUrls = string(videoUrlsStr)
				}
			} else {
				task.VideoUrls = "" // 空值时清空字段
			}

			shouldReturnQuota := false
			if (task.Progress != "100%" && responseItem.FailReason != "") || (task.Progress == "100%" && task.Status == "FAILURE") {
				logger.LogInfo(ctx, common.LogText("%s failed: %s", task.MjId, task.FailReason))
				task.Progress = "100%"
				if task.Quota != 0 {
					shouldReturnQuota = true
				}
			}
			asyncTask, asyncTaskExists, won, err := syncMidjourneyAsyncTask(ctx, task, preStatus, responseItem)
			if err != nil {
				logger.LogError(ctx, common.LogText("UpdateMidjourneyTask task error: %s", err.Error()))
				pollErrors = append(pollErrors, fmt.Errorf("save Midjourney task %s: %w", task.MjId, err))
			} else if won {
				summary.TasksUpdated++
				if shouldReturnQuota {
					if asyncTask != nil {
						if err := service.RefundTaskQuota(ctx, asyncTask, "构图失败"); err != nil {
							logger.LogError(ctx, fmt.Sprintf("refund Midjourney async task %s failed: %v", asyncTask.TaskID, err))
							pollErrors = append(pollErrors, fmt.Errorf("refund Midjourney task %s: %w", task.MjId, err))
						}
					} else if !asyncTaskExists {
						service.RefundMidjourneyQuota(ctx, task, "构图失败")
					}
				}
			}
		}
	}
	if report != nil && (ctx == nil || ctx.Err() == nil) {
		report(totalChannels, totalChannels)
	}
	return summary, errors.Join(pollErrors...)
}

// fetchMidjourneyTasks uses bounded batches and recovers missing results through
// the standard single-task endpoint. Stop after the first failed fallback so an
// upstream outage cannot turn one batch into hundreds of timed-out requests.
func fetchMidjourneyTasks(ctx context.Context, channel *model.Channel, taskIDs []string) ([]dto.MidjourneyDto, error) {
	var results []dto.MidjourneyDto
	for len(taskIDs) > 0 {
		batch := taskIDs[:min(len(taskIDs), 100)]
		taskIDs = taskIDs[len(batch):]
		items, batchErr := queryMidjourneyTasks(ctx, channel, batch, false)
		byID := make(map[string]dto.MidjourneyDto, len(items))
		for _, item := range items {
			byID[item.MjId] = item
		}
		var missing []string
		for _, id := range batch {
			if item, ok := byID[id]; ok {
				results = append(results, item)
			} else {
				missing = append(missing, id)
			}
		}
		for _, id := range missing {
			items, err := queryMidjourneyTasks(ctx, channel, []string{id}, true)
			if err != nil {
				return results, errors.Join(batchErr, fmt.Errorf("single task %s: %w", id, err))
			}
			if len(items) != 1 || items[0].MjId != id {
				return results, fmt.Errorf("upstream omitted or mismatched task %s", id)
			}
			results = append(results, items[0])
		}
	}
	return results, nil
}

func queryMidjourneyTasks(ctx context.Context, channel *model.Channel, taskIDs []string, individual bool) ([]dto.MidjourneyDto, error) {
	requestURL := strings.TrimRight(channel.GetBaseURL(), "/") + "/mj/task/list-by-condition"
	method := http.MethodPost
	var body io.Reader
	if individual {
		method = http.MethodGet
		requestURL = strings.TrimRight(channel.GetBaseURL(), "/") + "/mj/task/" + url.PathEscape(taskIDs[0]) + "/fetch"
	} else {
		encoded, err := common.Marshal(map[string]any{"ids": taskIDs})
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, requestURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mj-api-secret", channel.Key)
	resp, err := service.GetHttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream task query HTTP %d", resp.StatusCode)
	}
	// Never log raw upstream bodies: they may include prompts or signed URLs.
	const maxResponseBytes = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("upstream task query response exceeds 8 MiB")
	}
	var items []dto.MidjourneyDto
	if individual {
		var item dto.MidjourneyDto
		if err := common.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("invalid upstream task response: %w", err)
		}
		items = []dto.MidjourneyDto{item}
	} else if err := common.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("invalid upstream task list: %w", err)
	}
	return items, nil
}

func checkMjTaskNeedUpdate(oldTask *model.Midjourney, newTask dto.MidjourneyDto) bool {
	if oldTask.Code != 1 {
		return true
	}
	if oldTask.Progress != newTask.Progress {
		return true
	}
	if oldTask.PromptEn != newTask.PromptEn {
		return true
	}
	if oldTask.State != newTask.State {
		return true
	}
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if oldTask.ImageUrl != newTask.ImageUrl {
		return true
	}
	if oldTask.Status != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if oldTask.Progress != "100%" && newTask.FailReason != "" {
		return true
	}
	// 检查 VideoUrl 是否需要更新
	if oldTask.VideoUrl != newTask.VideoUrl {
		return true
	}
	// 检查 VideoUrls 是否需要更新
	if newTask.VideoUrls != nil && len(newTask.VideoUrls) > 0 {
		newVideoUrlsStr, _ := common.Marshal(newTask.VideoUrls)
		if oldTask.VideoUrls != string(newVideoUrlsStr) {
			return true
		}
	} else if oldTask.VideoUrls != "" {
		// 如果新数据没有 VideoUrls 但旧数据有，需要更新（清空）
		return true
	}

	return false
}

func syncMidjourneyAsyncTask(ctx context.Context, mjTask *model.Midjourney, preStatus string, responseItem dto.MidjourneyDto) (*model.Task, bool, bool, error) {
	asyncTask, exists, err := model.GetByTaskId(mjTask.UserId, mjTask.MjId)
	if err != nil {
		return nil, true, false, err
	}
	if !exists {
		asyncTask = nil
	}
	if exists && asyncTask.Platform != constant.TaskPlatformMidjourney {
		return nil, true, false, fmt.Errorf("task %s has unexpected platform %s", mjTask.MjId, asyncTask.Platform)
	}
	var changedTask *model.Task
	var preAsyncStatus model.TaskStatus
	if exists {
		preAsyncStatus = asyncTask.Status
		if service.ApplyMidjourneyUpdateToAsyncTask(asyncTask, responseItem) {
			changedTask = asyncTask
		}
	}
	won, err := mjTask.UpdateWithAsyncTask(ctx, preStatus, changedTask, preAsyncStatus)
	return asyncTask, exists, won, err
}

func refundLegacyMidjourneyTask(ctx context.Context, task *model.Midjourney) {
	if task == nil || task.Quota == 0 {
		return
	}
	if err := model.AdjustUserQuotaImmediately(task.UserId, task.Quota); err != nil {
		logger.LogError(ctx, "fail to increase user quota: "+err.Error())
		return
	}
	if err := model.AdjustTaskUsageAccounting(task.UserId, task.ChannelId, -task.Quota, 0); err != nil {
		if compensationErr := model.AdjustUserQuotaImmediately(task.UserId, -task.Quota); compensationErr != nil {
			logger.LogError(ctx, fmt.Sprintf("legacy Midjourney usage rollback failed for task %s: %v", task.MjId, compensationErr))
		}
		logger.LogError(ctx, fmt.Sprintf("adjust legacy Midjourney usage failed for task %s: %v", task.MjId, err))
		return
	}
	other := model.NewLogOther()
	other.SetPublic("task_id", task.MjId)
	other.SetPublic("reason", "构图失败")
	other.SetAdmin("midjourney_error", task.FailReason)
	other.SetPublic("unattributed_refund", true)
	if err := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: service.CovertMjpActionToModelName(task.Action),
		Quota:     task.Quota,
		Other:     other,
	}); err != nil {
		logger.LogError(ctx, fmt.Sprintf("record legacy Midjourney refund log failed for task %s: %v", task.MjId, err))
	}
}

func GetAllMidjourney(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	// 解析其他查询参数
	queryParams := model.TaskQueryParams{
		ChannelID:      c.Query("channel_id"),
		MjID:           c.Query("mj_id"),
		StartTimestamp: c.Query("start_timestamp"),
		EndTimestamp:   c.Query("end_timestamp"),
	}

	items := model.GetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := model.CountAllTasks(queryParams)

	if setting.MjForwardUrlEnabled {
		for i, midjourney := range items {
			midjourney.ImageUrl = service.BuildMidjourneyImageURL(midjourney)
			items[i] = midjourney
		}
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func GetUserMidjourney(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	userId := c.GetInt("id")

	queryParams := model.TaskQueryParams{
		MjID:           c.Query("mj_id"),
		StartTimestamp: c.Query("start_timestamp"),
		EndTimestamp:   c.Query("end_timestamp"),
	}

	items := model.GetAllUserTask(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := model.CountAllUserTask(userId, queryParams)

	for _, task := range items {
		failReason := task.UserFailReason()
		task.ChannelId = 0
		task.Properties = ""
		task.Buttons = ""
		task.State = ""
		task.Description = ""
		task.PromptEn = ""
		task.VideoUrl = ""
		task.VideoUrls = ""
		if task.ImageUrl != "" {
			task.ImageUrl = service.BuildMidjourneyImageURL(task)
		}
		task.FailReason = failReason
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
