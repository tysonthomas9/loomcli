package domain

// Await-event contracts (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW1).
//
// An AwaitInstance is one durable "wait for event X" registration made by a
// suspended workflow run. The contract rules enforced here at the type level:
//
//	RULE 1 — Pattern is a fully rendered subject-scoped key
//	         ("eventType:subject"); bare event types are rejected with
//	         ErrAwaitPatternUnscoped. Matching is exact string equality
//	         only — no glob expansion (locked decision); any '*' is a
//	         literal character.
//	RULE 3 — InstanceKey is runID + "#await-" + n with n >= 1
//	         (ErrAwaitInstanceKeyMalformed otherwise).
//	RULE 5 — Deadline is mandatory and must be in the future at
//	         registration time (ErrAwaitTimeoutRequired otherwise).
//	RULE 4 — ActorAllow is the persisted eligible-actor predicate;
//	         enforcement happens at resolve time (chunk AW7).
//
// RULE 2 (atomic register-and-check) is the shape of
// store.AwaitStore.RegisterAwaitAndCheck itself: a single transactional
// entry point, no separate check/register methods.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Await error codes are the stable structured identifiers HTTP layers map
// the sentinels below onto. Keep in sync with the matching Err* sentinel.
const (
	AwaitErrCodePatternUnscoped      = "await_pattern_unscoped"
	AwaitErrCodeTimeoutRequired      = "await_timeout_required"
	AwaitErrCodeInstanceKeyMalformed = "await_instance_key_malformed"
	// CompositionErrCodeDepthExceeded refuses a workflows/start that would
	// nest a child run deeper than the composition depth cap (AW10).
	CompositionErrCodeDepthExceeded = "composition_depth_exceeded"
)

// Structured await validation sentinels. Each wraps ErrInvalid so generic
// callers can match errors.Is(err, ErrInvalid) while await-aware callers
// match the specific rule.
var (
	// ErrAwaitPatternUnscoped (RULE 1): the await pattern is not a rendered
	// subject-scoped key — it lacks a non-empty subject segment after ':'.
	ErrAwaitPatternUnscoped = fmt.Errorf(
		"domain: %s: await pattern must be a rendered subject-scoped key: %w",
		AwaitErrCodePatternUnscoped, ErrInvalid)

	// ErrAwaitTimeoutRequired (RULE 5): the await deadline is zero or not
	// in the future. Every await must carry a real timeout; expiry resumes
	// the run with a timeout event rather than suspending it forever.
	ErrAwaitTimeoutRequired = fmt.Errorf(
		"domain: %s: await deadline is mandatory and must be in the future: %w",
		AwaitErrCodeTimeoutRequired, ErrInvalid)

	// ErrAwaitInstanceKeyMalformed (RULE 3): the instance key does not have
	// the canonical runID#await-{n} form with n >= 1.
	ErrAwaitInstanceKeyMalformed = fmt.Errorf(
		"domain: %s: await instance key must be runID#await-{n} with n >= 1: %w",
		AwaitErrCodeInstanceKeyMalformed, ErrInvalid)

	// ErrCompositionDepthExceeded: starting this child run would exceed the
	// composition depth cap (the parent-chain twin of the C19 hop-depth
	// guard) — workflows cannot recursively amplify by starting children.
	ErrCompositionDepthExceeded = fmt.Errorf(
		"domain: %s: composition depth cap reached, refusing to start a deeper child run: %w",
		CompositionErrCodeDepthExceeded, ErrInvalid)
)

// Await resolve-time sentinels (not validation errors, so they do not wrap
// ErrInvalid).
var (
	// ErrAwaitActorForbidden (RULE 4): the resolving actor is not in the
	// await's ActorAllow predicate. Backing the session-authenticated
	// approval path (vet A2); enforced at resolve time.
	ErrAwaitActorForbidden = errors.New("domain: await actor not eligible to resolve")

	// ErrDriverRunAlreadyResumed signals the suspend leg lost the accepted
	// pending->suspend window: the await resolved first and recorded a
	// pending-resume marker on the run, so the caller must NOT suspend it —
	// the run continues inline (no lost wakeup).
	ErrDriverRunAlreadyResumed = errors.New("domain: driver run already resumed for await")
)

// DefaultAwaitResumePayloadCap is the default byte cap on the resume payload
// persisted on a satisfied await row (returned inline on replay). The serve
// wiring may tune it via env in the timeout/budget chunk; stores reject or
// truncate larger payloads per their chunk contract.
const DefaultAwaitResumePayloadCap = 64 << 10 // 64 KiB

// AwaitStatus is the lifecycle of an AwaitInstance.
type AwaitStatus string

