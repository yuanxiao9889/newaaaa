package logger

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
)

func TestFormatQuota64UsesConfiguredDisplayUnit(t *testing.T) {
	generalSetting := operation_setting.GetGeneralSetting()
	originalDisplayType := generalSetting.QuotaDisplayType
	originalExchangeRate := operation_setting.USDExchangeRate
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		generalSetting.QuotaDisplayType = originalDisplayType
		operation_setting.USDExchangeRate = originalExchangeRate
		common.QuotaPerUnit = originalQuotaPerUnit
	})

	generalSetting.QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	operation_setting.USDExchangeRate = 7
	common.QuotaPerUnit = 500000

	assert.Equal(t, "¥14.000000", FormatQuota64(1000000))
}
