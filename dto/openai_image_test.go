package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestImageRequestPreservesOopiiCompatibilityFields(t *testing.T) {
	input := []byte(`{
		"model":"grok-imagine-image-pro",
		"prompt":"keep the subject",
		"n":1,
		"aspect_ratio":"3:2",
		"aspectRatio":"3:2",
		"image_size":"2K",
		"image_backend":"auto",
		"response_format":"b64_json",
		"reference_images":["data:image/png;base64,AAAA"],
		"output_resolution":"2K",
		"resolution":"2K",
		"generationConfig":{"imageConfig":{"aspectRatio":"3:2","imageSize":"2K"}},
		"extra_body":{"aspect_ratio":"3:2","aspectRatio":"3:2"}
	}`)

	var request ImageRequest
	require.NoError(t, common.Unmarshal(input, &request))
	require.Empty(t, request.Extra)

	encoded, err := common.Marshal(request)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	require.Equal(t, "3:2", payload["aspect_ratio"])
	require.Equal(t, "3:2", payload["aspectRatio"])
	require.Equal(t, "2K", payload["image_size"])
	require.Equal(t, "auto", payload["image_backend"])
	require.Equal(t, "b64_json", payload["response_format"])
	require.Equal(t, "2K", payload["output_resolution"])
	require.Equal(t, "2K", payload["resolution"])
	require.Contains(t, payload, "reference_images")
	require.Contains(t, payload, "generationConfig")
	require.Contains(t, payload, "extra_body")
}
