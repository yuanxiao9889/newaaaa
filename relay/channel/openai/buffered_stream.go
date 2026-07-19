package openai

import (
	"bufio"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type bufferedChatChoice struct {
	index          int
	role           string
	content        strings.Builder
	reasoning      strings.Builder
	finishReason   string
	toolCalls      map[int]*dto.ToolCallResponse
	receivedChoice bool
}

type bufferedChatStream struct {
	id                string
	model             string
	created           int64
	systemFingerprint *string
	choices           map[int]*bufferedChatChoice
	usage             *dto.Usage
	lastData          string
	receivedChunk     bool
}

func isOpenAIEventStream(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

func looksLikeOpenAISSE(body []byte) bool {
	return strings.HasPrefix(strings.TrimSpace(string(body)), "data:")
}

func (s *bufferedChatStream) addChunk(chunk *dto.ChatCompletionsStreamResponse) {
	if chunk == nil {
		return
	}
	s.receivedChunk = true
	if chunk.Id != "" {
		s.id = chunk.Id
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.Created != 0 {
		s.created = chunk.Created
	}
	if chunk.SystemFingerprint != nil {
		s.systemFingerprint = chunk.SystemFingerprint
	}
	if chunk.Usage != nil {
		usage := *chunk.Usage
		s.usage = &usage
	}

	for _, streamChoice := range chunk.Choices {
		choice, ok := s.choices[streamChoice.Index]
		if !ok {
			choice = &bufferedChatChoice{
				index:     streamChoice.Index,
				toolCalls: make(map[int]*dto.ToolCallResponse),
			}
			s.choices[streamChoice.Index] = choice
		}
		choice.receivedChoice = true
		if streamChoice.Delta.Role != "" {
			choice.role = streamChoice.Delta.Role
		}
		choice.content.WriteString(streamChoice.Delta.GetContentString())
		choice.reasoning.WriteString(streamChoice.Delta.GetReasoningContent())
		if streamChoice.FinishReason != nil {
			choice.finishReason = *streamChoice.FinishReason
		}

		for position, toolDelta := range streamChoice.Delta.ToolCalls {
			toolIndex := position
			if toolDelta.Index != nil {
				toolIndex = *toolDelta.Index
			}
			toolCall, exists := choice.toolCalls[toolIndex]
			if !exists {
				toolCall = &dto.ToolCallResponse{}
				choice.toolCalls[toolIndex] = toolCall
			}
			if toolDelta.ID != "" {
				toolCall.ID = toolDelta.ID
			}
			if toolDelta.Type != nil {
				toolCall.Type = toolDelta.Type
			}
			toolCall.Function.Name += toolDelta.Function.Name
			toolCall.Function.Arguments += toolDelta.Function.Arguments
		}
	}
}

func (s *bufferedChatStream) response(c *gin.Context, info *relaycommon.RelayInfo) (*dto.OpenAITextResponse, *dto.Usage, error) {
	if !s.receivedChunk {
		return nil, nil, fmt.Errorf("empty OpenAI-compatible SSE response")
	}

	choiceIndexes := make([]int, 0, len(s.choices))
	for index := range s.choices {
		choiceIndexes = append(choiceIndexes, index)
	}
	sort.Ints(choiceIndexes)

	choices := make([]dto.OpenAITextResponseChoice, 0, len(choiceIndexes))
	var usageText strings.Builder
	toolCount := 0
	for _, index := range choiceIndexes {
		bufferedChoice := s.choices[index]
		if !bufferedChoice.receivedChoice {
			continue
		}
		message := dto.Message{Role: bufferedChoice.role}
		if message.Role == "" {
			message.Role = "assistant"
		}
		if bufferedChoice.content.Len() > 0 {
			content := bufferedChoice.content.String()
			message.Content = content
			usageText.WriteString(content)
		}
		if bufferedChoice.reasoning.Len() > 0 {
			reasoning := bufferedChoice.reasoning.String()
			message.ReasoningContent = &reasoning
			usageText.WriteString(reasoning)
		}

		if len(bufferedChoice.toolCalls) > 0 {
			toolIndexes := make([]int, 0, len(bufferedChoice.toolCalls))
			for toolIndex := range bufferedChoice.toolCalls {
				toolIndexes = append(toolIndexes, toolIndex)
			}
			sort.Ints(toolIndexes)
			toolCalls := make([]dto.ToolCallResponse, 0, len(toolIndexes))
			for _, toolIndex := range toolIndexes {
				toolCall := *bufferedChoice.toolCalls[toolIndex]
				toolCall.Index = nil
				toolCalls = append(toolCalls, toolCall)
				usageText.WriteString(toolCall.Function.Name)
				usageText.WriteString(toolCall.Function.Arguments)
			}
			toolCallsJSON, err := common.Marshal(toolCalls)
			if err != nil {
				return nil, nil, err
			}
			message.ToolCalls = toolCallsJSON
			toolCount += len(toolCalls)
		}

		choices = append(choices, dto.OpenAITextResponseChoice{
			Index:        bufferedChoice.index,
			Message:      message,
			FinishReason: bufferedChoice.finishReason,
		})
	}

	usage := s.usage
	if usage == nil || usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, usageText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		usage.CompletionTokens += toolCount * 7
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	applyUsagePostProcessing(info, usage, common.StringToByteSlice(s.lastData))

	id := s.id
	if id == "" {
		id = helper.GetResponseID(c)
	}
	model := s.model
	if model == "" {
		model = info.UpstreamModelName
	}
	created := s.created
	if created == 0 {
		created = time.Now().Unix()
	}

	response := &dto.OpenAITextResponse{
		Id:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: choices,
		Usage:   *usage,
	}
	return response, usage, nil
}

// OaiBufferedStreamHandler converts an upstream Chat Completions SSE response
// into the single JSON response requested by a non-streaming client.
func OaiBufferedStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	stream := bufferedChatStream{choices: make(map[int]*bufferedChatChoice)}
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		info.SetFirstResponseTime()
		info.ReceivedResponseCount++
		stream.lastData = data

		var errorResponse dto.OpenAITextResponse
		if err := common.UnmarshalJsonStr(data, &errorResponse); err == nil {
			if openAIError := errorResponse.GetOpenAIError(); openAIError != nil && openAIError.Type != "" {
				return nil, types.WithOpenAIError(*openAIError, resp.StatusCode)
			}
		}

		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			logger.LogError(c, "failed to unmarshal buffered chat stream event: "+err.Error())
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		stream.addChunk(&chunk)
	}
	if err := scanner.Err(); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	response, usage, err := stream.response(c, info)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	for _, choice := range response.Choices {
		if choice.FinishReason == constant.FinishReasonContentFilter {
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
			break
		}
	}

	var responseBody []byte
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		responseBody, err = common.Marshal(service.ResponseOpenAI2Claude(response, info))
	case types.RelayFormatGemini:
		responseBody, err = common.Marshal(service.ResponseOpenAI2Gemini(response, info))
	default:
		responseBody, err = common.Marshal(response)
	}
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	responseMeta := *resp
	responseMeta.Header = resp.Header.Clone()
	responseMeta.Header.Set("Content-Type", "application/json")
	responseMeta.Header.Del("Transfer-Encoding")
	service.IOCopyBytesGracefully(c, &responseMeta, responseBody)
	return usage, nil
}
