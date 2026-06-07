package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestValidateGeminiImageOutputForBillingRejectsZeroOutputTokens(t *testing.T) {
	info := &relaycommon.RelayInfo{
		Request: &dto.GeminiChatRequest{
			GenerationConfig: dto.GeminiChatGenerationConfig{
				ResponseModalities: []string{"TEXT", "IMAGE"},
			},
		},
	}
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Parts: []dto.GeminiPart{
						{
							InlineData: &dto.GeminiInlineData{
								MimeType: "image/png",
								Data:     "abc123",
							},
						},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     100,
			CandidatesTokenCount: 0,
			TotalTokenCount:      100,
		},
	}

	if err := validateGeminiImageOutputForBilling(info, resp); err == nil {
		t.Fatal("expected zero output token Gemini image response to be rejected")
	}
}

func TestValidateGeminiImageOutputForBillingAllowsTextRequest(t *testing.T) {
	info := &relaycommon.RelayInfo{
		Request: &dto.GeminiChatRequest{
			GenerationConfig: dto.GeminiChatGenerationConfig{
				ResponseModalities: []string{"TEXT"},
			},
		},
	}
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{}},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     100,
			CandidatesTokenCount: 0,
			TotalTokenCount:      100,
		},
	}

	if err := validateGeminiImageOutputForBilling(info, resp); err != nil {
		t.Fatalf("expected text-only request to pass image validation, got %v", err)
	}
}
