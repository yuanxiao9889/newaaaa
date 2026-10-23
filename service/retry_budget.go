package service

import (
	"context"
	"fmt"
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
	Deadline time.Time
}

func NewRetryBudget(start time.Time) RetryBudget {
	policy := model.CurrentRequestPolicy()
	if !policy.RetryTimeBudgetEnabled {
		return RetryBudget{}
	}
	return RetryBudget{Deadline: start.Add(time.Duration(policy.RetryTimeBudgetSeconds) * time.Second)}
}

func (b RetryBudget) Remaining(countRemaining int, now time.Time) int {
	if b.Deadline.IsZero() {
		return countRemaining
	}
	if !now.Before(b.Deadline) {
		return 0
	}
	return max(1, countRemaining)
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
	if err != nil || channel != nil || len(identity.ExcludedChannelIDs) == 0 {
		return channel, group, err
	}
	// Avoid a tight retry loop when all upstreams reject immediately. The
	// deadline also bounds this wait, and cancellation wakes it immediately.
	wait := time.Second
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
	if cycle.Budget.Remaining(1, time.Now()) == 0 {
		return nil, group, context.DeadlineExceeded
	}
	cycle.Start = len(cycle.History)
	copyConstraints.Filters[filterIndex].ExcludedChannelIDs = nil
	selection.SetRetry(0)
	common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 0)
	return cacheGetRandomSatisfiedChannel(&selection)
}
