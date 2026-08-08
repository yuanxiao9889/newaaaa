package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSumUsedQuotaReturnsNetBilledQuota(t *testing.T) {
	truncateTables(t)

	logs := []Log{
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 100},
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 101},
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 102},
		{Type: LogTypeConsume, ModelName: "video-model", Quota: 650000, CreatedAt: 103},
		{Type: LogTypeRefund, ModelName: "video-model", Quota: 650000, CreatedAt: 104},
		// Final-accounting rollbacks have no matching consume log and must not
		// reduce billed usage.
		{Type: LogTypePreConsumeRollback, ModelName: "video-model", Quota: 650000, CreatedAt: 105},
		{Type: LogTypeConsume, ModelName: "other-model", Quota: 900000, CreatedAt: 106},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	stat, err := SumUsedQuota(LogTypeUnknown, 100, 110, "video-model", "", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 1950000, stat.Quota)
}
