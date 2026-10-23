package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// restoreImageTaskInput runs after authentication and the idempotency guard,
// before parsing, rate limits by model, channel selection and quota reservation.
// The opt-in request body is {}. To edit the original parameters or references,
// clients can retrieve retry-input and submit the edited body normally.
func restoreImageTaskInput(c *gin.Context) bool {
	taskID := c.GetHeader(service.ImageRetryTaskHeader)
	if taskID == "" {
		return true
	}
	if c.Request.Method != http.MethodPost || c.GetInt("id") <= 0 || c.GetHeader(ReferenceAssetsHeader) != "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_image_retry", "message": "invalid image retry request"}})
		return false
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1025))
	if err != nil || strings.TrimSpace(string(body)) != "{}" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_image_retry", "message": "send an empty JSON object to reuse the original image input"}})
		return false
	}
	input, body, err := service.ReadImageTaskInput(c.GetInt("id"), taskID, true)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusGone, gin.H{"error": gin.H{"code": "image_retry_input_unavailable", "message": "image retry input is unavailable or expired"}})
		return false
	}
	if c.Request.URL.Path != input.Path {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_image_retry", "message": "retry must use the original image endpoint"}})
		return false
	}
	query, err := url.ParseQuery(input.Query)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusGone, gin.H{"error": gin.H{"code": "image_retry_input_unavailable", "message": "stored image request query is invalid"}})
		return false
	}
	query.Set("async", "true")
	if input.Path == "/v1/chat/completions" || input.Path == "/v1/responses" {
		query.Set("image_task", "true")
	}
	c.Request.URL.RawQuery = query.Encode()
	common.CleanupBodyStorage(c)
	c.Set(common.KeyRequestBody, nil)
	_ = c.Request.Body.Close()
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Request.TransferEncoding = nil
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	c.Request.Header.Set("Content-Type", input.ContentType)
	c.Request.Header.Del(service.ImageRetryTaskHeader)
	c.Request.MultipartForm, c.Request.PostForm, c.Request.Form = nil, nil, nil
	return true
}
