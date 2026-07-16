package service

import "testing"

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
