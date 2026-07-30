package relay

import (
	"net/http"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTaskBillingRatiosAppliesFixedPriceUnit(t *testing.T) {
	tests := []struct {
		name             string
		modelName        string
		unit             string
		ratios           map[string]float64
		wantRatios       map[string]float64
		wantResolvedUnit string
		wantErr          bool
	}{
		{
			name:             "per request ignores seconds and keeps resolution",
			modelName:        "video-test",
			unit:             billing_setting.FixedPriceUnitRequest,
			ratios:           map[string]float64{"seconds": 6, "resolution": 2},
			wantRatios:       map[string]float64{"resolution": 2},
			wantResolvedUnit: billing_setting.FixedPriceUnitRequest,
		},
		{
			name:             "per second keeps duration and resolution",
			modelName:        "video-test",
			unit:             billing_setting.FixedPriceUnitSecond,
			ratios:           map[string]float64{"seconds": 6, "resolution": 2},
			wantRatios:       map[string]float64{"seconds": 6, "resolution": 2},
			wantResolvedUnit: billing_setting.FixedPriceUnitSecond,
		},
		{
			name:             "legacy video with seconds remains per second",
			modelName:        "video-test",
			ratios:           map[string]float64{"seconds": 8, "size": 1.5},
			wantRatios:       map[string]float64{"seconds": 8, "size": 1.5},
			wantResolvedUnit: billing_setting.FixedPriceUnitSecond,
		},
		{
			name:             "legacy fixed task without duration defaults per request",
			modelName:        "image-test",
			ratios:           map[string]float64{"size": 1.5},
			wantRatios:       map[string]float64{"size": 1.5},
			wantResolvedUnit: billing_setting.FixedPriceUnitRequest,
		},
		{
			name:      "explicit per second requires duration",
			modelName: "custom-model",
			unit:      billing_setting.FixedPriceUnitSecond,
			ratios:    map[string]float64{"resolution": 2},
			wantErr:   true,
		},
		{
			name:      "legacy video name without duration fails closed",
			modelName: "legacy-video-model",
			ratios:    map[string]float64{"resolution": 2},
			wantErr:   true,
		},
		{
			name:      "invalid snapshot unit fails closed",
			modelName: "custom-model",
			unit:      "minute",
			ratios:    map[string]float64{"seconds": 6},
			wantErr:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: test.modelName,
				PriceData: types.PriceData{
					UsePrice:       true,
					FixedPriceUnit: test.unit,
				},
			}

			ratios, taskErr := normalizeTaskBillingRatios(info, test.ratios)
			if test.wantErr {
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				return
			}

			require.Nil(t, taskErr)
			assert.Equal(t, test.wantRatios, ratios)
			assert.Equal(t, test.wantResolvedUnit, info.PriceData.FixedPriceUnit)
		})
	}
}

func TestMergeTaskBillingRatiosPreservesRemixMetadata(t *testing.T) {
	preserved := map[string]float64{"seconds": 8, "resolution": 2}
	estimated := map[string]float64{"seconds": 10, "quality": 1.5}

	merged := mergeTaskBillingRatios(preserved, estimated)

	assert.Equal(t, map[string]float64{
		"seconds":    10,
		"resolution": 2,
		"quality":    1.5,
	}, merged)
	assert.Equal(t, map[string]float64{"seconds": 8, "resolution": 2}, preserved)
}

func TestFixedPriceUnitControlsQuotaMultiplier(t *testing.T) {
	tests := []struct {
		name      string
		unit      string
		wantQuota float64
	}{
		{name: "per request keeps resolution but ignores duration", unit: billing_setting.FixedPriceUnitRequest, wantQuota: 90},
		{name: "per second multiplies duration and resolution", unit: billing_setting.FixedPriceUnitSecond, wantQuota: 540},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: "video-test",
				PriceData: types.PriceData{
					UsePrice:       true,
					FixedPriceUnit: test.unit,
				},
			}
			ratios, taskErr := normalizeTaskBillingRatios(info, map[string]float64{
				"seconds":    6,
				"resolution": 2,
			})

			require.Nil(t, taskErr)
			require.True(t, info.PriceData.ReplaceOtherRatios(ratios))
			assert.Equal(t, test.wantQuota, info.PriceData.ApplyOtherRatiosToFloat(45))
		})
	}
}

func TestPerSecondFixedPriceIncludesReferenceVideoMultiplier(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "hj-2.0-fast-720p",
		PriceData: types.PriceData{
			UsePrice:       true,
			FixedPriceUnit: billing_setting.FixedPriceUnitSecond,
		},
	}
	ratios, taskErr := normalizeTaskBillingRatios(info, map[string]float64{
		"seconds":         5,
		"reference_video": 2,
	})

	require.Nil(t, taskErr)
	require.True(t, info.PriceData.ReplaceOtherRatios(ratios))
	assert.Equal(t, 410.0, info.PriceData.ApplyOtherRatiosToFloat(41))
}

func TestNormalizeTaskBillingRatiosDoesNotChangeRatioBilling(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "ratio-video",
		PriceData:       types.PriceData{UsePrice: false},
	}
	want := map[string]float64{"seconds": 6, "resolution": 2}

	got, taskErr := normalizeTaskBillingRatios(info, want)

	require.Nil(t, taskErr)
	assert.Equal(t, want, got)
	assert.Empty(t, info.PriceData.FixedPriceUnit)
}
