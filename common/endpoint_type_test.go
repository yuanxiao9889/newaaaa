package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestNewAPIVideoModelsUseVideoEndpoint(t *testing.T) {
	models := []string{
		"hj-2.0-fast-480p",
		"MiniMax-H3",
		"MiniMax-H3-C",
		"MiniMax-H3-933-1440P-GF",
		"OK-video",
		"ok-video-1.5-preview",
		"ok-imagine-video",
	}
	for _, modelName := range models {
		t.Run(modelName, func(t *testing.T) {
			assert.Equal(t,
				[]constant.EndpointType{constant.EndpointTypeOpenAIVideo},
				GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, modelName),
			)
		})
	}
	assert.Contains(t,
		GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, "gpt-4.1"),
		constant.EndpointTypeOpenAI,
	)
}

func TestNewAPIImageModelsExposeImageGenerationEndpoint(t *testing.T) {
	models := []string{
		"all-image-2",
		"all-image-2-DD",
		"all-image-2-H",
		"gpt-image-2",
		"grok-imagine-image-pro",
		"grok-imagine-image-2.0",
		"monkey-image-pro",
		"monkey-image-flash 2",
	}
	for _, modelName := range models {
		t.Run(modelName, func(t *testing.T) {
			assert.Contains(t,
				GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, modelName),
				constant.EndpointTypeImageGeneration,
			)
		})
	}
}

func TestOpenAIVideoEndpointDefault(t *testing.T) {
	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeOpenAIVideo)
	assert.True(t, ok)
	assert.Equal(t, EndpointInfo{Path: "/v1/video/generations", Method: "POST"}, info)
}
