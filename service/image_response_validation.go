package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func ValidateOpenAIImageResponseForBilling(body []byte) error {
	var payload struct {
		Data  dto.ImageDataList `json:"data"`
		Usage *struct {
			OutputTokens     *int `json:"output_tokens"`
			CompletionTokens int  `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return err
	}

	hasImage := false
	for _, item := range payload.Data {
		if strings.TrimSpace(item.Url) != "" || strings.TrimSpace(item.B64Json) != "" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return errors.New("image generation failed: upstream returned no image result")
	}

	if usage := payload.Usage; usage != nil && usage.OutputTokens != nil && imageCompletionTokens(*usage.OutputTokens, usage.CompletionTokens) <= 0 {
		return errors.New("image generation failed: upstream output_tokens is 0")
	}

	return nil
}