const (
	// AwaitPending — registered, pending, waiting for a matching event.
	AwaitPending AwaitStatus = "pending"
	// AwaitSatisfied — a matching event resolved the await; the resume
	// payload is persisted on the row.
	AwaitSatisfied AwaitStatus = "satisfied"
	// AwaitTimedOut — the deadline expired; the run is resumed with a
	// synthetic timeout event (resume-with-timeout-event decision).
	AwaitTimedOut AwaitStatus = "timed_out"
	// AwaitCancelled — the owning run reached a terminal status (or a
	// cancel cascade hit it) before any event matched.
	AwaitCancelled AwaitStatus = "cancelled"
)

// IsValid reports whether the status is a known AwaitStatus.
func (s AwaitStatus) IsValid() bool {
	switch s {
	case AwaitPending, AwaitSatisfied, AwaitTimedOut, AwaitCancelled:
		return true
	}
	return false
}

// IsTerminal reports whether the await can no longer be resolved.
func (s AwaitStatus) IsTerminal() bool {
	switch s {
	case AwaitSatisfied, AwaitTimedOut, AwaitCancelled:
		return true
	default:
		return false
	}
}

// AwaitInstance is one durable await-event registration owned by a workflow
// run. A run may hold several (sequential awaits use increasing n in the
// instance key); one event resolves ALL pending instances whose Pattern
// equals the event's rendered subject key (multi-waiter decision).
//
// JSON tags are camelCase: awaits travel on the loomcli driver/watch wire.
type AwaitInstance struct {
	WorkspaceKey string `json:"workspaceKey"`
	// InstanceKey is the canonical identity: RunID + "#await-" + n, n >= 1.
	InstanceKey string `json:"instanceKey"`
	RunID       string `json:"runID"`
	// Pattern is the fully rendered subject-scoped key this await matches,
	// compared by exact string equality (no glob).
	Pattern string `json:"pattern"`
	// ActorAllow lists actors eligible to resolve this await (e.g. approval
	// awaits). Empty means any actor. Persisted predicate only; enforced at
	// resolve time (AW7).
	ActorAllow []string `json:"actorAllow,omitempty"`
	// Deadline is the mandatory expiry; the deadline sweeper resumes the
	// run with a timeout event once it passes.
	Deadline     time.Time   `json:"deadline"`
	RegisteredAt time.Time   `json:"registeredAt"`
	Status       AwaitStatus `json:"status"`
	// SatisfiedByEventID is the trigger event that resolved the await
	// (or the synthetic timeout event for timed_out rows).
	SatisfiedByEventID string `json:"satisfiedByEventID,omitempty"`
	// SatisfiedActor is the verified actor that won the resolution. Keeping it
	// on the replay row makes the in-memory and FleetDB audit contracts equal.
	SatisfiedActor string `json:"satisfiedActor,omitempty"`
	// SatisfiedPayload is the size-capped resume payload persisted on the
	// satisfied row and returned inline when the await is replayed.
	SatisfiedPayload json.RawMessage `json:"satisfiedPayload,omitempty"`
	ResumedAt        *time.Time      `json:"resumedAt,omitempty"`
}

// awaitKeySep separates the run ID from the await ordinal in InstanceKey.
const awaitKeySep = "#await-"

// AwaitInstanceKey builds the canonical instance key for the n-th await of
// a run: runID#await-{n}.
func AwaitInstanceKey(runID string, n int) string {
	return runID + awaitKeySep + strconv.Itoa(n)
}

// ParseAwaitInstanceKey splits a canonical instance key into its run ID and
// ordinal. Violations of RULE 3 (missing separator, empty run ID, n < 1,
// non-canonical digits like 01 or +1) wrap ErrAwaitInstanceKeyMalformed.
func ParseAwaitInstanceKey(key string) (runID string, n int, err error) {
	i := strings.LastIndex(key, awaitKeySep)
	if i <= 0 {
		return "", 0, fmt.Errorf("await instance key %q: %w", key, ErrAwaitInstanceKeyMalformed)
	}
	runID = key[:i]
	n, convErr := strconv.Atoi(key[i+len(awaitKeySep):])
	if convErr != nil || n < 1 || AwaitInstanceKey(runID, n) != key {
		return "", 0, fmt.Errorf("await instance key %q: %w", key, ErrAwaitInstanceKeyMalformed)
	}
	return runID, n, nil
}

// AwaitEventKey renders a journaled event's await-matching key — the exact
// string an await Pattern must equal to match (RULE 1): the same
// "eventType:subject" form ValidateAwaitPattern enforces on patterns. Every
// backend's registration scan and dispatch matcher must render event keys
// through this one function so exact-equality matching stays identical.
func AwaitEventKey(eventType, subjectRef string) string {
	return eventType + ":" + subjectRef
}

// AwaitTimeoutEventIDPrefix prefixes the synthetic event IDs the await
// deadline sweeper (AW8) resolves due awaits with. AwaitStore.ResolveAwait
// lands resolutions carrying such an event ID in AwaitTimedOut instead of
// AwaitSatisfied (resume-with-timeout-event decision); the run still resumes,
// on its timeout arm.
const AwaitTimeoutEventIDPrefix = "await-timeout-"

