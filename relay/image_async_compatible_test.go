package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestCompatibleImageAdmissionIsExplicitBoundedAndNonStreaming(t *testing.T) {
	for _, tc := range []struct {
		path     string
		format   types.RelayFormat
		eligible bool
	}{
		{"/v1/chat/completions?async=true&image_task=true", types.RelayFormatOpenAI, true},
		{"/v1/responses?async=true&image_task=true", types.RelayFormatOpenAIResponses, true},
		{"/v1/chat/completions?async=true", types.RelayFormatOpenAI, false},
		{"/v1/responses?image_task=true", types.RelayFormatOpenAIResponses, false},
		{"/v1/responses/compact?async=true&image_task=true", types.RelayFormatOpenAIResponses, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", tc.path, nil)
		require.Equal(t, tc.eligible, IsAsyncCompatibleImageRequest(c, tc.format))
		c.Set("async_image_worker", true)
		require.False(t, IsAsyncCompatibleImageRequest(c, tc.format), "worker replay never re-enqueues")
	}
	for _, request := range []dto.Request{
		&dto.GeneralOpenAIRequest{N: common.GetPointer(0)},
		&dto.GeneralOpenAIRequest{N: common.GetPointer(-1)},
		&dto.GeneralOpenAIRequest{N: common.GetPointer(2)},
		&dto.GeneralOpenAIRequest{Stream: common.GetPointer(true)},
		&dto.OpenAIResponsesRequest{Stream: common.GetPointer(true)},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions?async=true&image_task=true", nil)
		err := SubmitInternalAsyncCompatibleImageTask(c, nil, request, types.RelayFormatOpenAI)
		require.NotNil(t, err, "invalid input is rejected before DB/billing access")
		require.Equal(t, 400, err.StatusCode)
	}
}
