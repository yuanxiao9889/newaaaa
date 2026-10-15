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

// videoTaskRetryBilling reuses the original reservation while its durable
// submitting claim owns the task. Only the final polling result refunds it.
type videoTaskRetryBilling struct {
	ctx  context.Context
	task *model.Task
	info *relaycommon.RelayInfo
	mu   sync.Mutex
}

func NewVideoTaskRetryBilling(ctx context.Context, task *model.Task, info *relaycommon.RelayInfo) (relaycommon.BillingSettler, error) {
	if task == nil || task.ID <= 0 || info == nil || task.PrivateData.VideoRetry == nil {
		return nil, errors.New("video retry billing context is missing")
	}
	session := &videoTaskRetryBilling{ctx: ctx, task: task, info: info}
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

func (s *videoTaskRetryBilling) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if targetQuota < 0 {
		return errors.New("video retry quota cannot be negative")
	}
	if err := model.SetVideoTaskRetryQuota(s.ctx, s.task, max(targetQuota, s.task.Quota)); err != nil {
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

func (s *videoTaskRetryBilling) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *videoTaskRetryBilling) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.task.Quota
}

func (*videoTaskRetryBilling) Refund(*gin.Context) {}

func (*videoTaskRetryBilling) NeedsRefund() bool { return false }
