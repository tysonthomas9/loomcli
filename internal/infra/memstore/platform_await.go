package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// awaitStore is the in-memory store.AwaitStore (ARCHITECTURE-PROPOSAL §7
// step 8, chunk AW4).
//
// It deliberately carries no mutex of its own: every method synchronizes on
// the trigger-event journal's lock, so RegisterAwaitAndCheck's journal scan
// and index insert form one critical section with respect to event appends —
// the Go-mutex equivalent of fleet-db's single await_check_then_park Lua
// invocation (RULE 2). An event appended concurrently either lands before the
// scan, satisfying the await immediately, or after the park, where the
// dispatch matcher finds the parked instance via ListAwaitsByPattern. There
// is no interleave window for a lost wakeup.
//
// The registration scan covers the trigger-event journal; run.finished
// events ride that same journal once AW6 emits them through the
// internal-event loopback, so the "trigger-event journal + run.finished"
// decision is one scan here.
type awaitStore struct {
	// events is the journal the registration scan reads and whose mutex
	// guards ALL await state (see the type comment).
	events *triggerEventStore
	// items holds every await row, pending and terminal. Terminal rows are
	// the replay journal GetSatisfiedAwait serves (RULE 3: a re-executed run
	// deterministically learns which event satisfied which await).
	// ws -> instanceKey -> row.
	items map[string]map[string]*domain.AwaitInstance
	// byPattern indexes PENDING awaits only — the dispatch-side reverse
	// lookup (resolve-ALL multi-waiter). Resolved rows leave the index.
	// ws -> pattern -> instanceKey set.
	byPattern map[string]map[string]map[string]struct{}
}

func newAwaitStore(events *triggerEventStore) *awaitStore {
	return &awaitStore{
		events:    events,
		items:     make(map[string]map[string]*domain.AwaitInstance),
		byPattern: make(map[string]map[string]map[string]struct{}),
	}
}

var _ store.AwaitStore = (*awaitStore)(nil)

// defaultAwaitDeadlineListLimit caps ListDueAwaitDeadlines when the caller
// passes limit <= 0 (the "implementation default" of the store contract).
const defaultAwaitDeadlineListLimit = 100

// resumeEligible reports whether the named await cycle has resolved to a
// status that grants resuming its suspended run: satisfied or timed_out.
// Pending and cancel-cascaded awaits never grant resume — the security gate
// mirroring fleet-db's resume Lua AWAIT_NOT_TERMINAL guard.
func (s *awaitStore) resumeEligible(ws, instanceKey string) bool {
	s.events.mu.RLock()
	defer s.events.mu.RUnlock()
	row, ok := s.items[ws][instanceKey]
	if !ok {
		return false
	}
	return row.Status == domain.AwaitSatisfied || row.Status == domain.AwaitTimedOut
}

