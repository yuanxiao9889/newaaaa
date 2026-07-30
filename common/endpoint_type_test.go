package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestNewAPISeedanceModelsUseVideoEndpoint(t *testing.T) {
	assert.Equal(t,
		[]constant.EndpointType{constant.EndpointTypeOpenAIVideo},
		GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, "hj-2.0-fast-480p"),
	)
	assert.Contains(t,
		GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, "gpt-4.1"),
		constant.EndpointTypeOpenAI,
	)
}

func TestOpenAIVideoEndpointDefault(t *testing.T) {
	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeOpenAIVideo)
	assert.True(t, ok)
	assert.Equal(t, EndpointInfo{Path: "/v1/video/generations", Method: "POST"}, info)
}
