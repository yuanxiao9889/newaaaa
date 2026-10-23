package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const TransferOrigin = "https://www.oopii.cc"
const TransferBridge = "https://bridge.opics.top"

var ErrTransferPolicyConflict = errors.New("bridge policy version conflict")

// TransferPolicy is the versioned client contract, not a claim that a bridge
// storage agent has applied it. Existing operations retain their snapshot.
type TransferPolicy struct {
	Enabled                      bool    `json:"enabled"`
	OriginBaseURL                string  `json:"origin_base_url"`
	BridgeBaseURL                string  `json:"bridge_base_url"`
	CooldownFailureCount         int     `json:"cooldown_failure_count"`
	CooldownSeconds              int     `json:"cooldown_seconds"`
	DownloadHeaderTimeoutSeconds int     `json:"download_header_timeout_seconds"`
	SlowObservationSeconds       int     `json:"slow_observation_seconds"`
	SlowWindowSeconds            int     `json:"slow_window_seconds"`
	SlowWindowCount              int     `json:"slow_window_count"`
	SlowSpeedBytesPerSecond      int     `json:"slow_speed_bytes_per_second"`
	SlowRemainingSeconds         int     `json:"slow_remaining_seconds"`
	NearCompleteSeconds          int     `json:"near_complete_seconds"`
	StallPrepareSeconds          int     `json:"stall_prepare_seconds"`
	StallFailSeconds             int     `json:"stall_fail_seconds"`
	UnknownSizeProbeAfterSeconds int     `json:"unknown_size_probe_after_seconds"`
	ProbeMaxBytes                int     `json:"probe_max_bytes"`
	ProbeMaxSeconds              int     `json:"probe_max_seconds"`
	ProbeMinBytes                int     `json:"probe_min_bytes"`
	ProbeLimit                   int     `json:"probe_limit"`
	ProcessProbeLimit            int     `json:"process_probe_limit"`
	MinSpeedRatio                float64 `json:"min_speed_ratio"`
	MinSavingsSeconds            int     `json:"min_savings_seconds"`
	MinSavingsRatio              float64 `json:"min_savings_ratio"`
	MaxRestartBytes              int     `json:"max_restart_bytes"`
	MaxRestartRatio              float64 `json:"max_restart_ratio"`
	MaxSpeedSwitches             int     `json:"max_speed_switches"`
	MaxFailureRecoveries         int     `json:"max_failure_recoveries"`
	MaxPrimaryRequests           int     `json:"max_primary_requests"`
}

type TransferFieldSpec struct {
	Key          string  `json:"key"`
	Group        string  `json:"group"`
	Unit         string  `json:"unit"`
	Min          float64 `json:"min"`
	Max          float64 `json:"max"`
	DefaultValue float64 `json:"default_value"`
}

type TransferPolicyRevision struct {
	Version   int64          `json:"version"`
	ChangedAt int64          `json:"changed_at"`
	ChangedBy int            `json:"changed_by"`
	Reason    string         `json:"reason"`
	Policy    TransferPolicy `json:"policy"`
}

type TransferPolicyRecord struct {
	ID      int    `gorm:"primaryKey;autoIncrement:false"`
	Version int64  `gorm:"not null"`
	Policy  string `gorm:"type:text;not null"`
	History string `gorm:"type:text;not null"`
}

type TransferPolicySnapshot struct {
	Version int64                    `json:"version"`
	Policy  TransferPolicy           `json:"policy"`
	History []TransferPolicyRevision `json:"history"`
}

func DefaultTransferPolicy() TransferPolicy {
	return TransferPolicy{
		Enabled: true, OriginBaseURL: TransferOrigin, BridgeBaseURL: TransferBridge,
		CooldownFailureCount: 2, CooldownSeconds: 60, DownloadHeaderTimeoutSeconds: 10,
		SlowObservationSeconds: 12, SlowWindowSeconds: 5, SlowWindowCount: 2,
		SlowSpeedBytesPerSecond: 512 << 10, SlowRemainingSeconds: 30, NearCompleteSeconds: 10,
		StallPrepareSeconds: 8, StallFailSeconds: 30, UnknownSizeProbeAfterSeconds: 20,
		ProbeMaxBytes: 4 << 20, ProbeMaxSeconds: 8, ProbeMinBytes: 512 << 10,
		ProbeLimit: 2, ProcessProbeLimit: 2, MinSpeedRatio: 2, MinSavingsSeconds: 10,
		MinSavingsRatio: .3, MaxRestartBytes: 32 << 20, MaxRestartRatio: .25,
		MaxSpeedSwitches: 1, MaxFailureRecoveries: 2, MaxPrimaryRequests: 4,
	}
}

