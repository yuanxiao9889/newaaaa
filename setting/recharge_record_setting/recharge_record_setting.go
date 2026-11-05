package recharge_record_setting

import (
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	enabledEnv      = "RECHARGE_RECORDS_ENABLED"
	writeEnabledEnv = "RECHARGE_RECORD_INVOICE_WRITE_ENABLED"
	queryTimeoutEnv = "RECHARGE_RECORD_QUERY_TIMEOUT_SECONDS"
	defaultTimeout  = 8
	maxQueryTimeout = 30
)

// Enabled is the read-side feature flag. It permits the migration to ship with
// query-only behavior before invoice writes are exposed.
func Enabled() bool {
	return common.GetEnvOrDefaultBool(enabledEnv, true)
}

// WritesEnabled is the independent write rollout flag. Closing it leaves the
// invoice and history tables intact and only rejects new marking mutations.
func WritesEnabled() bool {
	return Enabled() && common.GetEnvOrDefaultBool(writeEnabledEnv, false)
}

func ImportEnabled() bool {
	return Enabled() && common.GetEnvOrDefaultBool("RECHARGE_RECORD_IMPORT_ENABLED", false)
}

func QueryTimeout() time.Duration {
	seconds := common.GetEnvOrDefault(queryTimeoutEnv, defaultTimeout)
	if seconds < 1 {
		seconds = defaultTimeout
	}
	if seconds > maxQueryTimeout {
		seconds = maxQueryTimeout
	}
	return time.Duration(seconds) * time.Second
}
