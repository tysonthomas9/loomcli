package execution

// Await-event store contract (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW1).
//
// Locked decisions baked into this contract:
//   - Multi-waiter: one event resolves ALL pending awaits whose Pattern
//     equals the event's rendered subject key (exact equality, no glob).
//   - Atomic register-and-check (RULE 2): RegisterAwaitAndCheck is the only
//     registration entry point — one call, one transaction. There are no
//     separate check/register methods. The immediate-satisfaction scan covers
//     the trigger-event journal plus run.finished events.
//   - Timeout: ListDueAwaitDeadlines feeds a sweeper that resumes runs
//     with a synthetic timeout event (resume-with-timeout-event).
//   - Replay: the resume payload is persisted on the satisfied row
//     (size-capped, see DefaultAwaitResumePayloadCap) and returned
//     inline by GetSatisfiedAwait when a finished await is re-executed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AwaitRegistration is the input to AwaitStore.RegisterAwaitAndCheck. The
// workspace key is the method parameter, not a field.
type AwaitRegistration struct {
	// InstanceKey is the canonical runID#await-{n} identity (RULE 3).
	InstanceKey string
	RunID       string
	// Pattern is the fully rendered subject-scoped key to wait for
	// (RULE 1); matched by exact string equality only.
	Pattern string
	// ActorAllow is the persisted eligible-actor predicate (RULE 4;
	// enforcement at resolve time, AW7). Empty means any actor.
	ActorAllow []string
	// Deadline is mandatory and must be in the future (RULE 5).
	Deadline time.Time
	// RegisteredAt zero means the store stamps its own clock.
	RegisteredAt time.Time
}

// Instance materializes the pending domain row for this registration and
// validates the registration invariants against now. Implementations call
// this at the top of RegisterAwaitAndCheck so every backend rejects invalid
// registrations identically.
func (r AwaitRegistration) Instance(workspaceKey string, now time.Time) (*AwaitInstance, error) {
	registeredAt := r.RegisteredAt
	if registeredAt.IsZero() {
		registeredAt = now
	}
	inst := &AwaitInstance{
		WorkspaceKey: workspaceKey,
		InstanceKey:  r.InstanceKey,
		RunID:        r.RunID,
		Pattern:      r.Pattern,
		ActorAllow:   append([]string(nil), r.ActorAllow...),
		Deadline:     r.Deadline,
		RegisteredAt: registeredAt,
		Status:       AwaitPending,
	}
	if err := inst.ValidateAt(now); err != nil {
		return nil, err
	}
	return inst, nil
}

// AwaitRegistrationResult is the persistence result of RegisterAwaitAndCheck.
// It is distinct from AwaitResult, the application command result returned by
// Execution's public API.
type AwaitRegistrationResult struct {
	// Instance is the persisted row: Status pending when pending, or
	// satisfied (with SatisfiedByEventID/SatisfiedPayload populated) when
	// an already-journaled event matched at registration time.
	Instance *AwaitInstance
	// Satisfied reports immediate satisfaction; false means pending and the
	// caller should suspend the run.
	Satisfied bool
}

// AwaitResolution is the resume decision returned by ResolveAwait.
type AwaitResolution struct {
	// Instance is the post-resolution row.
	Instance *AwaitInstance
	// Resume is true when this call transitioned the await out of pending —
	// the caller owns resuming the suspended run. False means the await was
	// already terminal (idempotent replay) and someone else resumed it.
	Resume bool
}

