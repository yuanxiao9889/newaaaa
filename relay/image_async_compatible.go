package relay

import (
	"fmt"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

// Explicit image intent keeps ordinary chat/Responses requests unchanged.
func IsAsyncCompatibleImageRequest(c *gin.Context, format types.RelayFormat) bool {
	if !IsAsyncImageRequest(c) || c.Query("image_task") != "true" || c.Request.Method != http.MethodPost {
		return false
	}
	return (format == types.RelayFormatOpenAI && c.Request.URL.Path == "/v1/chat/completions") ||
		(format == types.RelayFormatOpenAIResponses && c.Request.URL.Path == "/v1/responses")
}

func SubmitInternalAsyncCompatibleImageTask(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request, format types.RelayFormat) *types.NewAPIError {
	service.ReleaseAutoSwitchAttempt(c)
	if !IsAsyncCompatibleImageRequest(c, format) || request == nil || request.IsStream(c.Request) {
		return types.NewErrorWithStatusCode(fmt.Errorf("async image tasks require an explicit non-streaming image request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	switch request := request.(type) {
	case *dto.GeneralOpenAIRequest:
		if format != types.RelayFormatOpenAI || (request.N != nil && *request.N != 1) {
			return types.NewErrorWithStatusCode(fmt.Errorf("async image tasks only support n=1"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	case *dto.OpenAIResponsesRequest:
		if format != types.RelayFormatOpenAIResponses {
			return types.NewErrorWithStatusCode(fmt.Errorf("image protocol mismatch"), types.ErrorCodeInvalidRequest, http.StatusBadRequest)
		}
	default:
		return types.NewErrorWithStatusCode(fmt.Errorf("unsupported image protocol"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return submitInternalAsyncProtocolImageTask(c, info, string(format), constant.TaskActionImageGenerate)
}
