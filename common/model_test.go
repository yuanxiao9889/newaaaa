package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestIsImageGenerationModelRecognizesOopiiModelsExactly(t *testing.T) {
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
			assert.True(t, IsImageGenerationModel(modelName))
		})
	}

	assert.False(t, IsImageGenerationModel("grok-4.5"))
	assert.False(t, IsImageGenerationModel("gemini-3.5-flash"))
	assert.False(t, IsImageGenerationModel("prefix-all-image-2-suffix"))
}

func TestWanEndpointsDistinguishImagesFromVideos(t *testing.T) {
	for _, name := range []string{
		"wan2.7-image-pro", "wan2.7-image", "wan2.6-image", "wan2.6-t2i",
		"wan2.5-t2i-preview", "wan2.2-t2i-flash", "wan2.2-t2i-plus",
		"wanx2.1-t2i-turbo", "wanx2.1-t2i-plus", "wanx2.0-t2i-turbo",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, GetEndpointTypesByChannelType(constant.ChannelTypeAli, name), constant.EndpointTypeImageGeneration)
		})
	}
	for _, name := range []string{
		"wanx2.1-t2v-plus", "wanx2.1-t2v-turbo", "wanx2.1-i2v-plus", "wanx2.1-i2v-turbo",
	} {
		t.Run(name, func(t *testing.T) {
			assert.NotContains(t, GetEndpointTypesByChannelType(constant.ChannelTypeAli, name), constant.EndpointTypeImageGeneration)
		})
	}
}