// AwaitStore is the durable await-event registry every backend (memstore,
// fleetdb) must satisfy.
//
// Errors wrap the sentinels owned by Execution; the
// await-specific validation sentinels (ErrAwaitPatternUnscoped,
// ErrAwaitTimeoutRequired, ErrAwaitInstanceKeyMalformed) all
// wrap ErrInvalid before any persistence adapter is invoked.
type AwaitStore interface {
	// RegisterAwaitAndCheck atomically checks for an already-matching event
	// and otherwise leaves the await pending (RULE 2: one call, one transaction).
	// Idempotent on InstanceKey: re-registering an existing key returns the
	// current row without writing a duplicate.
	RegisterAwaitAndCheck(ctx context.Context, workspaceKey string, in AwaitRegistration) (*AwaitRegistrationResult, error)

	// ResolveAwait marks one pending await satisfied by eventID, persisting
	// the size-capped resume payload and the verified actor's identity
	// check (ActorAllow enforcement, AW7) on the row. The deadline sweeper
	// resolves due awaits through this same path with a synthetic timeout
	// event, landing them in timed_out instead of satisfied. Resolving an
	// already-terminal await returns Resume=false (idempotent).
	ResolveAwait(ctx context.Context, workspaceKey, instanceKey, eventID string, payload json.RawMessage, actor string) (*AwaitResolution, error)

	// ListAwaitsByPattern returns ALL pending awaits whose Pattern exactly
	// equals pattern (multi-waiter decision), ordered by RegisteredAt
	// ascending.
	ListAwaitsByPattern(ctx context.Context, workspaceKey, pattern string) ([]*AwaitInstance, error)

	// ListDueAwaitDeadlines returns pending awaits whose Deadline is at or
	// before before, ordered by Deadline ascending, capped at limit
	// (limit <= 0 means implementation default).
	ListDueAwaitDeadlines(ctx context.Context, workspaceKey string, before time.Time, limit int) ([]*AwaitInstance, error)

	// GetSatisfiedAwait returns the satisfied (or timed_out) row for
	// instanceKey with its persisted resume payload inline — the replay
	// path. A missing or still-pending instance wraps persistence.ErrNotFound.
	GetSatisfiedAwait(ctx context.Context, workspaceKey, instanceKey string) (*AwaitInstance, error)
}

// AtomicAwaitStore is the generic dispatch-side resolve-and-resume command.
// It commits the await's terminal transition together with either re-queuing
// its suspended run or recording the pending-resume marker for a run that has
// not suspended yet. A same-event replay is idempotent and converges the run;
// a different event never steals a resolution that already won.
//
// eventID selects satisfied versus timed_out using the canonical synthetic
// timeout prefix. actor is the verified event actor and is checked against the
// persisted ActorAllow predicate for ordinary satisfied resolutions.
type AtomicAwaitStore interface {
	ResolveAwaitAndResume(
		ctx context.Context,
		workspaceKey, instanceKey, eventID string,
		payload json.RawMessage,
		actor string,
	) error
}

// RunOutcomeAwaitStore is the narrow atomic command required by Execution's
// durable run.finished consumer. It resolves one pending composition await and
// either re-queues its suspended parent or records the pending-resume marker in
// the same backend transaction. A same-event replay is idempotent and must
// converge any still-suspended parent before returning success.
type RunOutcomeAwaitStore interface {
	ResolveRunOutcomeAwaitAndResume(
		ctx context.Context,
		workspaceKey, instanceKey, eventID string,
		payload json.RawMessage,
	) error
}

// Fail-closed placeholder for Store implementations that have not wired
// await persistence yet (memstore lands in AW2, fleet-db in AW3). Every
// method returns an error wrapping errors.ErrUnsupported so callers can
// detect the gap via errors.Is without panicking on a nil sub-store.

func errAwaitUnsupported(backend, op string) error {
	return fmt.Errorf("%s: await store %s: %w", backend, op, errors.ErrUnsupported)
}

// UnimplementedAwaitStore is a fail-closed AwaitStore placeholder.
type UnimplementedAwaitStore struct {
	// Backend names the implementation for error messages.
	Backend string
}

var _ AwaitStore = UnimplementedAwaitStore{}

func (u UnimplementedAwaitStore) RegisterAwaitAndCheck(context.Context, string, AwaitRegistration) (*AwaitRegistrationResult, error) {
	return nil, errAwaitUnsupported(u.Backend, "register and check")
}

func (u UnimplementedAwaitStore) ResolveAwait(context.Context, string, string, string, json.RawMessage, string) (*AwaitResolution, error) {
	return nil, errAwaitUnsupported(u.Backend, "resolve")
}

func (u UnimplementedAwaitStore) ListAwaitsByPattern(context.Context, string, string) ([]*AwaitInstance, error) {
	return nil, errAwaitUnsupported(u.Backend, "list by pattern")
}

func (u UnimplementedAwaitStore) ListDueAwaitDeadlines(context.Context, string, time.Time, int) ([]*AwaitInstance, error) {
	return nil, errAwaitUnsupported(u.Backend, "list due deadlines")
}

func (u UnimplementedAwaitStore) GetSatisfiedAwait(context.Context, string, string) (*AwaitInstance, error) {
	return nil, errAwaitUnsupported(u.Backend, "get satisfied")
}