func TransferPolicyFields() []TransferFieldSpec {
	fields := []TransferFieldSpec{
		{"cooldown_failure_count", "routing", "count", 1, 5, 0},
		{"cooldown_seconds", "routing", "seconds", 10, 600, 0},
		{"download_header_timeout_seconds", "routing", "seconds", 3, 30, 0},
		{"slow_observation_seconds", "slow", "seconds", 10, 60, 0},
		{"slow_window_seconds", "slow", "seconds", 3, 15, 0},
		{"slow_window_count", "slow", "count", 2, 2, 0},
		{"slow_speed_bytes_per_second", "slow", "bytes_per_second", 64 << 10, 8 << 20, 0},
		{"slow_remaining_seconds", "slow", "seconds", 15, 300, 0},
		{"near_complete_seconds", "slow", "seconds", 1, 30, 0},
		{"stall_prepare_seconds", "slow", "seconds", 3, 30, 0},
		{"stall_fail_seconds", "slow", "seconds", 10, 120, 0},
		{"unknown_size_probe_after_seconds", "slow", "seconds", 10, 120, 0},
		{"probe_max_bytes", "probe", "bytes", 512 << 10, 8 << 20, 0},
		{"probe_max_seconds", "probe", "seconds", 2, 15, 0},
		{"probe_min_bytes", "probe", "bytes", 64 << 10, 4 << 20, 0},
		{"probe_limit", "budget", "count", 0, 2, 0},
		{"process_probe_limit", "budget", "count", 1, 2, 0},
		{"min_speed_ratio", "probe", "ratio", 1.5, 5, 0},
		{"min_savings_seconds", "probe", "seconds", 5, 120, 0},
		{"min_savings_ratio", "probe", "ratio", .1, .9, 0},
		{"max_restart_bytes", "budget", "bytes", 0, 32 << 20, 0},
		{"max_restart_ratio", "budget", "ratio", 0, .25, 0},
		{"max_speed_switches", "budget", "count", 0, 1, 0},
		{"max_failure_recoveries", "budget", "count", 0, 2, 0},
		{"max_primary_requests", "budget", "count", 1, 4, 0},
	}
	data, _ := common.Marshal(DefaultTransferPolicy())
	var defaults map[string]common.RawMessage
	_ = common.Unmarshal(data, &defaults)
	for i := range fields {
		_ = common.Unmarshal(defaults[fields[i].Key], &fields[i].DefaultValue)
	}
	return fields
}

func ValidateTransferPolicy(policy TransferPolicy) error {
	if policy.OriginBaseURL != TransferOrigin || policy.BridgeBaseURL != TransferBridge {
		return errors.New("bridge and origin addresses require a separate trusted deployment")
	}
	data, err := common.Marshal(policy)
	if err != nil {
		return err
	}
	var values map[string]common.RawMessage
	if err = common.Unmarshal(data, &values); err != nil {
		return err
	}
	for _, spec := range TransferPolicyFields() {
		var value float64
		if err = common.Unmarshal(values[spec.Key], &value); err != nil ||
			math.IsNaN(value) || math.IsInf(value, 0) || value < spec.Min || value > spec.Max {
			return fmt.Errorf("%s must be between %g and %g", spec.Key, spec.Min, spec.Max)
		}
	}
	if policy.SlowObservationSeconds < policy.SlowWindowSeconds*policy.SlowWindowCount ||
		policy.StallPrepareSeconds >= policy.StallFailSeconds ||
		policy.NearCompleteSeconds >= policy.SlowRemainingSeconds ||
		policy.ProbeMinBytes > policy.ProbeMaxBytes ||
		policy.UnknownSizeProbeAfterSeconds < policy.SlowObservationSeconds {
		return errors.New("observation, stall, probe or remaining-time limits are inconsistent")
	}
	return nil
}

func LoadTransferPolicy() (*TransferPolicySnapshot, error) {
	if DB == nil {
		return nil, errors.New("database unavailable")
	}
	var record TransferPolicyRecord
	err := DB.First(&record, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		policy, _ := common.Marshal(DefaultTransferPolicy())
		record = TransferPolicyRecord{ID: 1, Version: 1, Policy: string(policy), History: "[]"}
		if err = DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
			return nil, err
		}
		err = DB.First(&record, 1).Error
	}
	if err != nil {
		return nil, err
	}
	result := &TransferPolicySnapshot{Version: record.Version}
	if err = common.UnmarshalJsonStr(record.Policy, &result.Policy); err != nil {
		return nil, err
	}
	if err = ValidateTransferPolicy(result.Policy); err != nil {
		return nil, err
	}
	if err = common.UnmarshalJsonStr(record.History, &result.History); err != nil {
		return nil, err
	}
	return result, nil
}

func SaveTransferPolicy(baseVersion int64, policy TransferPolicy, actor int, reason string) (*TransferPolicySnapshot, error) {
	reason = strings.TrimSpace(reason)
	if actor <= 0 || baseVersion < 1 || reason == "" || utf8.RuneCountInString(reason) > 500 {
		return nil, errors.New("valid version, actor and change reason (1-500 characters) are required")
	}
	if err := ValidateTransferPolicy(policy); err != nil {
		return nil, err
	}
	current, err := LoadTransferPolicy()
	if err != nil {
		return nil, err
	}
	if current.Version != baseVersion {
		return nil, ErrTransferPolicyConflict
	}
	next := &TransferPolicySnapshot{Version: baseVersion + 1, Policy: policy, History: current.History}
	if len(next.History) == 0 {
		next.History = append(next.History, TransferPolicyRevision{
			Version: current.Version, ChangedAt: 0, ChangedBy: 0,
			Reason: "Initial policy", Policy: current.Policy,
		})
	}
	next.History = append(next.History, TransferPolicyRevision{
		Version: next.Version, ChangedAt: time.Now().Unix(), ChangedBy: actor, Reason: reason, Policy: policy,
	})
	if len(next.History) > 20 {
		next.History = next.History[len(next.History)-20:]
	}
	rawPolicy, _ := common.Marshal(policy)
	rawHistory, _ := common.Marshal(next.History)
	update := DB.Model(&TransferPolicyRecord{}).Where("id = ? AND version = ?", 1, baseVersion).
		Updates(map[string]any{"version": next.Version, "policy": string(rawPolicy), "history": string(rawHistory)})
	if update.Error != nil {
		return nil, update.Error
	}
	if update.RowsAffected != 1 {
		return nil, ErrTransferPolicyConflict
	}
	return next, nil
}
