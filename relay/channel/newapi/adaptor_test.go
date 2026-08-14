package newapi

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequestPreservesOopiiGrokFields(t *testing.T) {
	var request dto.ImageRequest
	require.NoError(t, common.UnmarshalJsonStr(`{
		"model":"grok-imagine-image-2.0",
		"prompt":"keep the character identity",
		"n":1,
		"response_format":"b64_json",
		"resolution":"2k",
		"aspect_ratio":"auto",
		"quality":"medium",
		"images":[
			{"url":"data:image/png;base64,AAAA"},
			{"url":"data:image/png;base64,BBBB"}
		]
	}`, &request))

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, nil, request)
	require.NoError(t, err)

	imageRequest, ok := converted.(dto.ImageRequest)
	require.True(t, ok)
	assert.Equal(t, "grok-imagine-image-2.0", imageRequest.Model)
	assert.Equal(t, `"2k"`, string(imageRequest.Resolution))
	assert.Equal(t, `"auto"`, string(imageRequest.AspectRatio))
	assert.Equal(t, "medium", imageRequest.Quality)
	assert.JSONEq(t, `[
		{"url":"data:image/png;base64,AAAA"},
		{"url":"data:image/png;base64,BBBB"}
	]`, string(imageRequest.Images))
}

func TestConvertGeminiRequestPreservesOopiiImageConfigAndInlineData(t *testing.T) {
	var request dto.GeminiChatRequest
	require.NoError(t, common.UnmarshalJsonStr(`{
		"contents":[{
			"role":"user",
			"parts":[
				{"text":"cinematic frame"},
				{"inlineData":{"mimeType":"image/png","data":"AAAA"}}
			]
		}],
		"generationConfig":{
			"topP":0.95,
			"responseModalities":["IMAGE"],
			"imageConfig":{"aspectRatio":"16:9","imageSize":"2K"}
		}
	}`, &request))

	converted, err := (&Adaptor{}).ConvertGeminiRequest(nil, nil, &request)
	require.NoError(t, err)

	geminiRequest, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Same(t, &request, geminiRequest)
	require.Len(t, geminiRequest.Contents, 1)
	require.Len(t, geminiRequest.Contents[0].Parts, 2)
	require.NotNil(t, geminiRequest.Contents[0].Parts[1].InlineData)
	assert.Equal(t, "image/png", geminiRequest.Contents[0].Parts[1].InlineData.MimeType)
	assert.Equal(t, "AAAA", geminiRequest.Contents[0].Parts[1].InlineData.Data)
	assert.JSONEq(t, `{"aspectRatio":"16:9","imageSize":"2K"}`, string(geminiRequest.GenerationConfig.ImageConfig))
	assert.Equal(t, []string{"IMAGE"}, geminiRequest.GenerationConfig.ResponseModalities)
}

func TestGetRequestURLPreservesEncodedOopiiModelID(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1beta/models/monkey-image-flash%202:generateContent",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://www.oopii.cc",
		},
	}

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t,
		"https://www.oopii.cc/v1beta/models/monkey-image-flash%202:generateContent",
		requestURL,
	)
}
