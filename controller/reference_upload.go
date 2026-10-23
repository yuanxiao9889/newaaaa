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
		"relay_direct_upload":                service.ReferenceUploads.DirectEnabled(),
		"direct_protocol_version":            2,
		"direct_prepare_transport":           "relay",
		"direct_prepare_request_id_required": true,
		"direct_reference_urls":              service.ReferenceUploads.LegacyEnabled(),
		"max_file_bytes":                     service.ReferenceMaxBytes, "max_references": 16,
		"qiniu_fallback_upload":        false, // Older edge-upload protocol is not advertised.
		"qiniu_direct_fallback_upload": service.QiniuDirectUploadEnabled(),
		"resumable_reference_uploads":  service.ReferenceUploads != nil,
		"resumable_chunk_bytes":        service.ReferenceChunkBytes,
		"retention_seconds":            int(service.ReferenceRetention().Seconds()),
		"request_header":               "X-New-Api-Reference-Assets", "request_header_value": "1"})
}

func GetDirectReferenceSession(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	result, err := service.ReferenceUploads.DirectSession(c.GetInt("id"), c.Param("session"))
	if err != nil {
		referenceUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, result)
}

func PutDirectReferenceSession(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	control := http.NewResponseController(c.Writer)
	if err := control.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	defer control.SetReadDeadline(time.Time{})
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.ReferenceMaxBytes+1)
	result, err := service.ReferenceUploads.UploadDirect(c.GetInt("id"), c.Param("session"),
		c.GetHeader("X-Reference-Upload-Ticket"), c.Request.Body)
	if err != nil {
		referenceUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, result)
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
	c.Header("ETag", `"`+c.Param("asset")+`"`)
	http.ServeContent(c.Writer, c.Request, "reference", time.Time{}, bytes.NewReader(data))
}

func referenceUploadError(c *gin.Context, err error) {
	status, code := http.StatusServiceUnavailable, "reference_unavailable"
	switch {
	case errors.Is(err, service.ErrReferenceInvalid):
		status, code = http.StatusBadRequest, "reference_invalid"
	case errors.Is(err, service.ErrReferenceConflict):
		status, code = http.StatusConflict, "reference_upload_conflict"
	case errors.Is(err, service.ErrReferenceMissing):
		status, code = http.StatusGone, "reference_expired"
	case errors.Is(err, service.ErrReferenceUnavailable):
		status, code = http.StatusServiceUnavailable, "reference_unavailable"
	case errors.Is(err, service.ErrReferenceBusy):
		status, code = http.StatusTooManyRequests, "reference_busy"
		c.Header("Retry-After", "2")
	case errors.Is(err, service.ErrReferenceDownload):
		status, code = http.StatusBadGateway, "reference_download_failed"
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": code, "type": "invalid_request_error", "scope": "origin"}})
}
