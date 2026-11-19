package model

import (
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type MediaRetryAttempt struct {
	ChannelID       int    `json:"channel_id"`
	Round           int    `json:"round"`
	StartedAtMS     int64  `json:"started_at_ms"`
	FailureCategory string `json:"failure_category,omitempty"`
}

// MediaRetryState is a task-scoped snapshot. A new channel or process must not
// refresh its deadline, attempt allowance or rejected-channel history.
type MediaRetryState struct {
	Version       int                    `json:"version"`
	WindowMinutes int                    `json:"window_minutes"`
	MaxAttempts   int                    `json:"max_attempts"`
	StartedAtMS   int64                  `json:"started_at_ms,omitempty"`
	DeadlineMS    int64                  `json:"deadline_ms,omitempty"`
	Attempts      int                    `json:"attempts"`
	CycleStart    int                    `json:"cycle_start"`
	Round         int                    `json:"round"`
	History       []MediaRetryAttempt    `json:"history,omitempty"`
	StopReason    string                 `json:"stop_reason,omitempty"`
	Failures      map[int]map[string]int `json:"failures,omitempty"`
	Excluded      []int                  `json:"excluded,omitempty"`
}

// ExecutionTimeoutStart leaves enough time for a submission at the end of the
// retry window to finish under the existing execution timeout. It is an absolute
// anchor, not a new retry budget: switching channels or restarting cannot move it.
// Legacy tasks and invalid snapshots retain the original timeout.
func (s *MediaRetryState) ExecutionTimeoutStart(start int64) int64 {
	if s == nil || s.Version != 1 || s.WindowMinutes < 1 || s.WindowMinutes > 120 || s.MaxAttempts < 1 || s.MaxAttempts > 10 {
		return start
	}
	anchor := time.Unix(start, 0)
	if s.StartedAtMS > 0 {
		anchor = time.UnixMilli(s.StartedAtMS)
	}
	if s.MaxAttempts > 1 {
		anchor = anchor.Add(time.Duration(s.WindowMinutes) * time.Minute)
		if s.DeadlineMS > 0 && s.DeadlineMS < anchor.UnixMilli() {
			anchor = time.UnixMilli(s.DeadlineMS)
		}
	}
	// Round up so second-resolution timeout scans never cut off the final ms.
	return max(start, anchor.Add(time.Second-time.Nanosecond).Unix())
}

func (s *MediaRetryState) Remaining(now time.Time) int {
	if s == nil || s.Version != 1 || s.MaxAttempts < 1 || s.MaxAttempts > 10 || s.WindowMinutes < 1 || s.WindowMinutes > 120 || s.Attempts < 0 {
		return 0
	}
	if s.DeadlineMS > 0 && now.UnixMilli() >= s.DeadlineMS {
		return 0
	}
	return max(0, s.MaxAttempts-s.Attempts)
}

func (s *MediaRetryState) BeginAttempt(now time.Time) bool {
	if s.Remaining(now) == 0 {
		return false
	}
	if s.StartedAtMS == 0 {
		s.StartedAtMS = now.UnixMilli()
		s.DeadlineMS = now.Add(time.Duration(s.WindowMinutes) * time.Minute).UnixMilli()
	}
	s.Attempts++
	return true
}

// RecordFailure counts categories rather than provider-specific wording. It is
// called once by the owner of a confirmed failed attempt, never on poll errors.
func (s *MediaRetryState) RecordFailure(channelID int, code, message string) string {
	category := MediaRetryFailureCategory(code)
	if category == "" {
		var structured struct {
			Code  string `json:"code"`
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if common.UnmarshalJsonStr(message, &structured) == nil {
			for _, value := range []string{structured.Error.Code, structured.Code, structured.Error.Type} {
				if category = MediaRetryFailureCategory(value); category != "" {
					break
				}
			}
		}
	}
	if category == "" {
		category = MediaRetryFailureCategory(message)
	}
	if n := len(s.History); n > 0 && s.History[n-1].ChannelID == channelID {
		s.History[n-1].FailureCategory = category
		if category == "" {
			s.History[n-1].FailureCategory = "upstream_failure"
		}
	}
	if category == "" {
		return "upstream_failure"
	}
	if s.Failures == nil {
		s.Failures = make(map[int]map[string]int)
	}
	if s.Failures[channelID] == nil {
		s.Failures[channelID] = make(map[string]int)
	}
	s.Failures[channelID][category]++
	if s.Failures[channelID][category] >= 2 && !slices.Contains(s.Excluded, channelID) {
		s.Excluded = append(s.Excluded, channelID)
	}
	return category
}

func MediaRetryFailureCategory(value string) string {
	value = strings.ToLower(value)
	for _, group := range []struct {
		category string
		terms    []string
	}{
		{"moderation", []string{"content_policy", "content.policy", "content policy", "content_filter", "moderation", "safety", "unsafe", "nsfw", "审核", "违规", "敏感", "内容政策"}},
		{"quota", []string{"insufficient_quota", "insufficient_balance", "insufficient balance", "quota_exceeded", "credit", "余额", "额度"}},
		{"parameters", []string{"invalid_request", "invalid_parameter", "invalid parameter", "invalid_argument", "validation_error", "参数"}},
	} {
		for _, term := range group.terms {
			if strings.Contains(value, term) {
				return group.category
			}
		}
	}
	return ""
}
