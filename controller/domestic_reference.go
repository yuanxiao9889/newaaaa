package controller

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Machine-only, read-only lease reconciliation. Missing/unknown state never releases material.
func DomesticReferenceTaskStates(c *gin.Context) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8192))
	if err != nil || !service.VerifyDomesticTaskRequest(c.Request, body) {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	var request struct {
		ExecutionIDs []string `json:"execution_ids"`
	}
	if common.Unmarshal(body, &request) != nil || len(request.ExecutionIDs) == 0 || len(request.ExecutionIDs) > 64 {
		c.AbortWithStatus(400)
		return
	}
	states := make(map[string]int64, len(request.ExecutionIDs))
	for _, id := range request.ExecutionIDs {
		if legacy, ok := strings.CutPrefix(id, "legacy:"); ok {
			owner, taskID, valid := strings.Cut(legacy, ":")
			userID, parseErr := strconv.Atoi(owner)
			if !valid || parseErr != nil || userID <= 0 || len(taskID) == 0 || len(taskID) > 128 {
				c.AbortWithStatus(400)
				return
			}
			var tasks []model.Task
			if model.DB.Select("status", "finish_time").Where("user_id = ? AND task_id = ?", userID, taskID).Find(&tasks).Error != nil {
				c.AbortWithStatus(503)
				return
			}
			states[id] = 0
			if len(tasks) == 1 && (tasks[0].Status == model.TaskStatusSuccess || tasks[0].Status == model.TaskStatusFailure) && tasks[0].FinishTime > 0 {
				states[id] = tasks[0].FinishTime
			}
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			c.AbortWithStatus(400)
			return
		}
		var records []model.TransferRequest
		if model.DB.Where("execution_id = ?", id).Limit(1).Find(&records).Error != nil {
			c.AbortWithStatus(503)
			return
		}
		states[id] = 0
		if len(records) == 0 {
			continue
		}
		record := records[0]
		var tasks []model.Task
		// PostgreSQL JSON requires an explicit text cast; MySQL and SQLite accept LIKE directly.
		predicate := "private_data LIKE ?"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			predicate = "CAST(private_data AS TEXT) LIKE ?"
		}
		if model.DB.Select("status", "finish_time", "private_data").Where("user_id = ?", record.UserID).Where(predicate, "%"+id+"%").Find(&tasks).Error != nil {
			c.AbortWithStatus(503)
			return
		}
		found, active, finish := false, false, int64(0)
		for _, task := range tasks {
			if task.PrivateData.TransferExecutionID != id {
				continue
			}
			found = true
			if task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure || task.FinishTime <= 0 {
				active = true
			}
			finish = max(finish, task.FinishTime)
		}
		if found && !active {
			states[id] = finish
		}
		if !found && record.State == "failed" && record.FinishedAt > 0 {
			states[id] = record.FinishedAt
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"protocol_version": 1, "finished_at": states})
}