// IsAwaitTimeoutEventID reports whether eventID identifies a synthetic
// deadline-sweeper timeout event rather than a journaled trigger event.
func IsAwaitTimeoutEventID(eventID string) bool {
	return strings.HasPrefix(eventID, AwaitTimeoutEventIDPrefix)
}

// AwaitTimeoutActor is the synthetic ActorRef the deadline sweeper (AW8)
// stamps on timeout events. The dispatch matcher's sweeper lane treats it as
// always allowed to resolve the ONE instance its event targets — the explicit
// RULE 4 carve-out, never a general system bypass: the same actor name
// arriving on any ingress or loopback lane still faces the await's
// allow-list.
const AwaitTimeoutActor = "system:timeout"

// AwaitTimeoutEventID renders the deterministic synthetic event ID the
// deadline sweeper resolves a due instance with:
// "await-timeout-{instanceKey}". Deterministic so repeated sweep passes
// replay idempotently (the second resolve is a Resume=false no-op).
func AwaitTimeoutEventID(instanceKey string) string {
	return AwaitTimeoutEventIDPrefix + instanceKey
}

// AwaitTimeoutTargetInstance extracts the instance key a synthetic timeout
// event targets; ok=false for ordinary trigger-event IDs. RULE 3: a timeout
// resolves one specific instance, never a pattern broadly — the dispatch
// matcher skips every other candidate sharing the pattern.
func AwaitTimeoutTargetInstance(eventID string) (string, bool) {
	if !IsAwaitTimeoutEventID(eventID) {
		return "", false
	}
	return strings.TrimPrefix(eventID, AwaitTimeoutEventIDPrefix), true
}

// ValidateAwaitPattern enforces RULE 1: a pattern must be a fully rendered
// subject-scoped key — a non-empty event-type segment, then ':', then a
// non-empty subject segment. Bare event types (no ':', or nothing after it)
// wrap ErrAwaitPatternUnscoped.
func ValidateAwaitPattern(pattern string) error {
	eventType, subject, ok := strings.Cut(pattern, ":")
	if !ok || strings.TrimSpace(eventType) == "" || strings.TrimSpace(subject) == "" {
		return fmt.Errorf("await pattern %q: %w", pattern, ErrAwaitPatternUnscoped)
	}
	return nil
}

// ValidateAt checks the registration-time invariants (RULES 1, 3, 5) against
// the supplied clock. It is the write-time check: the future-deadline rule
// applies when the await is registered, not to terminal rows read back later.
func (a *AwaitInstance) ValidateAt(now time.Time) error {
	if a.WorkspaceKey == "" {
		return fmt.Errorf("await workspaceKey required: %w", ErrInvalid)
	}
	if a.RunID == "" {
		return fmt.Errorf("await runID required: %w", ErrInvalid)
	}
	runID, _, err := ParseAwaitInstanceKey(a.InstanceKey)
	if err != nil {
		return err
	}
	if runID != a.RunID {
		return fmt.Errorf("await instance key %q does not belong to run %q: %w",
			a.InstanceKey, a.RunID, ErrAwaitInstanceKeyMalformed)
	}
	if err := ValidateAwaitPattern(a.Pattern); err != nil {
		return err
	}
	if !a.Status.IsValid() {
		return fmt.Errorf("await status %q unknown: %w", a.Status, ErrInvalid)
	}
	if err := validateAwaitActorAllow(a.ActorAllow); err != nil {
		return err
	}
	return validateAwaitDeadline(a.Deadline, now)
}

// Validate is ValidateAt against the wall clock.
func (a *AwaitInstance) Validate() error { return a.ValidateAt(time.Now()) }

// validateAwaitActorAllow keeps every AwaitStore implementation on the same
// exact-match actor contract. Whitespace normalization is deliberately not
// performed: accepting a value here that can never equal a resolver identity
// would create an await that no actor can safely satisfy.
func validateAwaitActorAllow(actors []string) error {
	for _, actor := range actors {
		if strings.TrimSpace(actor) == "" || actor != strings.TrimSpace(actor) {
			return fmt.Errorf("await actorAllow entries must be non-blank canonical actor ids: %w", ErrInvalid)
		}
		if strings.ContainsFunc(actor, func(r rune) bool { return r < 0x20 }) {
			return fmt.Errorf("await actorAllow entry contains control characters: %w", ErrInvalid)
		}
	}
	return nil
}

// validateAwaitDeadline enforces RULE 5: mandatory, strictly future.
func validateAwaitDeadline(deadline, now time.Time) error {
	if deadline.IsZero() {
		return fmt.Errorf("await deadline missing: %w", ErrAwaitTimeoutRequired)
	}
	if !deadline.After(now) {
		return fmt.Errorf("await deadline %s is not in the future: %w",
			deadline.Format(time.RFC3339), ErrAwaitTimeoutRequired)
	}
	return nil
}
