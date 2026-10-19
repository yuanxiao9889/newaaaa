package controller

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ReferenceUploadCapabilities(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"enabled": service.ReferenceUploads != nil, "version": 1,
		"max_file_bytes": service.ReferenceMaxBytes, "max_references": 16,
		"retention_seconds": int(service.ReferenceRetention.Seconds()),
		"request_header":    "X-New-Api-Reference-Assets", "request_header_value": "1"})
}

func PrepareReferenceUpload(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 8193))
	var input service.ReferencePrepareRequest
	if err != nil || len(data) > 8192 || common.Unmarshal(data, &input) != nil {
		referenceUploadError(c, service.ErrReferenceInvalid)
		return
	}
	result, err := service.ReferenceUploads.Prepare(c.GetInt("id"), input)
	if err != nil {
		referenceUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func CompleteReferenceUpload(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
	var input struct {
		Ticket string `json:"ticket"`
	}
	if err != nil || len(data) > 4096 || common.Unmarshal(data, &input) != nil {
		referenceUploadError(c, service.ErrReferenceInvalid)
		return
	}
	result, err := service.ReferenceUploads.Complete(c.Request.Context(), c.GetInt("id"), input.Ticket)
	if err != nil {
		referenceUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func GetReferenceContent(c *gin.Context) {
	store := service.ReferenceUploads
	user, err := strconv.Atoi(c.Param("user"))
	if store == nil || err != nil || !store.VerifyContent(user, c.Param("asset"), c.Query("e"), c.Query("token")) {
		c.Status(http.StatusNotFound)
		return
	}
	data, mime, err := store.Read(user, c.Param("asset"), false)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline; filename=\"reference\"")
	http.ServeContent(c.Writer, c.Request, "reference", time.Time{}, bytes.NewReader(data))
}

func referenceUploadError(c *gin.Context, err error) {
	status, code := http.StatusBadRequest, "reference_invalid"
	switch {
	case errors.Is(err, service.ErrReferenceMissing):
		status, code = http.StatusGone, "reference_expired"
	case errors.Is(err, service.ErrReferenceUnavailable):
		status, code = http.StatusServiceUnavailable, "reference_unavailable"
	case errors.Is(err, service.ErrReferenceBusy):
		status, code = http.StatusTooManyRequests, "reference_busy"
	case errors.Is(err, service.ErrReferenceDownload):
		status, code = http.StatusBadGateway, "reference_download_failed"
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": err.Error(), "type": "invalid_request_error"}})
}
