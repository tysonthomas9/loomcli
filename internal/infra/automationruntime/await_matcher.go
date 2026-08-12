package trigger

// Dispatch-time await matcher (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW7).
//
// AwaitMatcher wires event arrival — webhook fan-out, internal loopback,
// cron, run.finished lifecycle — into the awaited index. It runs AFTER the
// durable router dispatch admitted the event, on the adapter-normalized
// fields only (C7/C15: the matcher never re-parses raw payloads):
//
//	RULE 1 — the event's matching key is domain.AwaitEventKey(eventType,
//	         subjectRef), compared by exact string equality against pending
//	         await patterns (no glob; the same rendering every backend's
//	         registration scan uses), so cross-repo/cross-entity confusion
//	         is structurally impossible.
//	RULE 4 — per candidate instance the event actor must be in the await's
//	         ActorAllow predicate (empty = no predicate). A rejected actor
//	         is audited and NEVER resumes the run or feeds its payload —
//	         killing both the self-trigger loop and the prompt-injection
//	         hijack (vet A3).
//	RULE 2 — ResolveAwaitAndResume atomically marks exactly this instance
//	         satisfied by this event and re-queues its suspended run (or
//	         writes the pending-resume marker). First event wins; same-event
//	         replay converges; a different-event loser is a recorded no-op.
//
// Multi-waiter cardinality is the LOCKED decision: one event resolves ALL
// pending instances whose pattern equals the rendered key, in RegisteredAt
// order (store contract).
//
// Pending->suspend window (locked decision): an event can arrive after
// RegisterAwaitAndCheck pends the await but before the executor's Suspend
// lands. Both backends atomically write a pending-resume marker; Suspend then
// surfaces domain.ErrDriverRunAlreadyResumed and the execution continues
// inline. No retry budget or split resolve/resume crash window remains.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// AwaitMatcher resolves pending awaits against admitted router events through
// explicit read and mutation ports. It is safe for concurrent use because it
// holds no state of its own.
type AwaitMatcher struct {
	AwaitStore     store.AwaitStore
	DriverRunStore store.DriverRunStore
	AtomicResolver store.AtomicAwaitStore
	// Logger receives audit records (actor rejections, deferred resumes);
	// slog.Default when nil.
	Logger *slog.Logger
	// SystemTimeoutLane marks the deadline sweeper's dispatch lane (AW8): the
	// RULE 4 carve-out — domain.AwaitTimeoutActor is always allowed to resolve
	// the one instance its synthetic event targets, so a due await times out
	// no matter how restrictive its allow-list — applies only when set. Every
	// other lane leaves it false, so a forged system:timeout actor on an
	// ingress or loopback event still faces the await's allow-list (explicit
	// carve-out, not a general system bypass).
	SystemTimeoutLane bool
}

func NewAwaitMatcherWithResolver(
	awaits store.AwaitStore,
	driverRuns store.DriverRunStore,
	resolver store.AtomicAwaitStore,
) *AwaitMatcher {
	return &AwaitMatcher{AwaitStore: awaits, DriverRunStore: driverRuns, AtomicResolver: resolver}
}

// AwaitDispatchEvent is the router-output view of one admitted event — the
// adapter-normalized identity fields plus the (already JSON-valid) payload.
// EventID is the most stable identifier the dispatching hook holds for the
// event (the source/delivery id on ingress lanes, the deterministic lifecycle
// id for run.finished); it lands in SatisfiedByEventID for audit/correlation,
// while the resume payload itself is persisted on the satisfied row.
type AwaitDispatchEvent struct {
	EventID    string
	EventType  string
	SourceKind string
	Origin     automation.EventOrigin
	SubjectRef string
	ActorRef   string
	Payload    json.RawMessage
}

// AwaitMatchOutcome classifies one candidate instance's dispatch resolution.
type AwaitMatchOutcome string

