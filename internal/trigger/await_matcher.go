package trigger

// Dispatch-time await matcher (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW7).
//
// AwaitMatcher wires event arrival — webhook fan-out, internal loopback,
// cron, run.finished lifecycle — into the awaited index. It runs AFTER the
// durable router dispatch admitted the event, on the adapter-normalized
// fields only (C7/C15: the matcher never re-parses raw payloads):
//
//	RULE 1 — the event's matching key is domain.AwaitEventKey(eventType,
//	         subjectRef), compared by exact string equality against parked
//	         await patterns (no glob; the same rendering every backend's
//	         registration scan uses), so cross-repo/cross-entity confusion
//	         is structurally impossible.
//	RULE 4 — per candidate instance the event actor must be in the await's
//	         ActorAllow predicate (empty = no predicate). A rejected actor
//	         is audited and NEVER resumes the run or feeds its payload —
//	         killing both the self-trigger loop and the prompt-injection
//	         hijack (vet A3).
//	RULE 2 — ResolveAwait atomically marks exactly this instance satisfied
//	         by this event (first event wins; losers are recorded no-ops),
//	RULE 3 — then ResumeAwaiting re-queues the suspended run. A resume
//	         blocked because the run is not suspended leaves the resolution
//	         standing: the event is journaled and the satisfied row replays
//	         (re-entry contract).
//
// Multi-waiter cardinality is the LOCKED decision: one event resolves ALL
// pending instances whose pattern equals the rendered key, in RegisteredAt
// order (store contract).
//
// Park->suspend window (locked decision): an event can arrive after
// RegisterAwaitAndCheck parked the await but before the executor's Suspend
// landed. The fleet-db backend records a pending-resume marker and surfaces
// domain.ErrDriverRunAlreadyResumed to the suspend leg; memstore instead has
// the matcher retry the resume briefly until the suspend lands (the
// still-running run returns domain.ErrInvalidTransition). Both shapes are
// tolerated here; a window that outlives the retry budget is recorded as
// resume_deferred — the resolution stands and the deadline machinery (AW8)
// converges the run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Default park->suspend-window retry budget: how often and how long Dispatch
// re-attempts a resume blocked by a still-running run before recording
// resume_deferred. Bounded so an ingress hook never stalls long.
const (
	defaultAwaitResumeRetries    = 5
	defaultAwaitResumeRetryDelay = 100 * time.Millisecond
)

// AwaitMatcher resolves parked awaits against admitted router events. The
// zero value plus Store is ready to use; safe for concurrent use (it holds no
// state of its own).
type AwaitMatcher struct {
	Store store.Store
	// Logger receives audit records (actor rejections, deferred resumes);
	// slog.Default when nil.
	Logger *slog.Logger
	// ResumeRetries / ResumeRetryDelay tune the park->suspend-window retry
	// budget; zero values select the package defaults.
	ResumeRetries    int
	ResumeRetryDelay time.Duration
	// SystemTimeoutLane marks the deadline sweeper's dispatch lane (AW8): the
	// RULE 4 carve-out — domain.AwaitTimeoutActor is always allowed to resolve
	// the one instance its synthetic event targets, so a due await times out
	// no matter how restrictive its allow-list — applies only when set. Every
	// other lane leaves it false, so a forged system:timeout actor on an
	// ingress or loopback event still faces the await's allow-list (explicit
	// carve-out, not a general system bypass).
	SystemTimeoutLane bool
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
	// AwaitMatchResumeDeferred — the resolution stands but the run could not
	// be transitioned here (park->suspend window outlived the retry budget,
	// run already re-queued by the backend's pending-resume marker, or run
	// terminal).
	AwaitMatchResumeDeferred AwaitMatchOutcome = "resume_deferred"
	// AwaitMatchFailed — a store error; details in the record's Reason and the
	// joined error returned by Dispatch.
	AwaitMatchFailed AwaitMatchOutcome = "failed"
)

// Stable Reason values recorded on AwaitMatchRecord (free-form error text is
// used for the failed outcome).
const (
	AwaitReasonActorRejected     = "actor_rejected"
	AwaitReasonParkWindowPending = "park_window_pending"
	AwaitReasonRunAlreadyResumed = "run_already_resumed"
	AwaitReasonRunTerminal       = "run_terminal"
)

// AwaitMatchRecord is the per-instance audit record of one Dispatch.
type AwaitMatchRecord struct {
	InstanceKey string
	RunID       string
	Outcome     AwaitMatchOutcome
	Reason      string
	// PayloadOmitted reports that the event payload exceeded the resume
	// payload cap and the await resolved without it (the satisfied row
	// carries no inline payload; the journaled event remains the source).
	PayloadOmitted bool
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
	if m == nil || m.Store == nil || strings.TrimSpace(ev.EventID) == "" ||
		strings.TrimSpace(ev.EventType) == "" || strings.TrimSpace(ev.SubjectRef) == "" {
		return result, nil
	}
	result.SubjectKey = domain.AwaitEventKey(ev.EventType, ev.SubjectRef)
	candidates, err := m.Store.Awaits().ListAwaitsByPattern(ctx, ws, result.SubjectKey)
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return result, nil
		}
		return result, fmt.Errorf("await dispatch: list awaits for key %q in workspace %q: %w", result.SubjectKey, ws, err)
	}
	payload, omitted := capAwaitResumePayload(ev.Payload)
	timeoutTarget, isTimeout := domain.AwaitTimeoutTargetInstance(ev.EventID)
	var errs []error
	for _, inst := range candidates { // resolve ALL matching (locked decision)
		if isTimeout && inst.InstanceKey != timeoutTarget {
			// RULE 3: a synthetic timeout event resolves exactly the instance
			// it targets; co-waiters on the same pattern keep their own
			// deadlines (multi-waiter does not apply to timeouts).
			continue
		}
		record, err := m.dispatchOne(ctx, ws, ev, inst, payload, omitted)
		result.Records = append(result.Records, record)
		errs = append(errs, err)
	}
	return result, errors.Join(errs...)
}

