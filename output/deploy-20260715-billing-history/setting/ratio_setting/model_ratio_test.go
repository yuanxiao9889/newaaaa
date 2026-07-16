package ratio_setting

import (
	"math"
	"testing"
)

func TestGetCompletionRatioPrefersConfiguredValueOverHardcodedDefault(t *testing.T) {
	original := completionRatioMap.ReadAll()
	completionRatioMap.Clear()
	defer func() {
		completionRatioMap.Clear()
		completionRatioMap.AddAll(original)
	}()

	completionRatioMap.Set("gpt-5.5", 5)

	if got := GetCompletionRatio("gpt-5.5"); got != 5 {
		t.Fatalf("expected configured completion ratio 5, got %v", got)
	}
}

func TestGPTImage2OFDefaultTokenPricing(t *testing.T) {
	originalModelRatio := modelRatioMap.ReadAll()
	originalCompletionRatio := completionRatioMap.ReadAll()
	modelRatioMap.Clear()
	completionRatioMap.Clear()
	modelRatioMap.AddAll(defaultModelRatio)
	completionRatioMap.AddAll(defaultCompletionRatio)
	defer func() {
		modelRatioMap.Clear()
		completionRatioMap.Clear()
		modelRatioMap.AddAll(originalModelRatio)
		completionRatioMap.AddAll(originalCompletionRatio)
	}()

	modelRatio, ok, _ := GetModelRatio("gpt-image-2-OF")
	if !ok {
		t.Fatalf("expected gpt-image-2-OF model ratio to be configured")
	}
	expectedModelRatio := 13.0 / 1000 * RMB
	if math.Abs(modelRatio-expectedModelRatio) > 1e-9 {
		t.Fatalf("expected model ratio %v, got %v", expectedModelRatio, modelRatio)
	}

	if got := GetCompletionRatio("gpt-image-2-OF"); got != 6 {
		t.Fatalf("expected completion ratio 6, got %v", got)
	}
}
