package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// outboxStore is the in-memory OutboxStore. Create dedupes on DedupeKey the
// same way fleet-db's storage layer does: a create with a previously-seen
// key returns the existing record instead of inserting again. Seq is
// monotonic per workspace.
type outboxStore struct {
	mu     sync.RWMutex
	items  map[string]map[string]*domain.OutboxRecord // ws -> outboxID -> record
	dedupe map[string]map[string]string               // ws -> dedupeKey -> outboxID
	seqs   map[string]int64                           // ws -> last assigned Seq
}

func newOutboxStore() *outboxStore {
	return &outboxStore{
		items:  make(map[string]map[string]*domain.OutboxRecord),
		dedupe: make(map[string]map[string]string),
		seqs:   make(map[string]int64),
	}
}

var _ store.OutboxStore = (*outboxStore)(nil)

func (s *outboxStore) Create(_ context.Context, in store.OutboxCreate) (*domain.OutboxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.DedupeKey != "" {
		if existingID, ok := s.dedupe[in.WorkspaceKey][in.DedupeKey]; ok {
			return cloneOutboxRecord(s.items[in.WorkspaceKey][existingID]), nil
		}
	}
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.OutboxRecord)
		s.dedupe[in.WorkspaceKey] = make(map[string]string)
	}
	s.seqs[in.WorkspaceKey]++
	seq := s.seqs[in.WorkspaceKey]
	outboxID := in.OutboxID
	if outboxID == "" {
		outboxID = fmt.Sprintf("outbox-%d", seq)
	}
	now := time.Now().UTC()
	stored := domain.OutboxRecord{
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
		Status:       domain.OutboxStatusPending,
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

func (s *outboxStore) ListDue(_ context.Context, ws string, filter store.OutboxDueFilter) ([]*domain.OutboxRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.OutboxRecord, 0, len(s.items[ws]))
	for _, record := range s.items[ws] {
		if record.Status != domain.OutboxStatusPending {
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

func (s *outboxStore) MarkResult(_ context.Context, ws, outboxID string, update store.OutboxDeliveryUpdate) (*domain.OutboxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.items[ws][outboxID]
	if !ok {
		return nil, fmt.Errorf("outbox record %q in workspace %q: %w", outboxID, ws, domain.ErrNotFound)
	}
	now := time.Now().UTC()
	record.Status = update.Status
	record.Attempt = update.Attempt
	record.NextRetryAt = clonePtr(update.NextRetryAt)
	record.LastError = update.LastError
	record.InboxMessageID = update.InboxMessageID
	record.UpdatedAt = now
	if update.Status == domain.OutboxStatusDelivered {
		record.DeliveredAt = &now
	}
	return cloneOutboxRecord(record), nil
}

func (s *outboxStore) Get(_ context.Context, ws, outboxID string) (*domain.OutboxRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.items[ws][outboxID]
	if !ok {
		return nil, fmt.Errorf("outbox record %q in workspace %q: %w", outboxID, ws, domain.ErrNotFound)
	}
	return cloneOutboxRecord(record), nil
}

// cloneOutboxRecord deep-copies a record, including its optional pointer
// timestamps, so callers can never mutate stored state.
func cloneOutboxRecord(record *domain.OutboxRecord) *domain.OutboxRecord {
	out := *record
	out.NextRetryAt = clonePtr(record.NextRetryAt)
	out.DeliveredAt = clonePtr(record.DeliveredAt)
	return &out
}