const (
	// AwaitMatchResolved — this event resolved the await and the run is
	// re-queued.
	AwaitMatchResolved AwaitMatchOutcome = "resolved"
	// AwaitMatchActorRejected — the event actor failed the RULE 4 allow-list;
	// audited, nothing resolved or resumed.
	AwaitMatchActorRejected AwaitMatchOutcome = "actor_rejected"
	// AwaitMatchAlreadyResolved — a racing event (or the timeout sweeper) won;
	// this dispatch is the recorded no-op loser.
	AwaitMatchAlreadyResolved AwaitMatchOutcome = "already_resolved"
	// AwaitMatchResumeDeferred — the resolution stands but the run was already
	// terminal, so no run transition was possible or needed.
	AwaitMatchResumeDeferred AwaitMatchOutcome = "resume_deferred"
	// AwaitMatchFailed — a store error; details in the record's Reason and the
	// joined error returned by Dispatch.
	AwaitMatchFailed AwaitMatchOutcome = "failed"
)

// Stable Reason values recorded on AwaitMatchRecord (free-form error text is
// used for the failed outcome).
const (
	AwaitReasonActorRejected        = "actor_rejected"
	AwaitReasonPendingSuspendWindow = "pending_suspend_window"
	AwaitReasonRunAlreadyResumed    = "run_already_resumed"
	AwaitReasonRunTerminal          = "run_terminal"
)

// AwaitMatchRecord is the per-instance audit record of one Dispatch.
type AwaitMatchRecord struct {
	InstanceKey string
	RunID       string
	Outcome     AwaitMatchOutcome
	Reason      string
}

// AwaitDispatchResult collects one Dispatch pass over all candidates.
type AwaitDispatchResult struct {
	// SubjectKey is the rendered event key matched against await patterns
	// (empty when the event had no subject and matching was skipped).
	SubjectKey string
	Records    []AwaitMatchRecord
}

// Resolved counts the candidates this dispatch resolved AND resumed.
func (r *AwaitDispatchResult) Resolved() int {
	n := 0
	for _, rec := range r.Records {
		if rec.Outcome == AwaitMatchResolved {
			n++
		}
	}
	return n
}

// Dispatch matches one admitted event against the awaited index and resolves
// ALL matching pending instances (multi-waiter decision). It returns the
// per-instance records plus the joined store errors; callers hook it
// best-effort after durable dispatch and must not fail ingestion on error.
// An event without a subject has no rendered key (RULE 1) and matches
// nothing; a backend without await support (errors.ErrUnsupported) is a
// silent no-op.
func (m *AwaitMatcher) Dispatch(ctx context.Context, ws string, ev AwaitDispatchEvent) (*AwaitDispatchResult, error) {
	result := &AwaitDispatchResult{}
	awaits, driverRuns := m.persistence()
	if awaits == nil || driverRuns == nil || strings.TrimSpace(ev.EventID) == "" ||
		strings.TrimSpace(ev.EventType) == "" || strings.TrimSpace(ev.SubjectRef) == "" {
		return result, nil
	}
	isTimeout := domain.IsAwaitTimeoutEventID(ev.EventID)
	switch {
	case isTimeout && !m.SystemTimeoutLane:
		return result, fmt.Errorf("await dispatch: event id %q uses the reserved timeout prefix outside the timeout lane: %w",
			ev.EventID, domain.ErrInvalid)
	case m.SystemTimeoutLane && (!isTimeout || ev.ActorRef != domain.AwaitTimeoutActor):
		return result, fmt.Errorf("await dispatch: timeout lane requires a canonical timeout id and actor: %w", domain.ErrInvalid)
	}
	if !isTimeout && !automation.EligibleForAwait(
		ev.EventType, string(ev.Origin), ev.SourceKind, ev.ActorRef, ev.EventID,
	) {
		// Provenance is immutable. Treat a historical or forged reserved event
		// as an audited successful no-op so durable notification consumers can
		// complete it without backoff and a later genuine event can still win.
		m.auditReservedProvenanceRejected(ws, ev)
		return result, nil
	}
	result.SubjectKey = domain.AwaitEventKey(ev.EventType, ev.SubjectRef)
	candidates, err := awaits.ListAwaitsByPattern(ctx, ws, result.SubjectKey)
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return result, nil
		}
		return result, fmt.Errorf("await dispatch: list awaits for key %q in workspace %q: %w", result.SubjectKey, ws, err)
	}
	timeoutTarget, _ := domain.AwaitTimeoutTargetInstance(ev.EventID)
	var errs []error
	for _, inst := range candidates { // resolve ALL matching (locked decision)
		if isTimeout && inst.InstanceKey != timeoutTarget {
			// RULE 3: a synthetic timeout event resolves exactly the instance
			// it targets; co-waiters on the same pattern keep their own
			// deadlines (multi-waiter does not apply to timeouts).
			continue
		}
		record, err := m.dispatchOne(ctx, ws, ev, inst, ev.Payload)
		result.Records = append(result.Records, record)
		errs = append(errs, err)
	}
	return result, errors.Join(errs...)
}

