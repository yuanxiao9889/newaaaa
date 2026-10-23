package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func AcceptTransferObservation(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var event service.TransferObservation
	if common.DecodeJson(c.Request.Body, &event) != nil || service.ValidateTransferObservation(event) != nil {
		c.JSON(400, gin.H{"error": gin.H{"code": "transfer_observation_invalid"}})
		return
	}
	owned, err := ownsTransferObservation(c.GetInt("id"), event)
	if err != nil {
		c.JSON(503, gin.H{"error": gin.H{"code": "transfer_observation_unavailable"}})
		return
	}
	if !owned {
		c.JSON(404, gin.H{"error": gin.H{"code": "transfer_resource_not_available"}})
		return
	}
	duplicate, err := service.TransferObservations.Accept(c.GetInt("id"), event)
	if err != nil {
		status, code := 400, "transfer_observation_invalid"
		if errors.Is(err, service.ErrTransferObservationConflict) {
			status, code = 409, "transfer_observation_conflict"
		} else if errors.Is(err, service.ErrTransferObservationBusy) {
			status, code = 429, "transfer_observation_busy"
		}
		c.JSON(status, gin.H{"error": gin.H{"code": code}})
		return
	}
	c.JSON(200, gin.H{"accepted": true, "duplicate": duplicate})
}

func ownsTransferObservation(user int, event service.TransferObservation) (bool, error) {
	if event.TaskID != "" {
		task, exists, err := model.GetByTaskId(user, event.TaskID)
		if err != nil {
			return false, err
		}
		if exists && task != nil && task.Status == model.TaskStatusSuccess && task.ResultRetrievable() {
			return true, nil
		}
		var count int64
		if err := model.DB.Model(&model.Midjourney{}).
			Where("user_id = ? AND mj_id = ? AND status = ?", user, event.TaskID, "SUCCESS").
			Count(&count).Error; err != nil {
			return false, err
		}
		if count == 1 {
			return true, nil
		}
	}
	if model.ValidTransferRequestKey(event.RequestID) {
		receipt, err := model.FindTransferRequest(user, event.RequestID)
		if err != nil {
			return false, err
		}
		return receipt != nil && receipt.State == "succeeded" &&
			receipt.FinishedAt >= time.Now().Add(-model.TransferResponseRetention).Unix(), nil
	}
	return false, nil
}
