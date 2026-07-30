package billing_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFixedPriceUnitJSON(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid units", value: `{"video-a":"request","video-b":"second"}`},
		{name: "empty object", value: `{}`},
		{name: "invalid json", value: `{`, wantErr: true},
		{name: "null is not an object", value: `null`, wantErr: true},
		{name: "empty model name", value: `{"":"second"}`, wantErr: true},
		{name: "unknown unit", value: `{"video":"minute"}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateFixedPriceUnitJSON(test.value)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestResolveFixedPriceUnitPrefersExplicitConfiguration(t *testing.T) {
	original := billingSetting.FixedPriceUnit
	billingSetting.FixedPriceUnit = map[string]string{
		"explicit-request": FixedPriceUnitRequest,
		"explicit-second":  FixedPriceUnitSecond,
	}
	t.Cleanup(func() {
		billingSetting.FixedPriceUnit = original
	})

	assert.Equal(t, FixedPriceUnitRequest, ResolveFixedPriceUnit("explicit-request", true))
	assert.Equal(t, FixedPriceUnitSecond, ResolveFixedPriceUnit("explicit-second", false))
	assert.Equal(t, FixedPriceUnitSecond, ResolveFixedPriceUnit("legacy-video", true))
	assert.Equal(t, FixedPriceUnitRequest, ResolveFixedPriceUnit("new-fixed-model", false))
}
