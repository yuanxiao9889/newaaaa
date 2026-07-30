package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacySecondBasedFixedPriceModel(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		endpoints []constant.EndpointType
		want      bool
	}{
		{name: "known preview model", modelName: "ok-video-1.5-preview", want: true},
		{name: "legacy ok video exception", modelName: "ok-video", want: false},
		{name: "video endpoint", modelName: "custom-model", endpoints: []constant.EndpointType{constant.EndpointTypeOpenAIVideo}, want: true},
		{name: "video marker", modelName: "vendor-veo-fast", want: true},
		{name: "ordinary fixed model", modelName: "image-generator", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, LegacySecondBasedFixedPriceModel(test.modelName, test.endpoints))
		})
	}
}

func TestPricingFixedPriceUnitJSONContract(t *testing.T) {
	payload, err := common.Marshal(Pricing{
		ModelName:      "video-model",
		QuotaType:      1,
		ModelPrice:     0.45,
		FixedPriceUnit: "second",
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))
	assert.Equal(t, "second", decoded["fixed_price_unit"])
}
