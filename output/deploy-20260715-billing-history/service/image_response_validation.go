package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func ValidateOpenAIImageResponseForBilling(body []byte) error {
	var payload struct {
		Data  []dto.ImageData `json:"data"`
		Usage *struct {
			OutputTokens *int `json:"output_tokens"`
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

	if payload.Usage != nil && payload.Usage.OutputTokens != nil && *payload.Usage.OutputTokens <= 0 {
		return errors.New("image generation failed: upstream output_tokens is 0")
	}

	return nil
}
