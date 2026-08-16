package memstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// awaitStore is the in-memory execution.AwaitStore (ARCHITECTURE-PROPOSAL §7
// step 8, chunk AW4).
//
// It deliberately carries no mutex of its own: every method synchronizes on
// the trigger-event journal's lock, so RegisterAwaitAndCheck's journal scan
// and index insert form one critical section with respect to event appends —
// the Go-mutex equivalent of fleet-db's single await_register_and_check Lua
// invocation (RULE 2). An event appended concurrently either lands before the
// scan, satisfying the await immediately, or after registration, where the
// dispatch matcher finds the pending instance via ListAwaitsByPattern. There
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
	items map[string]map[string]*execution.AwaitInstance
	// byPattern indexes PENDING awaits only — the dispatch-side reverse
	// lookup (resolve-ALL multi-waiter). Resolved rows leave the index.
	// ws -> pattern -> instanceKey set.
	byPattern map[string]map[string]map[string]struct{}
	// runs is wired by Store.New so the specialized run.finished command can
	// resolve the await and resume/mark its parent under one lock ordering.
	runs *driverRunStore
}

func newAwaitStore(events *triggerEventStore) *awaitStore {
	return &awaitStore{
		events:    events,
		items:     make(map[string]map[string]*execution.AwaitInstance),
		byPattern: make(map[string]map[string]map[string]struct{}),
	}
}

var _ execution.AwaitStore = (*awaitStore)(nil)
var _ execution.AtomicAwaitStore = (*awaitStore)(nil)
var _ execution.RunOutcomeAwaitStore = (*awaitStore)(nil)

// defaultAwaitDeadlineListLimit caps ListDueAwaitDeadlines when the caller
// passes limit <= 0 (the "implementation default" of the store contract).
const defaultAwaitDeadlineListLimit = 100
const runOutcomeAwaitActor = "system"

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
	return row.Status == execution.AwaitSatisfied || row.Status == execution.AwaitTimedOut
}

