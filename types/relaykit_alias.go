package types

import relaytypes "github.com/QuantumNous/new-api/relaykit/types"

// Relay transport types live in relaykit. Keep aliases here for local billing
// and asynchronous-task extensions that also depend on the host PriceData.
type (
	ChannelError   = relaytypes.ChannelError
	ErrorCode      = relaytypes.ErrorCode
	NewAPIError    = relaytypes.NewAPIError
	RelayFormat    = relaytypes.RelayFormat
	TokenCountMeta = relaytypes.TokenCountMeta
)

const (
	ErrorCodeBadResponse                 = relaytypes.ErrorCodeBadResponse
	ErrorCodeBadResponseBody             = relaytypes.ErrorCodeBadResponseBody
	ErrorCodeChannelModelMappedError     = relaytypes.ErrorCodeChannelModelMappedError
	ErrorCodeChannelParamOverrideInvalid = relaytypes.ErrorCodeChannelParamOverrideInvalid
	ErrorCodeChannelResponseTimeExceeded = relaytypes.ErrorCodeChannelResponseTimeExceeded
	ErrorCodeClientRequestCanceled       = relaytypes.ErrorCodeClientRequestCanceled
	ErrorCodeConvertRequestFailed        = relaytypes.ErrorCodeConvertRequestFailed
	ErrorCodeDoRequestFailed             = relaytypes.ErrorCodeDoRequestFailed
	ErrorCodeGenRelayInfoFailed          = relaytypes.ErrorCodeGenRelayInfoFailed
	ErrorCodeInvalidApiType              = relaytypes.ErrorCodeInvalidApiType
	ErrorCodeInvalidRequest              = relaytypes.ErrorCodeInvalidRequest
	ErrorCodeJsonMarshalFailed           = relaytypes.ErrorCodeJsonMarshalFailed
	ErrorCodeModelPriceError             = relaytypes.ErrorCodeModelPriceError
	ErrorCodeReadRequestBodyFailed       = relaytypes.ErrorCodeReadRequestBodyFailed
	ErrorCodeReadResponseBodyFailed      = relaytypes.ErrorCodeReadResponseBodyFailed
	ErrorCodeUpdateDataError             = relaytypes.ErrorCodeUpdateDataError

	RelayFormatClaude                    = relaytypes.RelayFormatClaude
	RelayFormatEmbedding                 = relaytypes.RelayFormatEmbedding
	RelayFormatGemini                    = relaytypes.RelayFormatGemini
	RelayFormatMjProxy                   = relaytypes.RelayFormatMjProxy
	RelayFormatOpenAI                    = relaytypes.RelayFormatOpenAI
	RelayFormatOpenAIAlphaSearch         = relaytypes.RelayFormatOpenAIAlphaSearch
	RelayFormatOpenAIAudio               = relaytypes.RelayFormatOpenAIAudio
	RelayFormatOpenAIImage               = relaytypes.RelayFormatOpenAIImage
	RelayFormatOpenAIRealtime            = relaytypes.RelayFormatOpenAIRealtime
	RelayFormatOpenAIResponses           = relaytypes.RelayFormatOpenAIResponses
	RelayFormatOpenAIResponsesCompaction = relaytypes.RelayFormatOpenAIResponsesCompaction
	RelayFormatRerank                    = relaytypes.RelayFormatRerank
	RelayFormatTask                      = relaytypes.RelayFormatTask
)

var (
	ErrOptionWithSkipRetry  = relaytypes.ErrOptionWithSkipRetry
	ErrOptionWithStatusCode = relaytypes.ErrOptionWithStatusCode
	IsRecordErrorLog        = relaytypes.IsRecordErrorLog
	IsSkipRetryError        = relaytypes.IsSkipRetryError
	NewChannelError         = relaytypes.NewChannelError
	NewError                = relaytypes.NewError
	NewErrorWithStatusCode  = relaytypes.NewErrorWithStatusCode
	NewOpenAIError          = relaytypes.NewOpenAIError
)
