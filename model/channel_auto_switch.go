package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ChannelAutoSwitchHealthy = "healthy"
	ChannelAutoSwitchCooling = "cooling"
	ChannelAutoSwitchProbing = "probing"

	channelAutoSwitchWindow       = 10 * time.Minute
	channelAutoSwitchProbeTimeout = 25 * time.Hour
)

type ChannelAutoSwitchOutcome string

const (
	ChannelAutoSwitchSuccess  ChannelAutoSwitchOutcome = "success"
	ChannelAutoSwitchFailure  ChannelAutoSwitchOutcome = "failure"
	ChannelAutoSwitchNeutral  ChannelAutoSwitchOutcome = "neutral"
	channelAutoSwitchReleased ChannelAutoSwitchOutcome = "released"
)

// ChannelAutoSwitchState supplements, but never rewrites, a channel's manual priority.
// The key includes the original client model and request protocol, not upstream aliases.
type ChannelAutoSwitchState struct {
	StateKey            string `json:"state_key" gorm:"primaryKey;size:64"`
	ChannelID           int    `json:"channel_id" gorm:"index"`
	Group               string `json:"group" gorm:"column:routing_group;type:text"`
	Model               string `json:"model" gorm:"type:text"`
	Protocol            string `json:"protocol" gorm:"type:text"`
	Status              string `json:"status" gorm:"size:16"`
	Generation          int64  `json:"generation"`
	Revision            int64  `json:"-"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	FirstFailureAt      int64  `json:"first_failure_at"`
	WindowTotal         int64  `json:"window_total"`
	WindowFailures      int64  `json:"window_failures"`
	CooldownSeconds     int64  `json:"cooldown_seconds"`
	CooldownUntil       int64  `json:"cooldown_until"`
	ProbeAttemptID      string `json:"-" gorm:"size:32"`
	ProbeDeadline       int64  `json:"probe_deadline"`
	ProbeSuccesses      int    `json:"probe_successes"`
	LastReason          string `json:"last_reason" gorm:"type:text"`
	UpdatedAt           int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
}

// CanProbe also allows the selector to expire an orphaned probe under the state lock.
// Expiry only schedules another cooldown; it never immediately admits a replacement.
func (s ChannelAutoSwitchState) CanProbe(now time.Time) bool {
	if s.Status == ChannelAutoSwitchHealthy {
		return false
	}
	if s.ProbeAttemptID != "" {
		return s.ProbeDeadline > 0 && s.ProbeDeadline <= now.Unix()
	}
	return s.CooldownUntil <= now.Unix()
}

// ChannelAutoSwitchAttempt makes a terminal observation idempotent across pollers
// and restarts. Missing or already completed tickets are never recreated by Record.
type ChannelAutoSwitchAttempt struct {
	ID                string `gorm:"primaryKey;size:32"`
	StateKey          string `gorm:"size:64;index:idx_auto_switch_window,priority:1"`
	Generation        int64  `gorm:"index:idx_auto_switch_window,priority:2"`
	Probe             bool
	StartedAt         int64
	CompletedAt       int64 `gorm:"index;index:idx_auto_switch_window,priority:3"`
	CompletedRevision int64
	Outcome           ChannelAutoSwitchOutcome `gorm:"size:16"`
}

// ChannelAutoSwitchTicket must be retained with an accepted asynchronous task until
// its actual final result. No secrets or upstream request bodies are stored here.
type ChannelAutoSwitchTicket struct {
	ID         string `json:"id"`
	StateKey   string `json:"state_key"`
	Generation int64  `json:"generation"`
	Probe      bool   `json:"probe"`
	ChannelID  int    `json:"channel_id"`
	Group      string `json:"group"`
	Model      string `json:"model"`
	Protocol   string `json:"protocol"`
}

func GetChannelAutoSwitchStates(channelIDs []int, group, model, protocol string) (map[int]ChannelAutoSwitchState, error) {
	states := make(map[int]ChannelAutoSwitchState)
	if len(channelIDs) == 0 {
		return states, nil
	}
	keys := make([]string, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		key, err := channelAutoSwitchStateKey(channelID, group, model, protocol)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	var rows []ChannelAutoSwitchState
	// Query the scope digest so MySQL's case/accent-insensitive text collation
	// cannot merge distinct client models or groups.
	err := DB.Where("state_key IN ?", keys).Find(&rows).Error
	for _, row := range rows {
		states[row.ChannelID] = row
	}
	return states, err
}

func ListChannelAutoSwitchStates(channelID int) ([]ChannelAutoSwitchState, error) {
	rows := make([]ChannelAutoSwitchState, 0)
	err := DB.Where("channel_id = ?", channelID).Order("updated_at DESC").Find(&rows).Error
	return rows, err
}

// BeginChannelAutoSwitchAttempt atomically reserves the final chosen candidate.
// A nil ticket means it is cooling or another node owns the only recovery probe.
func BeginChannelAutoSwitchAttempt(channelID int, group, model, protocol string, now time.Time) (*ChannelAutoSwitchTicket, error) {
	key, err := channelAutoSwitchStateKey(channelID, group, model, protocol)
	if err != nil {
		return nil, err
	}
	state := ChannelAutoSwitchState{
		StateKey: key, ChannelID: channelID,
		Group: group, Model: model, Protocol: protocol,
		Status: ChannelAutoSwitchHealthy, Generation: 1, UpdatedAt: now.Unix(),
	}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return nil, err
	}
	var ticket *ChannelAutoSwitchTicket
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockChannelAutoSwitchState(tx, state.StateKey, &state); err != nil {
			return err
		}
		if state.Status != ChannelAutoSwitchHealthy {
			if state.ProbeAttemptID != "" {
				if state.ProbeDeadline > 0 && state.ProbeDeadline <= now.Unix() {
					state.coolDown(now, "probe_expired")
					return tx.Save(&state).Error
				}
				return nil
			}
			if state.CooldownUntil > now.Unix() {
				return nil
			}
		}
		attempt := ChannelAutoSwitchAttempt{
			ID: common.GetUUID(), StateKey: state.StateKey, Generation: state.Generation,
			Probe: state.Status != ChannelAutoSwitchHealthy, StartedAt: now.Unix(),
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		if attempt.Probe {
			state.Status = ChannelAutoSwitchProbing
			state.ProbeAttemptID = attempt.ID
			state.ProbeDeadline = now.Add(channelAutoSwitchProbeTimeout).Unix()
			state.UpdatedAt = now.Unix()
			if err := tx.Save(&state).Error; err != nil {
				return err
			}
		}
		ticket = &ChannelAutoSwitchTicket{
			ID: attempt.ID, StateKey: state.StateKey, Generation: state.Generation,
			Probe: attempt.Probe, ChannelID: channelID, Group: group, Model: model, Protocol: protocol,
		}
		return nil
	})
	return ticket, err
}

func channelAutoSwitchStateKey(channelID int, group, model, protocol string) (string, error) {
	encodedScope, err := common.Marshal([]any{channelID, group, model, protocol})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encodedScope)
	return hex.EncodeToString(digest[:]), nil
}

// lockChannelAutoSwitchState acquires a database write lock before reading. Unlike
// SELECT FOR UPDATE this also serializes SQLite transactions, avoiding read-to-write
// upgrade races. All attempt mutations take this same state lock first.
func lockChannelAutoSwitchState(tx *gorm.DB, key string, state *ChannelAutoSwitchState) error {
	result := tx.Model(&ChannelAutoSwitchState{}).Where("state_key = ?", key).
		UpdateColumn("revision", gorm.Expr("revision + 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return tx.Where("state_key = ?", key).First(state).Error
}

// coolDown fences all attempts from before the transition, including late successes.
func (s *ChannelAutoSwitchState) coolDown(now time.Time, reason string) {
	s.Status = ChannelAutoSwitchCooling
	s.Generation++
	s.CooldownSeconds = min(max(s.CooldownSeconds*2, int64((5*time.Minute)/time.Second)), int64((30*time.Minute)/time.Second))
	s.CooldownUntil = now.Unix() + s.CooldownSeconds
	s.ProbeAttemptID = ""
	s.ProbeDeadline = 0
	s.ProbeSuccesses = 0
	s.ConsecutiveFailures = 0
	s.FirstFailureAt = 0
	s.LastReason = reason
	s.UpdatedAt = now.Unix()
}

func RecordChannelAutoSwitchResult(ticket ChannelAutoSwitchTicket, outcome ChannelAutoSwitchOutcome, reason string, now time.Time) error {
	if outcome != ChannelAutoSwitchSuccess && outcome != ChannelAutoSwitchFailure && outcome != ChannelAutoSwitchNeutral {
		return errors.New("invalid channel auto switch outcome")
	}
	return finishChannelAutoSwitchAttempt(ticket, outcome, reason, now)
}

// ReleaseChannelAutoSwitchAttempt is only for a request definitely rejected locally
// before sending. It gives the probe slot back without treating it as provider health.
func ReleaseChannelAutoSwitchAttempt(ticket ChannelAutoSwitchTicket, now time.Time) error {
	return finishChannelAutoSwitchAttempt(ticket, channelAutoSwitchReleased, "", now)
}

func finishChannelAutoSwitchAttempt(ticket ChannelAutoSwitchTicket, outcome ChannelAutoSwitchOutcome, reason string, now time.Time) error {
	if ticket.ID == "" || ticket.StateKey == "" {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var state ChannelAutoSwitchState
		if err := lockChannelAutoSwitchState(tx, ticket.StateKey, &state); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var attempt ChannelAutoSwitchAttempt
		found := tx.Where("id = ? AND state_key = ? AND generation = ?", ticket.ID, ticket.StateKey, ticket.Generation).Find(&attempt)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected == 0 || attempt.CompletedAt != 0 {
			return nil
		}
		if err := tx.Model(&attempt).Updates(map[string]any{"completed_at": now.Unix(), "completed_revision": state.Revision, "outcome": outcome}).Error; err != nil {
			return err
		}
		if attempt.Generation != state.Generation {
			return nil
		}
		state.UpdatedAt = now.Unix()
		if attempt.Probe {
			if state.ProbeAttemptID != attempt.ID {
				return nil
			}
			if state.ProbeDeadline > 0 && state.ProbeDeadline <= now.Unix() {
				state.coolDown(now, "probe_expired")
				return tx.Save(&state).Error
			}
			state.ProbeAttemptID = ""
			state.ProbeDeadline = 0
			switch outcome {
			case ChannelAutoSwitchSuccess:
				state.ProbeSuccesses++
				if state.ProbeSuccesses >= 3 {
					state.Status = ChannelAutoSwitchHealthy
					state.Generation++
					state.CooldownSeconds = 0
					state.CooldownUntil = 0
					state.WindowTotal = 0
					state.WindowFailures = 0
					state.LastReason = ""
				}
			case ChannelAutoSwitchFailure, ChannelAutoSwitchNeutral:
				state.coolDown(now, reason)
			}
			return tx.Save(&state).Error
		}
		if outcome == ChannelAutoSwitchNeutral || outcome == channelAutoSwitchReleased {
			return nil
		}
		window := tx.Model(&ChannelAutoSwitchAttempt{}).
			Where("state_key = ? AND generation = ? AND completed_at >= ? AND outcome IN ?", state.StateKey, state.Generation, now.Add(-channelAutoSwitchWindow).Unix(), []ChannelAutoSwitchOutcome{ChannelAutoSwitchSuccess, ChannelAutoSwitchFailure})
		state.ConsecutiveFailures = 0
		state.FirstFailureAt = 0
		if outcome == ChannelAutoSwitchFailure {
			state.LastReason = reason
			var recent []ChannelAutoSwitchAttempt
			// The durable revision orders simultaneous completions consistently across
			// nodes; timestamps alone cannot order several results in one second.
			if err := window.Session(&gorm.Session{}).Order("completed_revision DESC").Limit(3).Find(&recent).Error; err != nil {
				return err
			}
			for _, previous := range recent {
				if previous.Outcome != ChannelAutoSwitchFailure {
					break
				}
				state.ConsecutiveFailures++
				state.FirstFailureAt = previous.CompletedAt
			}
		}
		if err := window.Session(&gorm.Session{}).Count(&state.WindowTotal).Error; err != nil {
			return err
		}
		if err := window.Session(&gorm.Session{}).Where("outcome = ?", ChannelAutoSwitchFailure).Count(&state.WindowFailures).Error; err != nil {
			return err
		}
		if state.ConsecutiveFailures >= 3 || state.WindowTotal >= 10 && state.WindowFailures*2 >= state.WindowTotal {
			state.coolDown(now, state.LastReason)
		}
		return tx.Save(&state).Error
	})
}

// ResetChannelAutoSwitch preserves the generation fence so a saved task ticket
// from a previous configuration cannot restore or degrade the new configuration.
func ResetChannelAutoSwitch(channelID int) error {
	return resetChannelAutoSwitch(DB, channelID)
}

func resetChannelAutoSwitch(tx *gorm.DB, channelID int) error {
	return tx.Model(&ChannelAutoSwitchState{}).Where("channel_id = ?", channelID).Updates(map[string]any{
		"generation": gorm.Expr("generation + 1"), "revision": gorm.Expr("revision + 1"),
		"status": ChannelAutoSwitchHealthy, "consecutive_failures": 0, "first_failure_at": 0,
		"window_total": 0, "window_failures": 0, "cooldown_seconds": 0, "cooldown_until": 0,
		"probe_attempt_id": "", "probe_deadline": 0, "probe_successes": 0, "last_reason": "", "updated_at": time.Now().Unix(),
	}).Error
}

// PruneChannelAutoSwitchAttempts removes completed observations outside the health
// window and orphaned pending observations beyond the maximum probe lifetime.
// Missing tickets remain ignored, so pruning cannot enable replay.
func PruneChannelAutoSwitchAttempts(before time.Time) error {
	cutoff := min(before.Unix(), time.Now().Add(-channelAutoSwitchWindow).Unix())
	pendingCutoff := min(cutoff, time.Now().Add(-channelAutoSwitchProbeTimeout).Unix())
	return DB.Where("(completed_at > 0 AND completed_at < ?) OR (completed_at = 0 AND started_at < ?)", cutoff, pendingCutoff).Delete(&ChannelAutoSwitchAttempt{}).Error
}