// dispatchOne runs the RULE 4 actor predicate and the atomic resolve+resume
// command for a single candidate instance.
func (m *AwaitMatcher) dispatchOne(ctx context.Context, ws string, ev AwaitDispatchEvent, inst *domain.AwaitInstance, payload json.RawMessage) (AwaitMatchRecord, error) { //nolint:funlen // Atomic resolution outcome classification stays adjacent to the command it verifies.
	record := AwaitMatchRecord{InstanceKey: inst.InstanceKey, RunID: inst.RunID}
	awaits, driverRuns := m.persistence()
	if !m.dispatchActorAllowed(ev, inst) {
		record.Outcome, record.Reason = AwaitMatchActorRejected, AwaitReasonActorRejected
		m.auditActorRejected(ws, ev, inst)
		return record, nil
	}
	resolver := m.AtomicResolver
	if resolver == nil {
		err := fmt.Errorf("execution await resolver is unavailable: %w", errors.ErrUnsupported)
		record.Outcome, record.Reason = AwaitMatchFailed, err.Error()
		return record, fmt.Errorf("await dispatch: resolve and resume %q by event %q: %w", inst.InstanceKey, ev.EventID, err)
	}
	err := resolver.ResolveAwaitAndResume(ctx, ws, inst.InstanceKey, ev.EventID, payload, ev.ActorRef)
	if err != nil {
		record.Outcome, record.Reason = AwaitMatchFailed, err.Error()
		return record, fmt.Errorf("await dispatch: resolve and resume %q by event %q: %w", inst.InstanceKey, ev.EventID, err)
	}
	resolution, err := awaits.GetSatisfiedAwait(ctx, ws, inst.InstanceKey)
	if err != nil {
		// A cancel cascade can win after the pending candidate list was read.
		// Cancelled rows intentionally are not returned by GetSatisfiedAwait;
		// their terminal owner run proves this dispatch is a no-op loser.
		if errors.Is(err, domain.ErrNotFound) {
			run, runErr := driverRuns.Get(ctx, ws, inst.RunID)
			if runErr == nil && run.Status.IsTerminal() {
				record.Outcome = AwaitMatchAlreadyResolved
				return record, nil
			}
		}
		record.Outcome, record.Reason = AwaitMatchFailed, err.Error()
		return record, fmt.Errorf("await dispatch: read atomic resolution %q by event %q: %w", inst.InstanceKey, ev.EventID, err)
	}
	if resolution.SatisfiedByEventID != ev.EventID {
		// A racing event (or timeout) won. The atomic command left its winner
		// and run transition untouched.
		record.Outcome = AwaitMatchAlreadyResolved
		return record, nil
	}
	run, err := driverRuns.Get(ctx, ws, inst.RunID)
	if err != nil {
		record.Outcome, record.Reason = AwaitMatchFailed, err.Error()
		return record, fmt.Errorf("await dispatch: inspect atomically resumed run %q: %w", inst.RunID, err)
	}
	if run.ResumeSourceEventID == ev.EventID {
		record.Outcome = AwaitMatchResolved
		return record, nil
	}
	if run.Status.IsTerminal() {
		record.Outcome, record.Reason = AwaitMatchResumeDeferred, AwaitReasonRunTerminal
		m.auditResumeOutcome(ws, ev, inst, record)
		return record, nil
	}
	record.Outcome = AwaitMatchFailed
	record.Reason = fmt.Sprintf("atomic command returned run status %s without resume source %q", run.Status, ev.EventID)
	return record, fmt.Errorf("await dispatch: %q resolved by event %q but run %q did not converge: %s",
		inst.InstanceKey, ev.EventID, inst.RunID, record.Reason)
}

