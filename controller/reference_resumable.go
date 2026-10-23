package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func PrepareResumableReference(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var input service.ReferencePrepareRequest
	if common.DecodeJson(c.Request.Body, &input) != nil {
		referenceUploadError(c, service.ErrReferenceInvalid)
		return
	}
	result, err := service.ReferenceUploads.PrepareResumable(c.GetInt("id"), input)
	writeResumableReference(c, result, err)
}

func GetResumableReference(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	result, err := service.ReferenceUploads.ResumableStatus(c.GetInt("id"), c.Param("upload"))
	writeResumableReference(c, result, err)
}

func PutResumableReferenceChunk(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		referenceUploadError(c, service.ErrReferenceInvalid)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.ReferenceChunkBytes)
	deadline := http.NewResponseController(c.Writer)
	_ = deadline.SetReadDeadline(time.Now().Add(45 * time.Second))
	defer deadline.SetReadDeadline(time.Time{})
	result, err := service.ReferenceUploads.WriteResumableChunk(c.GetInt("id"), c.Param("upload"),
		index, c.GetHeader("X-Chunk-SHA256"), c.Request.Body)
	writeResumableReference(c, result, err)
}

func CompleteResumableReference(c *gin.Context) {
	if service.ReferenceUploads == nil {
		referenceUploadError(c, service.ErrReferenceUnavailable)
		return
	}
	result, err := service.ReferenceUploads.CompleteResumable(c.GetInt("id"), c.Param("upload"))
	writeResumableReference(c, result, err)
}

func writeResumableReference(c *gin.Context, result service.ReferenceResumableResult, err error) {
	if err != nil {
		referenceUploadError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