// RegisterAwaitAndCheck atomically checks the journal and otherwise parks the
// await (RULE 2: one call, one critical section). Idempotent on InstanceKey:
// a pending row is returned unchanged (crash-before-suspend replay) and a
// terminal row replays its recorded outcome with Satisfied=true so the caller
// never re-parks a finished await (RULE 3).
func (s *awaitStore) RegisterAwaitAndCheck(_ context.Context, workspaceKey string, in store.AwaitRegistration) (*store.AwaitResult, error) {
	now := time.Now().UTC()
	inst, err := in.Instance(workspaceKey, now)
	if err != nil {
		return nil, err
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	if existing, ok := s.items[workspaceKey][inst.InstanceKey]; ok {
		return &store.AwaitResult{
			Instance:  cloneAwaitInstance(existing),
			Satisfied: existing.Status.IsTerminal(),
		}, nil
	}
	// RULE 1 + RULE 4: exact rendered-key equality against the journal,
	// filtered by the actor allow-list, inside the same lock the journal's
	// appends take — the lost-wakeup fix (vet A2).
	if event := s.matchJournalLocked(workspaceKey, inst); event != nil {
		inst.Status = domain.AwaitSatisfied
		inst.SatisfiedByEventID = event.EventID
	}
	s.insertLocked(workspaceKey, inst)
	return &store.AwaitResult{
		Instance:  cloneAwaitInstance(inst),
		Satisfied: inst.Status == domain.AwaitSatisfied,
	}, nil
}

// matchJournalLocked scans the trigger-event journal for the earliest event
// whose rendered key exactly equals the await pattern and whose actor passes
// the allow-list. Earliest = ReceivedAt, then EventID, so re-registration
// replays deterministically. Caller holds the journal lock.
func (s *awaitStore) matchJournalLocked(ws string, inst *domain.AwaitInstance) *domain.TriggerEvent {
	var best *domain.TriggerEvent
	for _, event := range s.events.items[ws] {
		if domain.AwaitEventKey(event.EventType, event.SubjectRef) != inst.Pattern {
			continue
		}
		if !awaitActorAllowed(inst.ActorAllow, event.ActorRef) {
			continue
		}
		if best == nil || event.ReceivedAt.Before(best.ReceivedAt) ||
			(event.ReceivedAt.Equal(best.ReceivedAt) && event.EventID < best.EventID) {
			best = event
		}
	}
	return best
}

// insertLocked stores the row and, while it is pending, indexes it for the
// dispatch matcher. Caller holds the journal lock.
func (s *awaitStore) insertLocked(ws string, inst *domain.AwaitInstance) {
	if s.items[ws] == nil {
		s.items[ws] = make(map[string]*domain.AwaitInstance)
		s.byPattern[ws] = make(map[string]map[string]struct{})
	}
	stored := cloneAwaitInstance(inst)
	s.items[ws][stored.InstanceKey] = stored
	if stored.Status != domain.AwaitPending {
		return
	}
	if s.byPattern[ws][stored.Pattern] == nil {
		s.byPattern[ws][stored.Pattern] = make(map[string]struct{})
	}
	s.byPattern[ws][stored.Pattern][stored.InstanceKey] = struct{}{}
}

// ResolveAwait transitions one pending await out of pending, persisting the
// size-capped resume payload on the row. A synthetic deadline-sweeper event
// (domain.IsAwaitTimeoutEventID) lands the row in timed_out instead of
// satisfied. Resolving an already-terminal await is the idempotent replay:
// Resume=false, first writer's outcome untouched. The actor parameter is the
// verified resolver identity; the eligible-actor predicate is enforced by the
// dispatch matcher / approval endpoint before this call (AW7), mirroring
// fleet-db's resolve_await.lua, so it is not re-checked here.
func (s *awaitStore) ResolveAwait(_ context.Context, workspaceKey, instanceKey, eventID string, payload json.RawMessage, _ string) (*store.AwaitResolution, error) {
	if eventID == "" {
		return nil, fmt.Errorf("resolve await %q: event id required: %w", instanceKey, domain.ErrInvalid)
	}
	if len(payload) > domain.DefaultAwaitResumePayloadCap {
		return nil, fmt.Errorf("resolve await %q: resume payload %d bytes exceeds cap %d: %w",
			instanceKey, len(payload), domain.DefaultAwaitResumePayloadCap, domain.ErrInvalid)
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	inst, ok := s.items[workspaceKey][instanceKey]
	if !ok {
		return nil, fmt.Errorf("await %q in workspace %q: %w", instanceKey, workspaceKey, domain.ErrNotFound)
	}
	if inst.Status.IsTerminal() {
		return &store.AwaitResolution{Instance: cloneAwaitInstance(inst), Resume: false}, nil
	}
	now := time.Now().UTC()
	inst.Status = domain.AwaitSatisfied
	if domain.IsAwaitTimeoutEventID(eventID) {
		inst.Status = domain.AwaitTimedOut
	}
	inst.SatisfiedByEventID = eventID
	inst.SatisfiedPayload = cloneAwaitPayload(payload)
	inst.ResumedAt = &now
	s.dropPatternIndexLocked(workspaceKey, inst.Pattern, inst.InstanceKey)
	return &store.AwaitResolution{Instance: cloneAwaitInstance(inst), Resume: true}, nil
}

// ListAwaitsByPattern returns ALL pending awaits whose pattern exactly equals
// pattern (resolve-ALL multi-waiter decision), RegisteredAt ascending with
// InstanceKey tie-break so dispatch resolves waiters deterministically.
func (s *awaitStore) ListAwaitsByPattern(_ context.Context, workspaceKey, pattern string) ([]*domain.AwaitInstance, error) {
	s.events.mu.RLock()
	defer s.events.mu.RUnlock()
	keys := s.byPattern[workspaceKey][pattern]
	out := make([]*domain.AwaitInstance, 0, len(keys))
	for key := range keys {
		if inst := s.items[workspaceKey][key]; inst != nil && inst.Status == domain.AwaitPending {
			out = append(out, cloneAwaitInstance(inst))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].RegisteredAt.Equal(out[j].RegisteredAt) {
			return out[i].RegisteredAt.Before(out[j].RegisteredAt)
		}
		return out[i].InstanceKey < out[j].InstanceKey
	})
	return out, nil
}

// ListDueAwaitDeadlines returns pending awaits due at or before before,
// Deadline ascending with InstanceKey tie-break — the sweeper feed (the
// in-memory twin of fleet-db's deadline ZSET, sorted on read).
func (s *awaitStore) ListDueAwaitDeadlines(_ context.Context, workspaceKey string, before time.Time, limit int) ([]*domain.AwaitInstance, error) {
	if limit <= 0 {
		limit = defaultAwaitDeadlineListLimit
	}
	s.events.mu.RLock()
	defer s.events.mu.RUnlock()
	out := make([]*domain.AwaitInstance, 0, len(s.items[workspaceKey]))
	for _, inst := range s.items[workspaceKey] {
		if inst.Status != domain.AwaitPending || inst.Deadline.After(before) {
			continue
		}
		out = append(out, cloneAwaitInstance(inst))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Deadline.Equal(out[j].Deadline) {
			return out[i].Deadline.Before(out[j].Deadline)
		}
		return out[i].InstanceKey < out[j].InstanceKey
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetSatisfiedAwait is the replay path: the satisfied (or timed_out) row with
// its persisted resume payload inline. Missing, pending, and cancelled rows
// wrap domain.ErrNotFound.
func (s *awaitStore) GetSatisfiedAwait(_ context.Context, workspaceKey, instanceKey string) (*domain.AwaitInstance, error) {
	s.events.mu.RLock()
	defer s.events.mu.RUnlock()
	inst, ok := s.items[workspaceKey][instanceKey]
	if !ok || (inst.Status != domain.AwaitSatisfied && inst.Status != domain.AwaitTimedOut) {
		return nil, fmt.Errorf("satisfied await %q in workspace %q: %w", instanceKey, workspaceKey, domain.ErrNotFound)
	}
	return cloneAwaitInstance(inst), nil
}

// dropPatternIndexLocked removes a no-longer-pending instance from the
// dispatch index. Caller holds the journal lock.
func (s *awaitStore) dropPatternIndexLocked(ws, pattern, instanceKey string) {
	keys := s.byPattern[ws][pattern]
	delete(keys, instanceKey)
	if len(keys) == 0 {
		delete(s.byPattern[ws], pattern)
	}
}

// awaitActorAllowed applies the RULE 4 allow-list: empty allows any actor.
func awaitActorAllowed(allow []string, actor string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, a := range allow {
		if a == actor {
			return true
		}
	}
	return false
}

// cloneAwaitInstance deep-copies a row so callers never mutate stored state.
func cloneAwaitInstance(inst *domain.AwaitInstance) *domain.AwaitInstance {
	out := *inst
	out.ActorAllow = append([]string(nil), inst.ActorAllow...)
	out.SatisfiedPayload = cloneAwaitPayload(inst.SatisfiedPayload)
	out.ResumedAt = clonePtr(inst.ResumedAt)
	return &out
}

// cloneAwaitPayload copies a resume payload, preserving nil-ness (unlike
// cloneJSON, which defaults empty payloads to {} for run payload fields).
func cloneAwaitPayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(payload))
	copy(out, payload)
	return out
}
