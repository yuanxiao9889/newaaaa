package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func TestImageHelperConvertImageRequestErrorReturnsBadRequestAndSkipsRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)

	req := &dto.ImageRequest{
		Model:  "monkey-image-pro",
		Prompt: "draw a cat",
	}
	info := &relaycommon.RelayInfo{
		Request:         req,
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		RelayFormat:     types.RelayFormatOpenAIImage,
		OriginModelName: "monkey-image-pro",
		RequestURLPath:  "/v1/images/generations",
	}

	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "monkey-image-pro")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	c.Set("model_mapping", `{"monkey-image-pro":"gemini-3-pro-image-preview"}`)

	err := ImageHelper(c, info)
	if err == nil {
		t.Fatal("expected ImageHelper to fail")
	}
	if got, want := err.StatusCode, http.StatusBadRequest; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	if got, want := err.GetErrorCode(), types.ErrorCodeConvertRequestFailed; got != want {
		t.Fatalf("error code = %s, want %s", got, want)
	}
	if !types.IsSkipRetryError(err) {
		t.Fatal("expected convert request error to skip retry")
	}
}
