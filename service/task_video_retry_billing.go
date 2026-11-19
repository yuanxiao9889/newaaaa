package service

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// taskRetryBilling reuses the original reservation while its durable
// submitting claim owns the task. Only the final polling result refunds it.
type taskRetryBilling struct {
	ctx   context.Context
	task  *model.Task
	info  *relaycommon.RelayInfo
	mu    sync.Mutex
	image bool
}

// NewImageTaskRetryBilling keeps all replacement submissions on the queue's
// existing reservation. The image worker alone owns final settlement/refund.
func NewImageTaskRetryBilling(ctx context.Context, task *model.Task, info *relaycommon.RelayInfo) (relaycommon.BillingSettler, error) {
	if task == nil || task.ID <= 0 || info == nil || task.PrivateData.ImageRetry == nil {
		return nil, errors.New("image retry billing context is missing")
	}
	info.BillingSource, info.SubscriptionId = task.PrivateData.BillingSource, task.PrivateData.SubscriptionId
	info.FinalPreConsumedQuota = task.Quota
	return &taskRetryBilling{ctx: ctx, task: task, info: info, image: true}, nil
}

func NewVideoTaskRetryBilling(ctx context.Context, task *model.Task, info *relaycommon.RelayInfo) (relaycommon.BillingSettler, error) {
	if task == nil || task.ID <= 0 || info == nil || task.PrivateData.VideoRetry == nil {
		return nil, errors.New("video retry billing context is missing")
	}
	session := &taskRetryBilling{ctx: ctx, task: task, info: info}
	info.BillingSource = task.PrivateData.BillingSource
	if info.BillingSource == "" {
		info.BillingSource = BillingSourceWallet
	}
	info.SubscriptionId = task.PrivateData.SubscriptionId
	info.FinalPreConsumedQuota = task.Quota
	if info.BillingSource == BillingSourceSubscription {
		info.SubscriptionPreConsumed = int64(task.Quota)
	}
	return session, nil
}

func (s *taskRetryBilling) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if targetQuota < 0 {
		return errors.New("video retry quota cannot be negative")
	}
	setQuota := model.SetVideoTaskRetryQuota
	if s.image {
		setQuota = model.SetImageTaskRetryQuota
	}
	if err := setQuota(s.ctx, s.task, max(targetQuota, s.task.Quota)); err != nil {
		switch err.Error() {
		case "video retry token quota insufficient":
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
		case "video retry wallet quota insufficient", "video retry subscription quota insufficient":
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
		}
		return err
	}
	s.info.FinalPreConsumedQuota = s.task.Quota
	if s.info.BillingSource == BillingSourceSubscription {
		s.info.SubscriptionPreConsumed = int64(s.task.Quota)
	}
	return nil
}

// ReserveBeforeSubmit keeps retries covered by the existing durable reservation.
func (s *taskRetryBilling) ReserveBeforeSubmit(targetQuota int) error {
	return s.Reserve(targetQuota)
}

func (s *taskRetryBilling) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image {
		return errors.New("image retry settlement belongs to the queue worker")
	}
	previous := s.task.Quota
	if err := model.SetVideoTaskRetryQuota(s.ctx, s.task, actualQuota); err != nil {
		return err
	}
	s.info.FinalPreConsumedQuota = s.task.Quota
	if s.info.BillingSource == BillingSourceSubscription {
		s.info.SubscriptionPostDelta += int64(actualQuota - previous)
	}
	return nil
}

func (s *taskRetryBilling) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.task.Quota
}

func (*taskRetryBilling) Refund(*gin.Context) {}

func (*taskRetryBilling) NeedsRefund() bool { return false }
