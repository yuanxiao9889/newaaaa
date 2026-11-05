package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/recharge_record_setting"
	"github.com/gin-gonic/gin"
)

func GetRechargeRecordConfig(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"feature_enabled": recharge_record_setting.Enabled(),
		"writes_enabled":  recharge_record_setting.WritesEnabled(),
		"imports_enabled": recharge_record_setting.ImportEnabled(),
		"default_days":    model.RechargeRecordDefaultDays,
		"max_days":        model.RechargeRecordMaxDays,
		"max_page_size":   model.RechargeRecordMaxPageSize,
	})
}

func GetRechargeRecords(c *gin.Context) {
	if !recharge_record_setting.Enabled() {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORDS_DISABLED", "recharge records are disabled")
		return
	}
	filter, err := parseRechargeRecordFilter(c, 0)
	if err != nil {
		writeRechargeRecordError(c, http.StatusBadRequest, "INVALID_RECHARGE_RECORD_FILTER", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	records, total, err := model.ListRechargeRecords(ctx, filter)
	if err != nil {
		writeRechargeRecordModelError(c, err)
		return
	}
	writeRechargeRecordPage(c, filter.Page, filter.PageSize, records, total)
}

func GetSelfRechargeRecords(c *gin.Context) {
	if !recharge_record_setting.Enabled() {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORDS_DISABLED", "recharge records are disabled")
		return
	}
	userID := c.GetInt("id")
	if userID <= 0 {
		writeRechargeRecordError(c, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "not logged in")
		return
	}
	filter, err := parseRechargeRecordFilter(c, userID)
	if err != nil {
		writeRechargeRecordError(c, http.StatusBadRequest, "INVALID_RECHARGE_RECORD_FILTER", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	records, total, err := model.ListRechargeRecords(ctx, filter)
	if err != nil {
		writeRechargeRecordModelError(c, err)
		return
	}
	for i := range records {
		records[i].InternalNote = ""
		records[i].Verification = ""
		records[i].MarkedByName = ""
	}
	writeRechargeRecordPage(c, filter.Page, filter.PageSize, records, total)
}

func GetRechargeRecordDetail(c *gin.Context) {
	if !recharge_record_setting.Enabled() {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORDS_DISABLED", "recharge records are disabled")
		return
	}
	source := strings.TrimSpace(c.Param("source"))
	if source != model.RechargeSourceTopUp && source != model.RechargeSourceRedemption && source != model.RechargeSourceHistorical {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORD_NOT_FOUND", "recharge record not found")
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORD_NOT_FOUND", "recharge record not found")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	record, err := model.GetRechargeRecord(ctx, source, id)
	if err != nil {
		writeRechargeRecordModelError(c, err)
		return
	}
	common.ApiSuccess(c, record)
}

func CreateRechargeInvoiceMarking(c *gin.Context) {
	if !recharge_record_setting.WritesEnabled() {
		writeRechargeRecordError(c, http.StatusForbidden, "RECHARGE_RECORD_WRITES_DISABLED", "recharge record invoice writes are disabled")
		return
	}
	// Defense in depth: the route is declared session-only for access tokens,
	// and the handler refuses every non-browser credential even if a future
	// route change forgets that declaration.
	if c.GetString("session_id") == "" || c.GetBool("use_access_token") {
		writeRechargeRecordError(c, http.StatusForbidden, "AUTH_SESSION_REQUIRED", "a dashboard login session is required")
		return
	}

	var request model.InvoiceMarkingRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		writeRechargeRecordError(c, http.StatusBadRequest, "INVALID_INVOICE_MARKING", "invalid invoice marking request")
		return
	}
	headerKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	bodyKey := strings.TrimSpace(request.RequestID)
	if headerKey == "" {
		headerKey = bodyKey
	}
	if headerKey == "" {
		writeRechargeRecordError(c, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required")
		return
	}
	if bodyKey != "" && bodyKey != headerKey {
		writeRechargeRecordError(c, http.StatusBadRequest, "IDEMPOTENCY_KEY_MISMATCH", "Idempotency-Key does not match request_id")
		return
	}
	request.RequestID = headerKey
	request.OperatorID = c.GetInt("id")
	request.OperatorName = c.GetString("username")
	if request.OperatorID <= 0 || request.OperatorName == "" {
		writeRechargeRecordError(c, http.StatusForbidden, "AUTH_INSUFFICIENT_PRIVILEGE", "operator identity is required")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	result, err := model.MarkRechargeInvoices(ctx, request)
	if err != nil {
		writeRechargeInvoiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, result)
}

func GetRechargeRecordInvoiceHistory(c *gin.Context) {
	if !recharge_record_setting.Enabled() {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORDS_DISABLED", "recharge records are disabled")
		return
	}
	source := strings.TrimSpace(c.Param("source"))
	if source != model.RechargeSourceTopUp && source != model.RechargeSourceRedemption && source != model.RechargeSourceHistorical {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORD_NOT_FOUND", "recharge record not found")
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORD_NOT_FOUND", "recharge record not found")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), recharge_record_setting.QueryTimeout())
	defer cancel()
	before := 0
	if raw := c.Query("before_id"); raw != "" {
		before, err = strconv.Atoi(raw)
		if err != nil || before <= 0 {
			writeRechargeRecordError(c, http.StatusBadRequest, "INVALID_RECHARGE_RECORD_FILTER", "invalid history cursor")
			return
		}
	}
	// The admin permission is checked by the route. Audit access must not depend
	// on the continued existence of a mutable/deleted source row.
	history, err := model.ListRechargeInvoiceHistory(ctx, source, id, before)
	if err != nil {
		writeRechargeRecordModelError(c, err)
		return
	}
	common.ApiSuccess(c, history)
}

func parseRechargeRecordFilter(c *gin.Context, forcedUserID int) (model.RechargeRecordFilter, error) {
	filter := model.RechargeRecordFilter{UserID: forcedUserID, Page: 1, PageSize: 20}
	if forcedUserID <= 0 {
		if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				return filter, errors.New("invalid user_id")
			}
			filter.UserID = parsed
		}
	}
	if raw := strings.TrimSpace(c.Query("p")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return filter, errors.New("invalid page")
		}
		filter.Page = parsed
	}
	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > model.RechargeRecordMaxPageSize {
			return filter, errors.New("invalid page_size")
		}
		filter.PageSize = parsed
	}
	filter.Keyword = strings.TrimSpace(c.Query("keyword"))
	if utf8.RuneCountInString(filter.Keyword) > 128 {
		return filter, errors.New("keyword is too long")
	}
	source := strings.TrimSpace(c.Query("source"))
	switch source {
	case "", "topup":
	case model.RechargeKindOnline, model.RechargeKindAdmin, model.RechargeKindRedemption:
		filter.SourceKind = source
	default:
		return filter, errors.New("invalid source")
	}
	filter.Status = strings.TrimSpace(c.Query("status"))
	switch filter.Status {
	case "", common.TopUpStatusSuccess, common.TopUpStatusPending, common.TopUpStatusFailed, common.TopUpStatusExpired, model.RechargeStatusUsed:
	default:
		return filter, errors.New("invalid status")
	}
	filter.InvoiceStatus = strings.TrimSpace(c.Query("invoice_status"))
	switch filter.InvoiceStatus {
	case "", model.InvoiceStatusUnmarked, model.InvoiceStatusMarked, model.InvoiceStatusRevoked:
	default:
		return filter, errors.New("invalid invoice_status")
	}
	start, err := parseRechargeRecordTimestamp(c.Query("start_timestamp"))
	if err != nil {
		return filter, errors.New("invalid start_timestamp")
	}
	end, err := parseRechargeRecordTimestamp(c.Query("end_timestamp"))
	if err != nil {
		return filter, errors.New("invalid end_timestamp")
	}
	start, end, err = model.ValidateRechargeRecordWindow(start, end)
	if err != nil {
		return filter, errors.New("time range must be within 366 days")
	}
	filter.StartTime = start
	filter.EndTime = end
	return filter, nil
}

func parseRechargeRecordTimestamp(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("invalid timestamp")
	}
	return parsed, nil
}

func writeRechargeRecordPage(c *gin.Context, page, pageSize int, records []*model.RechargeRecord, total int64) {
	c.Header("Cache-Control", "no-store")
	pageInfo := &common.PageInfo{Page: page, PageSize: pageSize, Total: int(total)}
	pageInfo.SetItems(records)
	common.ApiSuccess(c, pageInfo)
}

func writeRechargeRecordModelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrRechargeRecordNotFound):
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORD_NOT_FOUND", "recharge record not found")
	case errors.Is(err, context.DeadlineExceeded):
		writeRechargeRecordError(c, http.StatusGatewayTimeout, "RECHARGE_RECORD_QUERY_TIMEOUT", "recharge record query timed out")
	case errors.Is(err, model.ErrInvalidRechargeRecordFilter):
		writeRechargeRecordError(c, http.StatusBadRequest, "INVALID_RECHARGE_RECORD_FILTER", "invalid recharge record filter")
	default:
		common.SysError("recharge record query failed: " + err.Error())
		writeRechargeRecordError(c, http.StatusInternalServerError, "RECHARGE_RECORD_QUERY_FAILED", "failed to query recharge records")
	}
}