// RegisterAwaitAndCheck atomically checks the journal and otherwise suspends the
// await (RULE 2: one call, one critical section). Idempotent on InstanceKey:
// a pending row is returned unchanged (crash-before-suspend replay) and a
// terminal row replays its recorded outcome with Satisfied=true so the caller
// never re-holds a finished await (RULE 3).
func (s *awaitStore) RegisterAwaitAndCheck(_ context.Context, workspaceKey string, in execution.AwaitRegistration) (*execution.AwaitRegistrationResult, error) {
	now := time.Now().UTC()
	inst, err := in.Instance(workspaceKey, now)
	if err != nil {
		return nil, err
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	if existing, ok := s.items[workspaceKey][inst.InstanceKey]; ok {
		return &execution.AwaitRegistrationResult{
			Instance:  cloneAwaitInstance(existing),
			Satisfied: existing.Status.IsTerminal(),
		}, nil
	}
	// RULE 1 + RULE 4: exact rendered-key equality against the journal,
	// filtered by the actor allow-list, inside the same lock the journal's
	// appends take — the lost-wakeup fix (vet A2).
	if event := s.matchJournalLocked(workspaceKey, inst); event != nil {
		inst.Status = execution.AwaitSatisfied
		inst.SatisfiedByEventID = event.SourceEventID
		if inst.SatisfiedByEventID == "" {
			inst.SatisfiedByEventID = event.EventID
		}
		inst.SatisfiedActor = event.ActorRef
		inst.SatisfiedPayload = cloneAwaitPayload(event.Payload)
	}
	s.insertLocked(workspaceKey, inst)
	return &execution.AwaitRegistrationResult{
		Instance:  cloneAwaitInstance(inst),
		Satisfied: inst.Status == execution.AwaitSatisfied,
	}, nil
}

// matchJournalLocked scans the trigger-event journal for the earliest event
// whose rendered key exactly equals the await pattern and whose actor passes
// the allow-list. Earliest = ReceivedAt, then EventID, so re-registration
// replays deterministically. Caller holds the journal lock.
func (s *awaitStore) matchJournalLocked(ws string, inst *execution.AwaitInstance) *automation.Event {
	var best *automation.Event
	for _, event := range s.events.items[ws] {
		eventID := event.SourceEventID
		if eventID == "" {
			eventID = event.EventID
		}
		// Synthetic timeout IDs are reserved to the deadline sweeper's
		// non-journaled SystemTimeoutLane. An ingress event that copied the
		// prefix must never satisfy a later registration through catch-up.
		if execution.IsAwaitTimeoutEventID(eventID) {
			continue
		}
		// run.finished is a reserved lifecycle lane. Historical external or
		// workflow rows (including rows backfilled into a new await index)
		// cannot satisfy registration-time catch-up merely by copying the
		// system actor name.
		if !automation.EligibleForAwait(
			event.EventType, string(event.Origin), event.SourceKind,
			event.ActorRef, eventID,
		) {
			continue
		}
		// Oversized event envelopes remain durable audit records but are not
		// eligible await-resume payloads. Continue scanning so one large event
		// cannot poison this pattern forever or hide a later valid winner.
		if len(event.Payload) > execution.DefaultAwaitResumePayloadCap {
			continue
		}
		if execution.AwaitEventKey(event.EventType, event.SubjectRef) != inst.Pattern {
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
func (s *awaitStore) insertLocked(ws string, inst *execution.AwaitInstance) {
	if s.items[ws] == nil {
		s.items[ws] = make(map[string]*execution.AwaitInstance)
		s.byPattern[ws] = make(map[string]map[string]struct{})
	}
	stored := cloneAwaitInstance(inst)
	s.items[ws][stored.InstanceKey] = stored
	if stored.Status != execution.AwaitPending {
		return
	}
	if s.byPattern[ws][stored.Pattern] == nil {
		s.byPattern[ws][stored.Pattern] = make(map[string]struct{})
	}
	s.byPattern[ws][stored.Pattern][stored.InstanceKey] = struct{}{}
}

// ResolveAwait transitions one pending await out of pending, persisting the
// size-capped resume payload on the row. A synthetic deadline-sweeper event
// (execution.IsAwaitTimeoutEventID) lands the row in timed_out instead of
// satisfied. Resolving an already-terminal await is the idempotent replay:
// Resume=false, first writer's outcome untouched. The actor parameter is the
// verified resolver identity; the eligible-actor predicate is enforced by the
// dispatch matcher / approval endpoint before this call (AW7), mirroring
// fleet-db's resolve_await.lua, so it is not re-checked here.
func (s *awaitStore) ResolveAwait(_ context.Context, workspaceKey, instanceKey, eventID string, payload json.RawMessage, actor string) (*execution.AwaitResolution, error) {
	if eventID == "" {
		return nil, fmt.Errorf("resolve await %q: event id required: %w", instanceKey, persistence.ErrInvalid)
	}
	if len(payload) > execution.DefaultAwaitResumePayloadCap {
		return nil, fmt.Errorf("resolve await %q: resume payload %d bytes exceeds cap %d: %w",
			instanceKey, len(payload), execution.DefaultAwaitResumePayloadCap, persistence.ErrInvalid)
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	inst, ok := s.items[workspaceKey][instanceKey]
	if !ok {
		return nil, fmt.Errorf("await %q in workspace %q: %w", instanceKey, workspaceKey, persistence.ErrNotFound)
	}
	if inst.Status.IsTerminal() {
		return &execution.AwaitResolution{Instance: cloneAwaitInstance(inst), Resume: false}, nil
	}
	now := time.Now().UTC()
	inst.Status = execution.AwaitSatisfied
	if execution.IsAwaitTimeoutEventID(eventID) {
		inst.Status = execution.AwaitTimedOut
	}
	inst.SatisfiedByEventID = eventID
	inst.SatisfiedActor = actor
	inst.SatisfiedPayload = cloneAwaitPayload(payload)
	inst.ResumedAt = &now
	s.dropPatternIndexLocked(workspaceKey, inst.Pattern, inst.InstanceKey)
	return &execution.AwaitResolution{Instance: cloneAwaitInstance(inst), Resume: true}, nil
}

// ResolveAwaitAndResume is the generic atomic dispatch lane. It acquires the
// DriverRun lock before the event/await lock, matching terminal DriverRun
// paths that enqueue outcomes while consulting the event journal. Keeping one
// global order prevents finish-versus-dispatch deadlocks while still making
// the two state transitions one critical section.
func (s *awaitStore) ResolveAwaitAndResume(
	_ context.Context,
	workspaceKey, instanceKey, eventID string,
	payload json.RawMessage,
	actor string,
) error {
	if strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("resolve await and resume %q: event id required: %w", instanceKey, persistence.ErrInvalid)
	}
	if len(payload) > execution.DefaultAwaitResumePayloadCap {
		return fmt.Errorf("resolve await and resume %q: resume payload %d bytes exceeds cap %d: %w",
			instanceKey, len(payload), execution.DefaultAwaitResumePayloadCap, persistence.ErrInvalid)
	}
	if s.runs == nil {
		return fmt.Errorf("resolve await and resume %q: DriverRun store unavailable: %w", instanceKey, persistence.ErrInvalid)
	}
	targetStatus := execution.AwaitSatisfied
	if execution.IsAwaitTimeoutEventID(eventID) {
		if actor != execution.AwaitTimeoutActor {
			return fmt.Errorf("resolve await and resume %q: timeout event actor %q: %w",
				instanceKey, actor, execution.ErrAwaitActorForbidden)
		}
		if target, ok := execution.AwaitTimeoutTargetInstance(eventID); !ok || target != instanceKey {
			return fmt.Errorf("resolve await and resume %q: timeout event targets %q: %w",
				instanceKey, target, persistence.ErrInvalid)
		}
		targetStatus = execution.AwaitTimedOut
	}
	return s.resolveAwaitAndResume(workspaceKey, instanceKey, eventID, payload, actor, targetStatus)
}

// ResolveRunOutcomeAwaitAndResume preserves the run.finished compatibility
// surface while sharing the generic atomic command. A composition waiter that
// explicitly excludes the system actor is a deliberate non-match, not an
// outbox poison record, so actor rejection remains a successful no-op here.
func (s *awaitStore) ResolveRunOutcomeAwaitAndResume(
	_ context.Context,
	workspaceKey, instanceKey, eventID string,
	payload json.RawMessage,
) error {
	if !strings.HasPrefix(eventID, "run-finished:") {
		return fmt.Errorf("resolve run outcome await %q: invalid event id: %w", instanceKey, persistence.ErrInvalid)
	}
	err := s.resolveAwaitAndResume(workspaceKey, instanceKey, eventID, payload, runOutcomeAwaitActor, execution.AwaitSatisfied)
	if errors.Is(err, execution.ErrAwaitActorForbidden) {
		return nil
	}
	return err
}

//nolint:funlen // The test store mirrors one atomic await-resolution and run-resume critical section.
func (s *awaitStore) resolveAwaitAndResume(
	workspaceKey, instanceKey, eventID string,
	payload json.RawMessage,
	actor string,
	targetStatus execution.AwaitStatus,
) error {
	if len(payload) > execution.DefaultAwaitResumePayloadCap {
		return fmt.Errorf("resolve await and resume %q: resume payload %d bytes exceeds cap %d: %w",
			instanceKey, len(payload), execution.DefaultAwaitResumePayloadCap, persistence.ErrInvalid)
	}
	if s.runs == nil {
		return fmt.Errorf("resolve await and resume %q: DriverRun store unavailable: %w", instanceKey, persistence.ErrInvalid)
	}
	runID, _, err := execution.ParseAwaitInstanceKey(instanceKey)
	if err != nil {
		return fmt.Errorf("resolve await and resume %q: %w", instanceKey, err)
	}

	s.runs.mu.Lock()
	defer s.runs.mu.Unlock()
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	inst := s.items[workspaceKey][instanceKey]
	if inst == nil {
		return fmt.Errorf("await %q in workspace %q: %w", instanceKey, workspaceKey, persistence.ErrNotFound)
	}
	if inst.RunID != runID {
		return fmt.Errorf("await %q belongs to run %q, not %q: %w",
			instanceKey, inst.RunID, runID, persistence.ErrInvalidTransition)
	}
	replay := inst.Status.IsTerminal()
	if replay && (inst.Status != targetStatus || inst.SatisfiedByEventID != eventID) {
		// A racing event (or timeout) won. This event is an idempotent no-op.
		return nil
	}
	if inst.Status == execution.AwaitPending && targetStatus == execution.AwaitSatisfied && !awaitActorAllowed(inst.ActorAllow, actor) {
		return fmt.Errorf("resolve await and resume %q by actor %q: %w",
			instanceKey, actor, execution.ErrAwaitActorForbidden)
	}

	run := s.runs.items[workspaceKey][runID]
	if run == nil {
		return fmt.Errorf("driver run %q in workspace %q: %w", inst.RunID, workspaceKey, persistence.ErrNotFound)
	}
	if replay {
		return convergeAtomicAwaitReplay(run, instanceKey, eventID, time.Now().UTC())
	}
	if err := validateAtomicAwaitResumeState(run, instanceKey, eventID); err != nil {
		return err
	}

	now := time.Now().UTC()
	if inst.Status == execution.AwaitPending {
		inst.Status = targetStatus
		inst.SatisfiedByEventID = eventID
		inst.SatisfiedActor = actor
		inst.SatisfiedPayload = cloneAwaitPayload(payload)
		inst.ResumedAt = &now
		s.dropPatternIndexLocked(workspaceKey, inst.Pattern, inst.InstanceKey)
	}
	switch run.Status {
	case execution.DriverRunSuspendedAwait:
		run.Status = execution.DriverRunQueued
		run.ResumeSourceEventID = eventID
		run.UpdatedAt = now
	case execution.DriverRunRunning:
		// Resolution won the pending->suspend window. Suspend observes this
		// marker and refuses to park the run.
		run.AwaitInstanceKey = instanceKey
		run.ResumeSourceEventID = eventID
		run.UpdatedAt = now
	}
	return nil
}

// convergeAtomicAwaitReplay heals only the same committed await cycle. A run
// that has progressed to another running/queued marker is a successful no-op;
// delayed replay must never move execution backward.
func convergeAtomicAwaitReplay(run *execution.DriverRunRecord, instanceKey, eventID string, now time.Time) error {
	switch run.Status {
	case execution.DriverRunSuspendedAwait:
		if run.AwaitInstanceKey != instanceKey {
			return nil
		}
		run.Status = execution.DriverRunQueued
		run.ResumeSourceEventID = eventID
		run.UpdatedAt = now
		return nil
	case execution.DriverRunRunning, execution.DriverRunQueued:
		return nil
	default:
		if run.Status.IsTerminal() {
			return nil
		}
		return fmt.Errorf("driver run %q cannot replay await resume from %s: %w",
			run.RunID, run.Status, persistence.ErrInvalidTransition)
	}
}

func validateAtomicAwaitResumeState(run *execution.DriverRunRecord, instanceKey, eventID string) error {
	switch run.Status {
	case execution.DriverRunSuspendedAwait:
		if run.AwaitInstanceKey != instanceKey {
			return fmt.Errorf("driver run %q awaits %q, not %q: %w",
				run.RunID, run.AwaitInstanceKey, instanceKey, persistence.ErrInvalidTransition)
		}
	case execution.DriverRunRunning:
		if run.AwaitInstanceKey == instanceKey && run.ResumeSourceEventID != "" && run.ResumeSourceEventID != eventID {
			return fmt.Errorf("driver run %q already has a different pending resume: %w", run.RunID, persistence.ErrInvalidTransition)
		}
	case execution.DriverRunQueued:
		if run.ResumeSourceEventID != eventID {
			return fmt.Errorf("driver run %q is queued without await resume %q: %w",
				run.RunID, eventID, persistence.ErrInvalidTransition)
		}
	default:
		if !run.Status.IsTerminal() {
			return fmt.Errorf("driver run %q cannot resume from %s: %w", run.RunID, run.Status, persistence.ErrInvalidTransition)
		}
	}
	return nil
}

// ListAwaitsByPattern returns ALL pending awaits whose pattern exactly equals
// pattern (resolve-ALL multi-waiter decision), RegisteredAt ascending with
// InstanceKey tie-break so dispatch resolves waiters deterministically.
func (s *awaitStore) ListAwaitsByPattern(_ context.Context, workspaceKey, pattern string) ([]*execution.AwaitInstance, error) {
	s.events.mu.RLock()
	defer s.events.mu.RUnlock()
	keys := s.byPattern[workspaceKey][pattern]
	out := make([]*execution.AwaitInstance, 0, len(keys))
	for key := range keys {
		if inst := s.items[workspaceKey][key]; inst != nil && inst.Status == execution.AwaitPending {
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
func (s *awaitStore) ListDueAwaitDeadlines(_ context.Context, workspaceKey string, before time.Time, limit int) ([]*execution.AwaitInstance, error) {
	if limit <= 0 {
		limit = defaultAwaitDeadlineListLimit
	}
	s.events.mu.RLock()
	defer s.events.mu.RUnlock()
	out := make([]*execution.AwaitInstance, 0, len(s.items[workspaceKey]))
	for _, inst := range s.items[workspaceKey] {
		if inst.Status != execution.AwaitPending || inst.Deadline.After(before) {
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
// wrap persistence.ErrNotFound.
func (s *awaitStore) GetSatisfiedAwait(_ context.Context, workspaceKey, instanceKey string) (*execution.AwaitInstance, error) {
	s.events.mu.RLock()
	defer s.events.mu.RUnlock()
	inst, ok := s.items[workspaceKey][instanceKey]
	if !ok || (inst.Status != execution.AwaitSatisfied && inst.Status != execution.AwaitTimedOut) {
		return nil, fmt.Errorf("satisfied await %q in workspace %q: %w", instanceKey, workspaceKey, persistence.ErrNotFound)
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
func cloneAwaitInstance(inst *execution.AwaitInstance) *execution.AwaitInstance {
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
