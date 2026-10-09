package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const (
	VideoTaskRetryWindowSeconds        int64 = 70
	VideoTaskRetrySubmitTimeoutSeconds int64 = 600
)

// RetryVideoTaskFunc is registered by the controller to avoid a dependency
// cycle. It runs only for confirmed upstream FAILURE results. A handled result
// means this poll must not persist or settle its previous upstream attempt.
var RetryVideoTaskFunc func(context.Context, *model.Task, *relaycommon.TaskInfo) (bool, error)

// prepareVideoTaskForPolling prevents polling the previous provider while a
// replacement POST is in flight and removes request snapshots after the retry
// window closes. An interrupted submission is never replayed automatically.
func prepareVideoTaskForPolling(ctx context.Context, task *model.Task) (bool, error) {
	retry := task.PrivateData.VideoRetry
	if retry == nil {
		return true, nil
	}
	now := time.Now().Unix()
	if retry.Submitting {
		startedAt := retry.SubmitStartedAt
		if startedAt == 0 {
			startedAt = task.UpdatedAt
		}
		if startedAt > 0 && now-startedAt < VideoTaskRetrySubmitTimeoutSeconds {
			return false, nil
		}
		return false, failVideoTaskRetry(ctx, task, "video channel resubmission outcome is unknown; automatic resubmission stopped")
	}
	// Initial billing settlement runs just after insertion. Give that owner
	// time to enable retry before polling can finalize/refund its reservation.
	// If it was interrupted, eventually poll the original accepted job without
	// retry so a missing readiness write cannot strand the task indefinitely.
	if !retry.Ready && retry.AcceptedAt > 0 && now-retry.AcceptedAt < VideoTaskRetrySubmitTimeoutSeconds {
		return false, nil
	}
	if len(retry.Body) == 0 || retry.AcceptedAt > 0 && now-retry.AcceptedAt <= VideoTaskRetryWindowSeconds {
		return true, nil
	}
	retry.Body = nil
	return task.UpdateWithStatus(task.Status)
}

func failVideoTaskRetry(ctx context.Context, task *model.Task, reason string) error {
	fromStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = taskcommon.ProgressComplete
	task.FailReason = reason
	task.FinishTime = time.Now().Unix()
	won, err := task.UpdateWithStatus(fromStatus)
	if err != nil || !won {
		return err
	}
	return RefundTaskQuota(ctx, task, reason)
}
