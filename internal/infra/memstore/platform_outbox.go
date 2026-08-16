package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// outboxStore is the in-memory OutboxStore. Create dedupes on DedupeKey the
// same way fleet-db's storage layer does: a create with a previously-seen
// key returns the existing record instead of inserting again. Seq is
// monotonic per workspace.
type outboxStore struct {
	mu     sync.RWMutex
	items  map[string]map[string]*execution.OutboxDelivery // ws -> outboxID -> record
	dedupe map[string]map[string]string                    // ws -> dedupeKey -> outboxID
	seqs   map[string]int64                                // ws -> last assigned Seq
}

func newOutboxStore() *outboxStore {
	return &outboxStore{
		items:  make(map[string]map[string]*execution.OutboxDelivery),
		dedupe: make(map[string]map[string]string),
		seqs:   make(map[string]int64),
	}
}

var _ execution.OutboxStore = (*outboxStore)(nil)

func (s *outboxStore) Create(_ context.Context, in execution.OutboxCreate) (*execution.OutboxDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.DedupeKey != "" {
		if existingID, ok := s.dedupe[in.WorkspaceKey][in.DedupeKey]; ok {
			return cloneOutboxRecord(s.items[in.WorkspaceKey][existingID]), nil
		}
	}
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*execution.OutboxDelivery)
		s.dedupe[in.WorkspaceKey] = make(map[string]string)
	}
	s.seqs[in.WorkspaceKey]++
	seq := s.seqs[in.WorkspaceKey]
	outboxID := in.OutboxID
	if outboxID == "" {
		outboxID = fmt.Sprintf("outbox-%d", seq)
	}
	now := time.Now().UTC()
	stored := execution.OutboxDelivery{
		WorkspaceKey: in.WorkspaceKey,
		OutboxID:     outboxID,
		Seq:          seq,
		Kind:         in.Kind,
		EpicID:       in.EpicID,
		DriverRunID:  in.DriverRunID,
		TaskRunID:    in.TaskRunID,
		TargetAgent:  in.TargetAgent,
		Body:         in.Body,
		DedupeKey:    in.DedupeKey,
		Status:       execution.OutboxDeliveryStatusPending,
		Attempt:      0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.items[in.WorkspaceKey][outboxID] = &stored
	if in.DedupeKey != "" {
		s.dedupe[in.WorkspaceKey][in.DedupeKey] = outboxID
	}
	return cloneOutboxRecord(&stored), nil
}

func (s *outboxStore) ListDue(_ context.Context, ws string, filter execution.OutboxDueFilter) ([]*execution.OutboxDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*execution.OutboxDelivery, 0, len(s.items[ws]))
	for _, record := range s.items[ws] {
		if record.Status != execution.OutboxDeliveryStatusPending {
			continue
		}
		if record.NextRetryAt != nil && record.NextRetryAt.After(filter.Now) {
			continue
		}
		out = append(out, cloneOutboxRecord(record))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *outboxStore) MarkResult(_ context.Context, ws, outboxID string, update execution.OutboxDeliveryUpdate) (*execution.OutboxDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.items[ws][outboxID]
	if !ok {
		return nil, fmt.Errorf("outbox record %q in workspace %q: %w", outboxID, ws, persistence.ErrNotFound)
	}
	now := time.Now().UTC()
	record.Status = update.Status
	record.Attempt = update.Attempt
	record.NextRetryAt = clonePtr(update.NextRetryAt)
	record.LastError = update.LastError
	record.InboxMessageID = update.InboxMessageID
	record.UpdatedAt = now
	if update.Status == execution.OutboxDeliveryStatusDelivered {
		record.DeliveredAt = &now
	}
	return cloneOutboxRecord(record), nil
}

func (s *outboxStore) Get(_ context.Context, ws, outboxID string) (*execution.OutboxDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.items[ws][outboxID]
	if !ok {
		return nil, fmt.Errorf("outbox record %q in workspace %q: %w", outboxID, ws, persistence.ErrNotFound)
	}
	return cloneOutboxRecord(record), nil
}

// cloneOutboxRecord deep-copies a record, including its optional pointer
// timestamps, so callers can never mutate stored state.
func cloneOutboxRecord(record *execution.OutboxDelivery) *execution.OutboxDelivery {
	out := *record
	out.NextRetryAt = clonePtr(record.NextRetryAt)
	out.DeliveredAt = clonePtr(record.DeliveredAt)
	return &out
}