func writeRechargeInvoiceError(c *gin.Context, err error) {
	var conflict *model.InvoiceMarkingConflictError
	switch {
	case errors.As(err, &conflict):
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"code":    "INVOICE_MARKING_CONFLICT",
			"message": "invoice marking conflict; no record was changed",
			"data":    gin.H{"conflicts": conflict.Conflicts},
		})
	case errors.Is(err, model.ErrInvoiceIdempotency):
		writeRechargeRecordError(c, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", "idempotency key was used for a different request")
	case errors.Is(err, model.ErrInvoiceValidation):
		writeRechargeRecordError(c, http.StatusBadRequest, "INVALID_INVOICE_MARKING", "invalid invoice marking request")
	case errors.Is(err, model.ErrInvoiceRecordMismatch):
		writeRechargeRecordError(c, http.StatusBadRequest, "INVOICE_RECORD_MISMATCH", "all records in a batch must belong to one user")
	case errors.Is(err, model.ErrInvoiceHistoryNotFound):
		writeRechargeRecordError(c, http.StatusConflict, "INVOICE_REQUEST_INCOMPLETE", "the original request has no completed history")
	case errors.Is(err, model.ErrRechargeRecordNotFound):
		writeRechargeRecordError(c, http.StatusNotFound, "RECHARGE_RECORD_NOT_FOUND", "recharge record not found")
	case errors.Is(err, context.DeadlineExceeded):
		writeRechargeRecordError(c, http.StatusGatewayTimeout, "INVOICE_MARKING_TIMEOUT", "invoice marking timed out")
	default:
		common.SysError("invoice marking failed: " + err.Error())
		writeRechargeRecordError(c, http.StatusInternalServerError, "INVOICE_MARKING_FAILED", "failed to update invoice marking")
	}
}

func writeRechargeRecordError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}
