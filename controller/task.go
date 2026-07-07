package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func GetAllTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 解析其他查询参数
	queryParams := model.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		ModelName:      c.Query("model_name"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ChannelID:      c.Query("channel_id"),
	}
	if username := c.Query("username"); username != "" {
		userIDs, err := model.SearchUserIDsByUsername(username, 1000)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if len(userIDs) == 0 {
			userIDs = []int{-1}
		}
		queryParams.UserIDs = userIDs
	}

	items := model.TaskGetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := model.TaskCountAllTasks(queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(tasksToDto(items, true, false))
	common.ApiSuccess(c, pageInfo)
}

func GetTask(c *gin.Context) {
	taskId := c.Param("id")
	task, exists, err := model.GetByOnlyTaskId(taskId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exists {
		common.ApiError(c, errors.New("task not found"))
		return
	}
	common.ApiSuccess(c, relay.TaskModel2Dto(task))
}

func GetUserTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	userId := c.GetInt("id")

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	queryParams := model.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		ModelName:      c.Query("model_name"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
	}

	items := model.TaskGetAllUserTask(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := model.TaskCountAllUserTask(userId, queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(tasksToDto(items, false, false))
	common.ApiSuccess(c, pageInfo)
}

func GetUserTaskDetail(c *gin.Context) {
	taskId := c.Param("id")
	userId := c.GetInt("id")
	task, exists, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exists {
		common.ApiError(c, errors.New("task not found"))
		return
	}
	common.ApiSuccess(c, relay.UserTaskModel2Dto(task))
}

func tasksToDto(tasks []*model.Task, fillUser bool, includeData bool) []*dto.TaskDto {
	var userIdMap map[int]*model.UserBase
	var channelNameMap map[int]string
	if fillUser {
		userIdMap = make(map[int]*model.UserBase)
		userIds := types.NewSet[int]()
		channelIds := types.NewSet[int]()
		for _, task := range tasks {
			userIds.Add(task.UserId)
			if task.ChannelId > 0 {
				channelIds.Add(task.ChannelId)
			}
		}
		for _, userId := range userIds.Items() {
			cacheUser, err := model.GetUserCache(userId)
			if err == nil {
				userIdMap[userId] = cacheUser
			}
		}
		if len(channelIds.Items()) > 0 {
			channelNameMap = make(map[int]string)
			channels, err := model.GetChannelsByIds(channelIds.Items())
			if err == nil {
				for _, channel := range channels {
					channelNameMap[channel.Id] = channel.Name
				}
			}
		}
	}
	result := make([]*dto.TaskDto, len(tasks))
	for i, task := range tasks {
		if fillUser {
			if user, ok := userIdMap[task.UserId]; ok {
				task.Username = user.Username
			}
			if channelName, ok := channelNameMap[task.ChannelId]; ok {
				task.ChannelName = channelName
			}
		}
		if includeData {
			result[i] = relay.TaskModel2DtoWithOptions(task, true, fillUser)
		} else {
			result[i] = relay.TaskModel2DtoWithOptions(task, false, fillUser)
		}
	}
	return result
}
