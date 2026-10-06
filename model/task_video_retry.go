package model

import (
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"gorm.io/gorm"
)

// VideoTaskRetryState retains the original request only while an accepted video
// task can be retried. A persisted Submitting marker forbids replay after an
// interrupted POST whose outcome is unknown. Revision fences stale polls even
// when a replacement task has the same status as its predecessor.
type VideoTaskRetryState struct {
	Body            []byte                  `json:"body,omitempty"`
	ContentType     string                  `json:"content_type,omitempty"`
	Accept          string                  `json:"accept,omitempty"`
	Method          string                  `json:"method,omitempty"`
	Path            string                  `json:"path,omitempty"`
	Query           string                  `json:"query,omitempty"`
	UserGroup       string                  `json:"user_group,omitempty"`
	NativePluginKey string                  `json:"native_plugin_key,omitempty"`
	NativeRoutePath string                  `json:"native_route_path,omitempty"`
	Params          map[string]string       `json:"params,omitempty"`
	LegacyPluginKey string                  `json:"legacy_plugin_key,omitempty"`
	Action          string                  `json:"action,omitempty"`
	Constraints     dto.ChannelConstraints  `json:"constraints"`
	AcceptedAt      int64                   `json:"accepted_at,omitempty"`
	Attempt         int                     `json:"attempt,omitempty"`
	Submitting      bool                    `json:"submitting,omitempty"`
	SubmitStartedAt int64                   `json:"submit_started_at,omitempty"`
	Revision        int64                   `json:"revision,omitempty"`
	Ready           bool                    `json:"ready,omitempty"`
	Details         []AsyncImageRetryDetail `json:"details,omitempty"`
	QuotaSaturation map[string]any          `json:"quota_saturation,omitempty"`
}

// RecordQuotaClamp retains the first saturation marker until final billing.
// Non-finite inputs need a string representation because JSON cannot encode
// them as numbers and rejecting the task write would lose its reservation.
func (s *VideoTaskRetryState) RecordQuotaClamp(clamp *common.QuotaClamp) {
	if s == nil || clamp == nil || len(s.QuotaSaturation) > 0 {
		return
	}
	s.QuotaSaturation = clamp.AuditMap()
	if math.IsNaN(clamp.Original) || math.IsInf(clamp.Original, 0) {
		s.QuotaSaturation["original"] = strconv.FormatFloat(clamp.Original, 'g', -1, 64)
	}
}

func (t *Task) updateVideoTaskWithStatus(fromStatus TaskStatus) (bool, error) {
	expected := t.PrivateData.VideoRetry
	won := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current Task
		if err := lockForUpdate(tx).First(&current, t.ID).Error; err != nil {
			return err
		}
		currentRetry := current.PrivateData.VideoRetry
		if current.Status != fromStatus || currentRetry == nil || currentRetry.Revision != expected.Revision {
			return nil
		}
		next := *expected
		next.Revision++
		if t.Status == TaskStatusSuccess || t.Status == TaskStatusFailure {
			next.Body = nil
			next.Submitting = false
		}
		t.PrivateData.VideoRetry = &next
		if err := tx.Model(t).Select("*").Updates(t).Error; err != nil {
			return err
		}
		won = true
		return nil
	})
	if err != nil || !won {
		t.PrivateData.VideoRetry = expected
	}
	return won && err == nil, err
}
