package dto

import (
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
)

// Relay protocol DTOs live in relaykit. These aliases keep the local task and
// asynchronous image extensions source-compatible while they are migrated.
type (
	AudioRequest                  = relaydto.AudioRequest
	BoolValue                     = relaydto.BoolValue
	ChannelOtherSettings          = relaydto.ChannelOtherSettings
	ChatCompletionsStreamResponse = relaydto.ChatCompletionsStreamResponse
	ClaudeErrorWithStatusCode     = relaydto.ClaudeErrorWithStatusCode
	ClaudeRequest                 = relaydto.ClaudeRequest
	EmbeddingRequest              = relaydto.EmbeddingRequest
	GeminiChatCandidate           = relaydto.GeminiChatCandidate
	GeminiChatContent             = relaydto.GeminiChatContent
	GeminiChatGenerationConfig    = relaydto.GeminiChatGenerationConfig
	GeminiChatRequest             = relaydto.GeminiChatRequest
	GeminiChatResponse            = relaydto.GeminiChatResponse
	GeminiInlineData              = relaydto.GeminiInlineData
	GeminiPart                    = relaydto.GeminiPart
	GeminiUsageMetadata           = relaydto.GeminiUsageMetadata
	GeneralErrorResponse          = relaydto.GeneralErrorResponse
	GeneralOpenAIRequest          = relaydto.GeneralOpenAIRequest
	ImageAsyncSubmitResponse      = relaydto.ImageAsyncSubmitResponse
	ImageAsyncTaskResponse        = relaydto.ImageAsyncTaskResponse
	ImageData                     = relaydto.ImageData
	ImageRequest                  = relaydto.ImageRequest
	ImageResponse                 = relaydto.ImageResponse
	InputTokenDetails             = relaydto.InputTokenDetails
	IntValue                      = relaydto.IntValue
	Message                       = relaydto.Message
	Notify                        = relaydto.Notify
	OpenAIError                   = relaytypes.OpenAIError
	OpenAIErrorWithStatusCode     = relaydto.OpenAIErrorWithStatusCode
	OpenAIResponsesRequest        = relaydto.OpenAIResponsesRequest
	OpenAITextResponse            = relaydto.OpenAITextResponse
	OpenAITextResponseChoice      = relaydto.OpenAITextResponseChoice
	OpenAIVideo                   = relaydto.OpenAIVideo
	OpenAIVideoError              = relaydto.OpenAIVideoError
	OutputTokenDetails            = relaydto.OutputTokenDetails
	PlayGroundRequest             = relaydto.PlayGroundRequest
	Request                       = relaydto.Request
	RerankRequest                 = relaydto.RerankRequest
	ToolCallResponse              = relaydto.ToolCallResponse
	Usage                         = relaydto.Usage
	UserSetting                   = relaydto.UserSetting
)

const (
	ImageAsyncStatusSubmitted  = relaydto.ImageAsyncStatusSubmitted
	ImageAsyncStatusQueued     = relaydto.ImageAsyncStatusQueued
	ImageAsyncStatusProcessing = relaydto.ImageAsyncStatusProcessing
	ImageAsyncStatusSucceeded  = relaydto.ImageAsyncStatusSucceeded
	ImageAsyncStatusFailed     = relaydto.ImageAsyncStatusFailed
	ImageAsyncStatusExpired    = relaydto.ImageAsyncStatusExpired

	NotifyTypeQuotaExceed = relaydto.NotifyTypeQuotaExceed

	VideoStatusUnknown    = relaydto.VideoStatusUnknown
	VideoStatusQueued     = relaydto.VideoStatusQueued
	VideoStatusInProgress = relaydto.VideoStatusInProgress
	VideoStatusCompleted  = relaydto.VideoStatusCompleted
	VideoStatusFailed     = relaydto.VideoStatusFailed
)

var (
	NewNotify         = relaydto.NewNotify
	NewOpenAIVideo    = relaydto.NewOpenAIVideo
	NotifyTypeBark    = relaydto.NotifyTypeBark
	NotifyTypeEmail   = relaydto.NotifyTypeEmail
	NotifyTypeGotify  = relaydto.NotifyTypeGotify
	NotifyTypeWebhook = relaydto.NotifyTypeWebhook
)
