package common

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

// GetEndpointTypesByChannelType returns the endpoint types supported by a channel/model pair.
func GetEndpointTypesByChannelType(channelType int, modelName string) []constant.EndpointType {
	if channelType == constant.ChannelTypeNewAPI {
		normalizedModel := strings.TrimSpace(modelName)
		switch {
		case strings.HasPrefix(strings.ToLower(normalizedModel), "hj-2.0-"):
			return []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
		case normalizedModel == "MiniMax-H3",
			normalizedModel == "MiniMax-H3-C",
			normalizedModel == "MiniMax-H3-933-1440P-GF",
			strings.EqualFold(normalizedModel, "OK-video"),
			normalizedModel == "ok-video-1.5-preview",
			normalizedModel == "ok-imagine-video":
			return []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
		}
	}
	var endpointTypes []constant.EndpointType
	switch channelType {
	case constant.ChannelTypeJina:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeJinaRerank}
	case constant.ChannelTypeAws:
		fallthrough
	case constant.ChannelTypeAnthropic:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeVertexAi:
		fallthrough
	case constant.ChannelTypeGemini:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeOpenRouter:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
	case constant.ChannelTypeXai:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse}
	case constant.ChannelTypeSora, constant.ChannelTypeGrokVideo:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
	case constant.ChannelTypeSub2API, constant.ChannelTypeNewAPI:
		endpointTypes = []constant.EndpointType{
			constant.EndpointTypeOpenAI,
			constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact,
			constant.EndpointTypeAnthropic,
			constant.EndpointTypeGemini,
			constant.EndpointTypeOpenAIAlphaSearch,
		}
	case constant.ChannelTypeCodex:
		endpointTypes = []constant.EndpointType{
			constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact,
			constant.EndpointTypeOpenAIAlphaSearch,
		}
	default:
		if IsOpenAIResponseOnlyModel(modelName) {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIResponse}
		} else {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
		}
	}
	if IsImageGenerationModel(modelName) {
		endpointTypes = append([]constant.EndpointType{constant.EndpointTypeImageGeneration}, endpointTypes...)
	}
	return endpointTypes
}