// dispatchOne runs the RULE 4 actor predicate, the atomic resolve and the
// resume for a single candidate instance.
func (m *AwaitMatcher) dispatchOne(ctx context.Context, ws string, ev AwaitDispatchEvent, inst *domain.AwaitInstance, payload json.RawMessage, omitted bool) (AwaitMatchRecord, error) {
	record := AwaitMatchRecord{InstanceKey: inst.InstanceKey, RunID: inst.RunID, PayloadOmitted: omitted}
	if !m.dispatchActorAllowed(ev, inst) {
		record.Outcome, record.Reason = AwaitMatchActorRejected, AwaitReasonActorRejected
		record.PayloadOmitted = false // nothing fed, nothing to omit
		m.auditActorRejected(ws, ev, inst)
		return record, nil
	}
	resolution, err := m.Store.Awaits().ResolveAwait(ctx, ws, inst.InstanceKey, ev.EventID, payload, ev.ActorRef)
	if err != nil {
		record.Outcome, record.Reason = AwaitMatchFailed, err.Error()
		return record, fmt.Errorf("await dispatch: resolve %q by event %q: %w", inst.InstanceKey, ev.EventID, err)
	}
	if !resolution.Resume {
		// First event won earlier; this dispatch is the recorded no-op loser
		// and must not touch the run (the winner owns the resume).
		record.Outcome = AwaitMatchAlreadyResolved
		return record, nil
	}
	record.Outcome, record.Reason = m.resumeRun(ctx, ws, inst, ev.EventID)
	if record.Outcome != AwaitMatchResolved {
		m.auditResumeOutcome(ws, ev, inst, record)
	}
	if record.Outcome == AwaitMatchFailed {
		return record, fmt.Errorf("await dispatch: %q resolved by event %q but run %q resume failed: %s",
			inst.InstanceKey, ev.EventID, inst.RunID, record.Reason)
	}
	return record, nil
}

// resumeRun re-queues the suspended run, tolerating the park->suspend window:
// a still-running run is retried within the budget (memstore shape), a
// pending-resume marker (fleet-db shape) and a lost resume race are recorded
// deferred no-ops. The resolution stands in every non-failed branch.
func (m *AwaitMatcher) resumeRun(ctx context.Context, ws string, inst *domain.AwaitInstance, eventID string) (AwaitMatchOutcome, string) {
	retries, delay := m.resumeRetryBudget()
	for attempt := 0; ; attempt++ {
		_, err := m.Store.DriverRuns().ResumeAwaiting(ctx, ws, inst.RunID, inst.InstanceKey, eventID)
		switch {
		case err == nil:
			return AwaitMatchResolved, ""
		case errors.Is(err, domain.ErrDriverRunAlreadyResumed):
			return AwaitMatchResumeDeferred, AwaitReasonRunAlreadyResumed
		case errors.Is(err, domain.ErrInvalidTransition):
			reason, retry := m.classifyResumeBlock(ctx, ws, inst.RunID)
			if !retry || attempt >= retries || !sleepAwaitResumeRetry(ctx, delay) {
				return AwaitMatchResumeDeferred, reason
			}
		default:
			return AwaitMatchFailed, fmt.Sprintf("resume run: %v", err)
		}
	}
}

// classifyResumeBlock inspects a run whose resume hit ErrInvalidTransition
// (every blocked shape is a deferred resolution): queued means a racing
// resume already won, terminal means the run finished before the event
// landed, running means the accepted park->suspend window — the only
// retryable shape.
func (m *AwaitMatcher) classifyResumeBlock(ctx context.Context, ws, runID string) (reason string, retry bool) {
	run, err := m.Store.DriverRuns().Get(ctx, ws, runID)
	if err != nil {
		return fmt.Sprintf("inspect blocked run: %v", err), false
	}
	switch {
	case run.Status == domain.DriverRunQueued:
		return AwaitReasonRunAlreadyResumed, false
	case run.Status.IsTerminal():
		return AwaitReasonRunTerminal, false
	default:
		// running (park->suspend window) or transiently re-suspended: retry.
		return AwaitReasonParkWindowPending, true
	}
}

func (m *AwaitMatcher) resumeRetryBudget() (int, time.Duration) {
	retries, delay := m.ResumeRetries, m.ResumeRetryDelay
	if retries <= 0 {
		retries = defaultAwaitResumeRetries
	}
	if delay <= 0 {
		delay = defaultAwaitResumeRetryDelay
	}
	return retries, delay
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

// capAwaitResumePayload enforces the resume payload size cap at dispatch
// time: an oversized payload is omitted (the await still resolves; the
// journaled event remains the payload source) instead of failing the resolve
// and stranding the await.
func capAwaitResumePayload(payload json.RawMessage) (json.RawMessage, bool) {
	if len(payload) <= domain.DefaultAwaitResumePayloadCap {
		return payload, false
	}
	return nil, true
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

// sleepAwaitResumeRetry waits one retry interval, aborting on ctx
// cancellation.
func sleepAwaitResumeRetry(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
