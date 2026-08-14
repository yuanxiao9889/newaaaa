package common

import (
	"testing"

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
