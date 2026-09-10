package controller

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// RelayImageUpload exposes the OpenAI-compatible temporary image upload used
// by image models that require public reference URLs. Files are stored by the
// same bounded, expiring storage used by the Midjourney upload route.
func RelayImageUpload(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid multipart image upload", "type": "invalid_request_error"}})
		return
	}
	var headers []*multipart.FileHeader
	for _, field := range []string{"image", "file", "image[]"} {
		headers = append(headers, c.Request.MultipartForm.File[field]...)
	}
	if len(headers) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "image is required", "type": "invalid_request_error"}})
		return
	}
	encoded := make([]string, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "failed to open image", "type": "invalid_request_error"}})
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 8<<20+1))
		_ = file.Close()
		if readErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "failed to read image", "type": "invalid_request_error"}})
			return
		}
		if len(data) > 8<<20 {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"message": "image exceeds 8388608 bytes", "type": "invalid_request_error"}})
			return
		}
		mimeType := header.Header.Get("Content-Type")
		if !strings.HasPrefix(mimeType, "image/") {
			mimeType = "image/png"
		}
		encoded = append(encoded, fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data)))
	}
	stored, err := service.StoreMidjourneyImages(encoded)
	if err != nil || len(stored.URLs) == 0 {
		message := "failed to store image"
		if err != nil {
			message = err.Error()
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": message, "type": "invalid_request_error"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": stored.URLs[0], "urls": stored.URLs})
}
