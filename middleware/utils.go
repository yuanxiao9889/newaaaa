package middleware

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func abortWithOpenAiMessage(c *gin.Context, statusCode int, message string, code ...types.ErrorCode) {
	codeStr := ""
	if len(code) > 0 {
		codeStr = string(code[0])
	}
	userId := c.GetInt("id")
	_, preparedPluginRoute := c.Get(pluginruntime.ContextKeyRouteRequest)
	publicError := sanitizedTaskPluginError(statusCode, message, codeStr)
	c.Header("X-Error-Code", publicError.Code)
	nativeError := &dto.TaskError{Code: publicError.Code, Message: publicError.Message, StatusCode: publicError.HTTPStatus, LocalError: true}
	if !preparedPluginRoute || !RespondTaskPluginError(c, nativeError) {
		c.JSON(publicError.HTTPStatus, gin.H{
			"error": gin.H{
				"message": common.MessageWithRequestId(publicError.Message, c.GetString(common.RequestIdKey)),
				"type":    "new_api_error",
				"code":    publicError.Code,
			},
		})
	}
	c.Abort()
	logger.LogError(c.Request.Context(), fmt.Sprintf("user %d | %s", userId, message))
}

func abortWithMidjourneyMessage(c *gin.Context, statusCode int, code int, description string) {
	c.JSON(statusCode, gin.H{
		"description": description,
		"type":        "new_api_error",
		"code":        code,
	})
	c.Abort()
	logger.LogError(c.Request.Context(), description)
}
