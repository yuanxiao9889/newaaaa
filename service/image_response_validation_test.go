package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateOpenAIImageResponseForBillingObjectData(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		valid bool
	}{
		{"URL image", `{"data":{"url":"https://example.com/image.png"}}`, true},
		{"base64 image", `{"data":{"b64_json":"abc123"},"usage":{"output_tokens":80}}`, true},
		{"empty object", `{"data":{}}`, false},
		{"blank image", `{"data":{"url":"  "}}`, false},
		{"zero output tokens", `{"data":{"b64_json":"abc123"},"usage":{"output_tokens":0}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOpenAIImageResponseForBilling([]byte(tc.body))
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidateOpenAIImageResponseForBillingRejectsZeroOutputTokens(t *testing.T) {
	body := []byte(`{
		"created": 1710000000,
		"data": [{"url": "https://example.com/image.png"}],
		"usage": {"input_tokens": 120, "output_tokens": 0, "total_tokens": 120}
	}`)

	if err := ValidateOpenAIImageResponseForBilling(body); err == nil {
		t.Fatal("expected zero output_tokens image response to be rejected")
	}
}

func TestValidateOpenAIImageResponseForBillingRejectsMissingImage(t *testing.T) {
	body := []byte(`{
		"created": 1710000000,
		"data": [],
		"usage": {"input_tokens": 120, "output_tokens": 80, "total_tokens": 200}
	}`)

	if err := ValidateOpenAIImageResponseForBilling(body); err == nil {
		t.Fatal("expected image response without image data to be rejected")
	}
}

func TestValidateOpenAIImageResponseForBillingAllowsLegacyImageWithoutUsage(t *testing.T) {
	body := []byte(`{
		"created": 1710000000,
		"data": [{"b64_json": "abc123"}]
	}`)

	if err := ValidateOpenAIImageResponseForBilling(body); err != nil {
		t.Fatalf("expected legacy image response without usage to pass, got %v", err)
	}
}
