package controller

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"regexp"
	"strings"
)

func isCompatibleImageFormat(format string) bool {
	return format == string(types.RelayFormatOpenAI) || format == string(types.RelayFormatOpenAIResponses)
}

func buildInternalAsyncCompatibleImageContext(ctx context.Context, task *model.Task, body []byte) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, error) {
	c, recorder, err := buildInternalAsyncBaseContext(ctx, task, body)
	if err != nil {
		return nil, nil, nil, err
	}
	format := types.RelayFormat(task.PrivateData.RequestRelayFormat)
	if !isCompatibleImageFormat(string(format)) {
		return nil, nil, nil, fmt.Errorf("unsupported stored image protocol")
	}
	c.Set("relay_mode", relayconstant.Path2RelayMode(c.Request.URL.Path))
	request, err := helper.GetAndValidateRequest(c, format)
	if err != nil {
		return nil, nil, nil, err
	}
	info, err := relaycommon.GenRelayInfo(c, format, request, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	prepareInternalAsyncRelayInfo(task, info)
	info.Request = request
	return c, recorder, info, nil
}

func runCompatibleImageHelper(c *gin.Context, info *relaycommon.RelayInfo, format string) *types.NewAPIError {
	if format == string(types.RelayFormatOpenAIResponses) {
		return relay.ResponsesHelper(c, info)
	}
	return relay.TextHelper(c, info)
}

func runInternalAsyncCompatibleImageRelayOnce(ctx context.Context, task *model.Task, body []byte, retry *service.RetryParam) (*gin.Context, *httptest.ResponseRecorder, *model.Channel, *types.NewAPIError, error) {
	c, recorder, info, err := buildInternalAsyncCompatibleImageContext(ctx, task, body)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	retry.Ctx = c
	channel, apiErr := getChannel(c, info, retry)
	if apiErr != nil {
		return c, recorder, channel, apiErr, nil
	}
	addUsedChannel(c, channel.Id)
	c.Request = c.Request.WithContext(service.BeginUpstreamDelivery(c.Request.Context(), channel.Id, retry.GetRetry()+1))
	return c, recorder, channel, runCompatibleImageHelper(c, info, task.PrivateData.RequestRelayFormat), nil
}

var markdownImage = regexp.MustCompile(`!\[[^\]]*\]\(([^\s)]+)(?:\s+[^)]*)?\)`)

// Only image-bearing result fields are inspected; input, links and tool arguments
// are never treated as generated images. Existing storage validates media bytes.
func compatibleImageResponse(body []byte) (dto.ImageResponse, error) {
	var root map[string]any
	if err := common.Unmarshal(body, &root); err != nil {
		return dto.ImageResponse{}, err
	}
	result := dto.ImageResponse{Created: common.GetTimestamp(), Data: []dto.ImageData{}}
	seen := map[string]bool{}
	add := func(source string, base64 bool) {
		source = strings.TrimSpace(source)
		if source == "" || seen[source] || len(result.Data) >= 16 {
			return
		}
		seen[source] = true
		if base64 || strings.HasPrefix(source, "data:image/") {
			result.Data = append(result.Data, dto.ImageData{B64Json: source})
		} else if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
			result.Data = append(result.Data, dto.ImageData{Url: source})
		}
	}
	var content func(any)
	content = func(value any) {
		switch value := value.(type) {
		case string:
			if strings.HasPrefix(strings.TrimSpace(value), "data:image/") {
				add(value, false)
			}
			for _, match := range markdownImage.FindAllStringSubmatch(value, 16) {
				add(match[1], false)
			}
		case []any:
			for _, item := range value {
				content(item)
			}
		case map[string]any:
			kind, _ := value["type"].(string)
			if kind == "image_generation_call" {
				source, _ := value["result"].(string)
				add(source, true)
			}
			if kind == "image_url" || kind == "output_image" || kind == "image" {
				if source, ok := value["image_url"].(string); ok {
					add(source, false)
				}
				if source, ok := value["url"].(string); ok {
					add(source, false)
				}
				if image, ok := value["image_url"].(map[string]any); ok {
					source, _ := image["url"].(string)
					add(source, false)
				}
			}
			if kind == "text" || kind == "output_text" {
				content(value["text"])
			}
			content(value["content"])
		}
	}
	if choices, ok := root["choices"].([]any); ok {
		for _, raw := range choices {
			if choice, ok := raw.(map[string]any); ok {
				if message, ok := choice["message"].(map[string]any); ok {
					content(message["content"])
					content(message["images"])
				}
			}
		}
	}
	content(root["output"])
	if len(result.Data) == 0 {
		return result, fmt.Errorf("image task response contains no supported image result")
	}
	return result, nil
}
