package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

func ImageHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	c.Set("async_image_original_content_type", c.Request.Header.Get("Content-Type"))
	info.InitChannelMeta(c)
	info.BillingImageCount = nil
	info.ImageRequestCount = 0

	imageReq, ok := info.Request.(*dto.ImageRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.ImageRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(imageReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to ImageRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	imageAdapter := info.ChannelSetting.ImageAdapter
	adaptImage := imageAdapter.Matches(request.Model)
	if adaptImage {
		// ImageRequest.MarshalJSON omits Extra; preserve it inside the opt-in
		// adapter after the normal deep-copy and authoritative model mapping.
		request.Extra = imageReq.Extra
		originalContentType := c.Request.Header.Get("Content-Type")
		defer func() {
			if originalContentType == "" {
				c.Request.Header.Del("Content-Type")
			} else {
				c.Request.Header.Set("Content-Type", originalContentType)
			}
		}()
		if imageAdapter.Task != nil {
			if validationErr := validateAsyncImageTaskRequest(info, request); validationErr != nil {
				return validationErr
			}
		}
	}
	if adaptImage && (model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled) {
		return types.NewErrorWithStatusCode(fmt.Errorf("image adapter cannot be combined with request body passthrough"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	imageCount, err := request.ImageCount(false)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	var requestBody io.Reader
	var jsonData []byte

	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
			requestBody = common.NewReplayableBodyReader(storage)
		} else {
			jsonData, err = storage.Bytes()
			if err != nil {
				return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
		}
		if jsonData != nil && info.IsModelMapped {
			jsonData, err = sjson.SetBytes(jsonData, "model", request.Model)
			if err != nil {
				return types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
		}
	} else {
		convertedRequest, err := adaptor.ConvertImageRequest(c, info, *request)
		if err != nil {
			// An adaptor that already classified its rejection (status code
			// and retry policy) keeps that classification instead of being
			// downgraded to a retryable conversion failure.
			var apiErr *types.NewAPIError
			if errors.As(err, &apiErr) {
				return apiErr
			}
			return types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

		switch convertedRequest.(type) {
		case *bytes.Buffer:
			requestBody = convertedRequest.(io.Reader)
		default:
			jsonData, err = common.Marshal(convertedRequest)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}

			// apply param override
			if len(info.ParamOverride) > 0 {
				jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
				if err != nil {
					return newAPIErrorFromParamOverride(err)
				}
			}
			if adaptImage {
				if err = openai.ValidateConfiguredImageCount(jsonData, request); err != nil {
					return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
				}
			}

		}
	}
	if jsonData != nil {
		// This is a different trust boundary from ingress: channel overrides
		// and pass-through bodies can change the quantity actually submitted.
		var outbound struct {
			N *uint `json:"n"`
		}
		if err := common.Unmarshal(jsonData, &outbound); err != nil {
			return types.NewErrorWithStatusCode(fmt.Errorf("invalid image billing parameters: %w", err), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		quantityRequest := dto.ImageRequest{N: outbound.N}
		if quantityRequest.N == nil {
			quantityRequest.N = common.GetPointer(uint(imageCount))
		}
		imageCount, err = quantityRequest.ImageCount(false)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		logger.LogDebug(c, "image request body: %s", jsonData)
		body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer closer.Close()
		requestBody = body
	}
	if billingErr := service.PrepareImageBillingForRequest(c, info, imageCount); billingErr != nil {
		return billingErr
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK || (adaptImage && imageAdapter.Task != nil) {
			if httpResp.StatusCode == http.StatusCreated && info.ApiType == constant.APITypeReplicate {
				// replicate channel returns 201 Created when using Prefer: wait, treat it as success.
				httpResp.StatusCode = http.StatusOK
			} else if httpResp.StatusCode == http.StatusAccepted || (httpResp.StatusCode == http.StatusOK && adaptImage && imageAdapter.Task != nil) {
				snapshot, err := parseNativeAsyncImageSnapshot(httpResp, info)
				if err != nil {
					return types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
				}
				if c.GetBool("async_image_worker") {
					c.Set(NativeAsyncImageFormat, snapshot)
					return nil
				}
				return SubmitInternalAsyncImageTask(c, info, request, snapshot)
			} else {
				newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
				// reset status code 重置状态码
				service.ResetStatusCode(newAPIError, statusCodeMappingStr)
				return newAPIError
			}
		}
	}
	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	// The log content shows the settled count: the count the handler derived
	// from the upstream response when it did, otherwise the reserved quantity.
	imageN := info.RequestedImageCount()
	if info.BillingImageCount != nil {
		imageN = *info.BillingImageCount
	} else if count, ok := info.PriceData.OtherRatios()["n"]; ok && info.PriceData.UsePrice && count >= 1 && count <= dto.MaxImageN {
		imageN = common.QuotaRound(count)
	}

	if usage.(*dto.Usage).TotalTokens == 0 {
		usage.(*dto.Usage).TotalTokens = 1
	}
	if usage.(*dto.Usage).PromptTokens == 0 {
		usage.(*dto.Usage).PromptTokens = 1
	}

	quality := request.Quality
	if quality == "" {
		quality = "standard"
	}

	var logContent []string

	if len(request.Size) > 0 {
		logContent = append(logContent, fmt.Sprintf("大小 %s", request.Size))
	}
	if len(quality) > 0 {
		logContent = append(logContent, fmt.Sprintf("品质 %s", quality))
	}
	if imageN > 0 {
		logContent = append(logContent, fmt.Sprintf("生成数量 %d", imageN))
	}

	usageDto := usage.(*dto.Usage)
	if c.GetBool("async_image_worker") {
		c.Set("async_image_usage", usageDto)
	} else {
		service.PostTextConsumeQuota(c, info, usageDto, logContent)
	}
	return nil
}

// mappedImageJSONBody preserves the original JSON payload while replacing only
// the public model alias with the channel's mapped upstream model.
func mappedImageJSONBody(storage common.BodyStorage, modelName string) (io.Reader, error) {
	raw, err := storage.Bytes()
	if err != nil {
		return nil, fmt.Errorf("read image request body failed: %w", err)
	}
	var payload map[string]json.RawMessage
	if err = common.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode image request body failed: %w", err)
	}
	payload["model"], err = common.Marshal(modelName)
	if err != nil {
		return nil, fmt.Errorf("encode mapped image model failed: %w", err)
	}
	mapped, err := common.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode mapped image request body failed: %w", err)
	}
	return bytes.NewReader(mapped), nil
}

func preConsumeAsyncImageBilling(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if info == nil || info.PriceData.FreeModel || info.PriceData.QuotaToPreConsume <= 0 || info.Billing != nil {
		return nil
	}
	info.ForcePreConsume = true
	return service.PreConsumeBilling(c, info.PriceData.QuotaToPreConsume, info)
}
