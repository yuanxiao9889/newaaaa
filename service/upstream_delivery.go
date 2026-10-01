package service

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
)

type deliveryContextKey struct{}
type deliveryAttemptKey struct{}

// UpstreamDelivery records transport evidence, not provider business acceptance.
// A successful write only proves that the local transport wrote the request.
type UpstreamDelivery struct {
	TaskID            string `json:"task_id"`
	WorkerAttempt     int    `json:"worker_attempt"`
	Attempt           int    `json:"attempt"`
	ChannelID         int    `json:"channel_id"`
	Stage             string `json:"stage"`
	StartedAt         int64  `json:"started_at"`
	UpdatedAt         int64  `json:"updated_at"`
	ElapsedMS         int64  `json:"elapsed_ms"`
	Host              string `json:"host,omitempty"`
	PeerAddress       string `json:"peer_address,omitempty"`
	FailureStage      string `json:"failure_stage,omitempty"`
	Method            string `json:"method,omitempty"`
	ConnectionReady   bool   `json:"connection_ready"`
	ConnectionReused  bool   `json:"connection_reused"`
	RequestWritten    bool   `json:"request_written"`
	WriteCount        int    `json:"write_count"`
	ResponseReceived  bool   `json:"response_received"`
	StatusCode        int    `json:"status_code,omitempty"`
	UpstreamRequestID string `json:"upstream_request_id,omitempty"`
	ResponseComplete  bool   `json:"response_complete"`
	ErrorKind         string `json:"error_kind,omitempty"`
}

type deliverySession struct {
	mu            sync.Mutex
	taskID        string
	workerAttempt int
	attempts      []*deliveryAttempt
}
type deliveryAttempt struct {
	mu    sync.Mutex
	start time.Time
	data  UpstreamDelivery
}

var activeDeliveries sync.Map

// TrackAsyncImageDelivery keeps only active workers in memory. Events also go to
// the normal server log, so a process restart does not erase already emitted evidence.
func TrackAsyncImageDelivery(ctx context.Context, taskID string, workerAttempt int) (context.Context, func()) {
	session := &deliverySession{taskID: taskID, workerAttempt: workerAttempt}
	activeDeliveries.Store(taskID, session)
	return context.WithValue(ctx, deliveryContextKey{}, session), func() {
		activeDeliveries.CompareAndDelete(taskID, session)
	}
}

func BeginUpstreamDelivery(ctx context.Context, channelID int, attemptNumber int) context.Context {
	session, _ := ctx.Value(deliveryContextKey{}).(*deliverySession)
	if session == nil {
		return ctx
	}
	session.mu.Lock()
	attempt := &deliveryAttempt{start: time.Now()}
	attempt.data = UpstreamDelivery{TaskID: session.taskID, WorkerAttempt: session.workerAttempt, Attempt: attemptNumber, ChannelID: channelID, StartedAt: attempt.start.UnixMilli()}
	session.attempts = append(session.attempts, attempt)
	session.mu.Unlock()
	attempt.record("channel_selected", nil)
	return context.WithValue(ctx, deliveryAttemptKey{}, attempt)
}

// AsyncImageDeliverySnapshot is also used by the independent timeout sweeper.
// Match worker ownership so an older worker cannot report a replacement's evidence.
func AsyncImageDeliverySnapshot(taskID string, workerAttempt int) []UpstreamDelivery {
	value, ok := activeDeliveries.Load(taskID)
	if !ok {
		return nil
	}
	session := value.(*deliverySession)
	if session.workerAttempt != workerAttempt {
		return nil
	}
	session.mu.Lock()
	attempts := append([]*deliveryAttempt(nil), session.attempts...)
	session.mu.Unlock()
	result := make([]UpstreamDelivery, 0, len(attempts))
	for _, attempt := range attempts {
		attempt.mu.Lock()
		data := attempt.data
		data.ElapsedMS = time.Since(attempt.start).Milliseconds()
		attempt.mu.Unlock()
		result = append(result, data)
	}
	return result
}

func (a *deliveryAttempt) record(stage string, update func(*UpstreamDelivery)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if stage == "transport_failed" || stage == "response_body_failed" {
		a.data.FailureStage = a.data.Stage
	}
	a.data.Stage = stage
	a.data.UpdatedAt = time.Now().UnixMilli()
	a.data.ElapsedMS = time.Since(a.start).Milliseconds()
	if update != nil {
		update(&a.data)
	}
	// Never log bodies, authorization, query strings, URL paths or raw errors.
	encoded, err := common.Marshal(a.data)
	if err == nil {
		logger.LogInfo(context.Background(), "upstream_delivery "+string(encoded))
	}
}

func deliveryErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "network_timeout"
	}
	return "transport_error"
}
