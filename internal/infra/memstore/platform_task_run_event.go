package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// taskRunEventStore is the in-memory append-only TaskRunEvent journal. It
// is idempotent on EventID the same way fleet-db's storage layer is: an
// Append with a previously-seen EventID returns the existing event instead
// of inserting again. Seq is monotonic per workspace.
type taskRunEventStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.TaskRunEvent // ws -> eventID -> event
	seqs  map[string]int64                           // ws -> last assigned Seq
}

func newTaskRunEventStore() *taskRunEventStore {
	return &taskRunEventStore{
		items: make(map[string]map[string]*domain.TaskRunEvent),
		seqs:  make(map[string]int64),
	}
}

var _ store.TaskRunEventStore = (*taskRunEventStore)(nil)

func (s *taskRunEventStore) Append(_ context.Context, in store.TaskRunEventAppend) (*domain.TaskRunEvent, error) {
	eventID := in.EventID
	if eventID == "" {
		eventID = domain.TaskRunEventID(in.TaskRunID, in.Attempt, in.Type)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.items[in.WorkspaceKey][eventID]; ok {
		out := *existing
		return &out, nil
	}
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.TaskRunEvent)
	}
	s.seqs[in.WorkspaceKey]++
	var nextEligibleAt *time.Time
	if !in.NextEligibleAt.IsZero() {
		at := in.NextEligibleAt
		nextEligibleAt = &at
	}
	stored := domain.TaskRunEvent{
		WorkspaceKey:   in.WorkspaceKey,
		EventID:        eventID,
		Seq:            s.seqs[in.WorkspaceKey],
		EpicID:         in.EpicID,
		DriverRunID:    in.DriverRunID,
		TaskID:         in.TaskID,
		TaskRunID:      in.TaskRunID,
		Type:           in.Type,
		Status:         in.Status,
		SchedulerState: in.SchedulerState,
		Attempt:        in.Attempt,
		ErrorClass:     in.ErrorClass,
		ErrorMessage:   in.ErrorMessage,
		LogsRef:        in.LogsRef,
		ArtifactsRef:   in.ArtifactsRef,
		NextEligibleAt: nextEligibleAt,
		OccurredAt:     in.OccurredAt,
	}
	s.items[in.WorkspaceKey][eventID] = &stored
	out := stored
	return &out, nil
}

func (s *taskRunEventStore) ListSince(_ context.Context, ws string, filter store.TaskRunEventFilter) ([]*domain.TaskRunEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TaskRunEvent, 0, len(s.items[ws]))
	for _, event := range s.items[ws] {
		if event.Seq <= filter.AfterSeq {
			continue
		}
		if filter.EpicID != "" && event.EpicID != filter.EpicID {
			continue
		}
		if filter.DriverRunID != "" && event.DriverRunID != filter.DriverRunID {
			continue
		}
		clone := *event
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}
