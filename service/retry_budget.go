package service

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// RetryBudget limits new submissions, not the lifetime of accepted upstream
// work. In-flight requests keep their existing timeout and cancellation rules.
type RetryBudget struct {
	Media      *model.MediaRetryState
	Deadline   time.Time
	MaxRetries int
}

func NewRetryBudget(start time.Time) RetryBudget {
	policy := model.CurrentRequestPolicy()
	if !policy.RetryTimeBudgetEnabled {
		return RetryBudget{}
	}
	return RetryBudget{Deadline: start.Add(time.Duration(policy.RetryTimeBudgetSeconds) * time.Second), MaxRetries: policy.RetryTimeBudgetMaxRetries}
}

// Remaining uses the complete attempt history, including the initial submission,
// so changing channels, starting another round or resuming a task cannot reset
// the configured retry limit in time-budget mode.
func (b RetryBudget) Remaining(countRemaining, attemptsMade int, now time.Time) int {
	if b.Media != nil {
		return b.Media.Remaining(now)
	}
	if b.Deadline.IsZero() {
		return countRemaining
	}
	maxTimedRetries := b.MaxRetries
	if !now.Before(b.Deadline) || attemptsMade >= maxTimedRetries+1 {
		return 0
	}
	return min(max(1, countRemaining), maxTimedRetries+1-max(0, attemptsMade))
}

// ChannelRetryCycle excludes only this round's attempts. Static constraints
// (including explicit exclusions), enabled status and health gates still apply.
type ChannelRetryCycle struct {
	Enabled bool
	History []string
	Start   int
	Budget  RetryBudget
}

func (cycle *ChannelRetryCycle) Select(param *RetryParam) (*model.Channel, string, error) {
	c := param.Ctx
	constraints := GetChannelConstraints(c)
	if _, pinned, _ := constraints.ResolvedPin(); pinned || ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return cacheGetRandomSatisfiedChannel(param)
	}
	if cycle.Start < 0 || cycle.Start > len(cycle.History) {
		return nil, param.TokenGroup, fmt.Errorf("invalid channel retry round")
	}
	// A temporary copy ensures resetting a round never erases caller filters.
	copyConstraints := *constraints
	copyConstraints.Filters = append([]dto.ChannelFilter(nil), constraints.Filters...)
	if cycle.Budget.Media != nil {
		copyConstraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterChannelIdentity, ExcludedChannelIDs: slices.Clone(cycle.Budget.Media.Excluded)})
		used := c.GetStringSlice("use_channel")
		c.Set("use_channel", cycle.History[cycle.Start:])
		defer c.Set("use_channel", used)
	}
	common.SetContextKey(c, constant.ContextKeyChannelConstraints, &copyConstraints)
	defer common.SetContextKey(c, constant.ContextKeyChannelConstraints, constraints)
	identity := dto.ChannelFilter{Kind: dto.FilterChannelIdentity}
	for _, used := range cycle.History[cycle.Start:] {
		id, err := strconv.Atoi(used)
		if err != nil {
			return nil, param.TokenGroup, fmt.Errorf("invalid retry channel history")
		}
		identity.ExcludedChannelIDs = append(identity.ExcludedChannelIDs, id)
	}
	filterIndex := len(copyConstraints.Filters)
	copyConstraints.AddFilter(identity)
	// Exhaust every eligible peer at each priority before lowering priority.
	selection := *param
	selection.Retry = common.GetPointer(0)
	common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 0)
	channel, group, err := cacheGetRandomSatisfiedChannel(&selection)
	if err == nil && channel == nil && cycle.Budget.Media != nil {
		c.Set("media_retry_allow_cooling", true)
		channel, group, err = cacheGetRandomSatisfiedChannel(&selection)
		c.Set("media_retry_allow_cooling", false)
	}
	if err != nil || channel != nil || len(identity.ExcludedChannelIDs) == 0 {
		return channel, group, err
	}
	// Avoid a tight retry loop when all upstreams reject immediately. The
	// deadline also bounds this wait, and cancellation wakes it immediately.
	wait := time.Second
	if state := cycle.Budget.Media; state != nil && state.DeadlineMS > 0 {
		wait = min(wait, time.Until(time.UnixMilli(state.DeadlineMS)))
	}
	if !cycle.Budget.Deadline.IsZero() {
		wait = min(wait, time.Until(cycle.Budget.Deadline))
	}
	if wait <= 0 {
		return nil, group, context.DeadlineExceeded
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-c.Request.Context().Done():
		return nil, group, c.Request.Context().Err()
	case <-timer.C:
	}
	if cycle.Budget.Remaining(1, len(cycle.History), time.Now()) == 0 {
		return nil, group, context.DeadlineExceeded
	}
	cycle.Start = len(cycle.History)
	if cycle.Budget.Media != nil {
		cycle.Budget.Media.CycleStart = cycle.Start
		cycle.Budget.Media.Round++
		c.Set("use_channel", []string(nil))
	}
	copyConstraints.Filters[filterIndex].ExcludedChannelIDs = nil
	selection.SetRetry(0)
	common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 0)
	channel, group, err = cacheGetRandomSatisfiedChannel(&selection)
	if err == nil && channel == nil && cycle.Budget.Media != nil {
		c.Set("media_retry_allow_cooling", true)
		defer c.Set("media_retry_allow_cooling", false)
		return cacheGetRandomSatisfiedChannel(&selection)
	}
	return channel, group, err
}
