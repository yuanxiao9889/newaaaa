package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/recharge_record_setting"
	"github.com/gin-gonic/gin"
)

// This guard supplements both Casbin and the session-only PAT route rule.
func requireRechargeImportSession(c *gin.Context) bool {
	if !recharge_record_setting.ImportEnabled() {
		writeRechargeRecordError(c, http.StatusForbidden, "RECHARGE_IMPORT_DISABLED", "historical recharge import is disabled")
		return false
	}
	if c.GetInt("id") <= 0 || c.GetString("session_id") == "" || c.GetBool("use_access_token") {
		writeRechargeRecordError(c, http.StatusForbidden, "AUTH_SESSION_REQUIRED", "a dashboard login session is required")
		return false
	}
	return true
}

func GetRechargeImportReview(c *gin.Context) {
	if !recharge_record_setting.Enabled() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	before, _ := strconv.Atoi(c.Query("before_id"))
	if before < 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	page, err := model.ListRechargeImportReview(ctx, before)
	if err != nil {
		writeRechargeRecordModelError(c, err)
		return
	}
	common.ApiSuccess(c, page)
}

func CreateRechargeImportJob(c *gin.Context) {
	if !requireRechargeImportSession(c) {
		return
	}
	var request struct {
		Start int64 `json:"start_timestamp"`
		End   int64 `json:"end_timestamp"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	if common.DecodeJson(c.Request.Body, &request) != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	job, err := model.CreateRechargeImportJob(ctx, request.Start, request.End, c.GetInt("id"))
	if err != nil {
		writeRechargeImportError(c, err)
		return
	}
	common.ApiSuccess(c, job)
}

func AdvanceRechargeImportJob(c *gin.Context) {
	if !requireRechargeImportSession(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	job, err := model.AdvanceRechargeImportJob(ctx, id)
	if err != nil {
		writeRechargeImportError(c, err)
		return
	}
	common.ApiSuccess(c, job)
}

func ReviewHistoricalRecharge(c *gin.Context) {
	if !requireRechargeImportSession(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	var request struct {
		UserID    int    `json:"user_id"`
		Quota     int    `json:"quota"`
		Note      string `json:"note"`
		Reject    bool   `json:"reject"`
		Confirmed bool   `json:"confirmed"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	if common.DecodeJson(c.Request.Body, &request) != nil || !request.Confirmed {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	err = model.ReviewHistoricalRecharge(ctx, id, request.UserID, request.Quota, c.GetInt("id"), request.Note, request.Reject)
	if err != nil {
		writeRechargeImportError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func writeRechargeImportError(c *gin.Context, err error) {
	if errors.Is(err, model.ErrRechargeImportConflict) {
		writeRechargeRecordError(c, http.StatusConflict, "RECHARGE_IMPORT_CONFLICT", err.Error())
		return
	}
	if errors.Is(err, model.ErrRechargeImportUnsupported) || errors.Is(err, model.ErrInvoiceValidation) || errors.Is(err, model.ErrInvalidRechargeRecordFilter) {
		writeRechargeRecordError(c, http.StatusBadRequest, "INVALID_RECHARGE_IMPORT", err.Error())
		return
	}
	writeRechargeRecordModelError(c, err)
}
