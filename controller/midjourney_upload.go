package controller

import (
	"errors"
	"net/http"
	"os"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func RelayMidjourneyUpload(c *gin.Context) {
	var request struct {
		Base64Array []string `json:"base64Array"`
	}
	if err := common.UnmarshalBodyReusable(c, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4, "description": "bind_request_body_failed"})
		return
	}
	result, err := service.StoreMidjourneyImages(request.Base64Array)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrInvalidMidjourneyUpload) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"code": 4, "description": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 1, "description": "success", "result": result.URLs})
}

func RelayMidjourneyUploadImage(c *gin.Context) {
	if err := service.ServeMidjourneyUpload(c.Writer, c.Request, c.Param("id")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusInternalServerError)
	}
}
