package service

import (
	"crypto/sha256"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type TransferObservation struct {
	EventID   string `json:"event_id"`
	TaskID    string `json:"task_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Route     string `json:"route"`
	Bytes     int64  `json:"bytes"`
	Resumes   int    `json:"resumes"`
	Reason    string `json:"reason"`
	Outcome   string `json:"outcome"`
}

type TransferObservationTotals struct {
	Reports   int64            `json:"reports"`
	Bytes     int64            `json:"bytes"`
	Resumes   int64            `json:"resumes"`
	Completed int64            `json:"completed"`
	Failed    int64            `json:"failed"`
	Cancelled int64            `json:"cancelled"`
	Reasons   map[string]int64 `json:"reasons"`
}

type TransferObservationSnapshot struct {
	Source    string                               `json:"source"`
	StartedAt int64                                `json:"started_at"`
	Routes    map[string]TransferObservationTotals `json:"routes"`
}

type transferObservationReceipt struct {
	hash [32]byte
	at   time.Time
	user int
}

var transferObservationID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var ErrTransferObservationConflict = errors.New("download observation ID was already used for different data")
var ErrTransferObservationBusy = errors.New("download observation capacity reached")

type TransferObservationStore struct {
	mu      sync.Mutex
	started int64
	events  map[string]transferObservationReceipt
	users   map[int]int
	routes  map[string]TransferObservationTotals
}

func NewTransferObservationStore() *TransferObservationStore {
	store := &TransferObservationStore{started: time.Now().Unix(),
		events: map[string]transferObservationReceipt{}, users: map[int]int{},
		routes: map[string]TransferObservationTotals{}}
	for _, route := range []string{"bridge_upstream", "bridge_relay", "origin_upstream", "origin_relay", "qiniu"} {
		store.routes[route] = TransferObservationTotals{Reasons: map[string]int64{}}
	}
	return store
}

var TransferObservations = NewTransferObservationStore()

func ValidateTransferObservation(event TransferObservation) error {
	if !transferObservationID.MatchString(event.EventID) || len(event.TaskID) > 191 || len(event.RequestID) > 191 ||
		(event.TaskID == "" && event.RequestID == "") || event.Bytes < 0 || event.Bytes > 1<<40 ||
		event.Resumes < 0 || event.Resumes > 20 ||
		!slices.Contains([]string{"bridge_upstream", "bridge_relay", "origin_upstream", "origin_relay", "qiniu"}, event.Route) ||
		!slices.Contains([]string{"completed", "failed", "cancelled"}, event.Outcome) ||
		!slices.Contains([]string{"completed", "user_direct", "bridge_unavailable", "ticket_unavailable",
			"slow_route", "stream_error", "resumed", "cancelled", "other"}, event.Reason) {
		return errors.New("invalid download observation")
	}
	return nil
}

// Observations are untrusted client telemetry, not billing or authoritative
// bandwidth accounting. All counters reset on process restart. The receipt set
// is bounded and retains accepted IDs for 24 hours so retried reports do not add
// bytes twice; on capacity exhaustion no existing receipt is evicted early.
func (s *TransferObservationStore) Accept(user int, event TransferObservation) (bool, error) {
	if user <= 0 {
		return false, errors.New("invalid observation owner")
	}
	if err := ValidateTransferObservation(event); err != nil {
		return false, err
	}
	raw, err := common.Marshal(event)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(raw)
	key := strconv.Itoa(user) + ":" + event.EventID
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, receipt := range s.events {
		if now.Sub(receipt.at) >= 24*time.Hour {
			delete(s.events, id)
			s.users[receipt.user]--
			if s.users[receipt.user] == 0 {
				delete(s.users, receipt.user)
			}
		}
	}
	if previous, exists := s.events[key]; exists {
		if previous.hash != digest {
			return false, ErrTransferObservationConflict
		}
		return true, nil
	}
	if len(s.events) >= 10000 || s.users[user] >= 1000 {
		return false, ErrTransferObservationBusy
	}
	s.events[key] = transferObservationReceipt{hash: digest, at: now, user: user}
	s.users[user]++
	total := s.routes[event.Route]
	total.Reports++
	total.Bytes += event.Bytes
	total.Resumes += int64(event.Resumes)
	total.Reasons[event.Reason]++
	switch event.Outcome {
	case "completed":
		total.Completed++
	case "failed":
		total.Failed++
	case "cancelled":
		total.Cancelled++
	}
	s.routes[event.Route] = total
	return false, nil
}

func (s *TransferObservationStore) Snapshot() TransferObservationSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := TransferObservationSnapshot{Source: "client_reported", StartedAt: s.started,
		Routes: make(map[string]TransferObservationTotals, len(s.routes))}
	for route, total := range s.routes {
		reasons := make(map[string]int64, len(total.Reasons))
		for key, value := range total.Reasons {
			reasons[key] = value
		}
		total.Reasons = reasons
		result.Routes[route] = total
	}
	return result
}