func (m *AwaitMatcher) persistence() (store.AwaitStore, store.DriverRunStore) {
	if m == nil {
		return nil, nil
	}
	return m.AwaitStore, m.DriverRunStore
}

// auditActorRejected records the RULE 4 rejection. DEVIATION from the chunk
// sketch's "Delivery row": TriggerDeliveryStore deliberately exposes no
// direct create (delivery rows are dispatch-path artifacts keyed by binding),
// so the durable audit is the structured log record plus the actor_rejected
// record on the dispatch result; the await stays pending and the event never
// reaches the run.
func (m *AwaitMatcher) auditActorRejected(ws string, ev AwaitDispatchEvent, inst *domain.AwaitInstance) {
	m.logger().Warn("await dispatch: event actor rejected by allow-list",
		"workspace", ws,
		"await_instance", inst.InstanceKey,
		"driver_run", inst.RunID,
		"await_pattern", inst.Pattern,
		"event_id", ev.EventID,
		"event_type", ev.EventType,
		"subject_ref", ev.SubjectRef,
		"actor_ref", ev.ActorRef,
		"reason", AwaitReasonActorRejected,
	)
}

// auditReservedProvenanceRejected records a forged or historical event that
// is ineligible to enter the await matcher. There is intentionally no error:
// retrying immutable provenance would poison a durable notification queue.
func (m *AwaitMatcher) auditReservedProvenanceRejected(ws string, ev AwaitDispatchEvent) {
	m.logger().Warn("await dispatch: reserved event provenance rejected",
		"workspace", ws,
		"event_id", ev.EventID,
		"event_type", ev.EventType,
		"source_kind", ev.SourceKind,
		"origin", string(ev.Origin),
		"subject_ref", ev.SubjectRef,
		"actor_ref", ev.ActorRef,
		"reason", "reserved_provenance",
	)
}

// auditResumeOutcome records a resolution whose resume did not land here.
func (m *AwaitMatcher) auditResumeOutcome(ws string, ev AwaitDispatchEvent, inst *domain.AwaitInstance, record AwaitMatchRecord) {
	m.logger().Warn("await dispatch: resolved without resuming run",
		"workspace", ws,
		"await_instance", inst.InstanceKey,
		"driver_run", inst.RunID,
		"event_id", ev.EventID,
		"outcome", string(record.Outcome),
		"reason", record.Reason,
	)
}

func (m *AwaitMatcher) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// dispatchActorAllowed applies RULE 4 with the AW8 timeout carve-out: on the
// sweeper's lane, domain.AwaitTimeoutActor resolving the one instance its own
// synthetic event targets bypasses the allow-list (a due await must time out
// no matter how restrictive its predicate). Everything else — including the
// same actor name on any other lane, or a timeout-shaped event naming a
// different instance — goes through the allow-list.
func (m *AwaitMatcher) dispatchActorAllowed(ev AwaitDispatchEvent, inst *domain.AwaitInstance) bool {
	if m.SystemTimeoutLane && ev.ActorRef == domain.AwaitTimeoutActor {
		if target, ok := domain.AwaitTimeoutTargetInstance(ev.EventID); ok && target == inst.InstanceKey {
			return true
		}
	}
	return awaitDispatchActorAllowed(inst.ActorAllow, ev.ActorRef)
}

// awaitDispatchActorAllowed applies the RULE 4 allow-list (empty = any
// actor), mirroring the registration-scan check in both backends.
func awaitDispatchActorAllowed(allow []string, actor string) bool {
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
