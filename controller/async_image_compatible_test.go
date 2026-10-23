package controller

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCompatibleImageResultExtraction(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,cGl4ZWxz"}}]}}]}`,
		`{"choices":[{"message":{"content":"![result](https://fixture.invalid/a.png) ![duplicate](https://fixture.invalid/a.png)"}}]}`,
		`{"output":[{"type":"image_generation_call","result":"cGl4ZWxz"}]}`,
	} {
		response, err := compatibleImageResponse([]byte(body))
		require.NoError(t, err)
		require.Len(t, response.Data, 1)
	}
	for _, body := range []string{
		`{"input":[{"type":"image_url","image_url":"https://fixture.invalid/reference.png"}],"output":[]}`,
		`{"choices":[{"message":{"content":"just text","tool_calls":[{"arguments":"https://fixture.invalid/tool.png"}]}}]}`,
		`{"output":[{"type":"output_image","url":"file:///private/image.png"}]}`,
		`not json`,
	} {
		_, err := compatibleImageResponse([]byte(body))
		require.Error(t, err)
	}
}
