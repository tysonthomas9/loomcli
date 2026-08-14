package driver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

type RunOptions struct {
	WorkspaceKey     string
	DriverID         string
	DriverVersionID  string
	EpicID           string
	RunID            string
	IdempotencyKey   string
	Entrypoint       string
	SourceKind       string
	SourceRef        string
	TriggerBindingID string
	AgentServiceID   string
	Payload          json.RawMessage
}

func CreateDriverRun(ctx context.Context, s store.Store, opts RunOptions) (*domain.DriverRun, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(opts.WorkspaceKey) == "" || strings.TrimSpace(opts.DriverID) == "" {
		return nil, fmt.Errorf("workspace key and driver id required: %w", domain.ErrInvalid)
	}
	driver, version, err := resolveDriverRunVersion(ctx, s, opts.WorkspaceKey, opts.DriverID, opts.DriverVersionID)
	if err != nil {
		return nil, err
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}
	entrypoint := opts.Entrypoint
	if entrypoint == "" {
		entrypoint = EntrypointRun
	}
	payload := clonePayload(opts.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("payload must be valid JSON: %w", domain.ErrInvalid)
	}
	sourceKind := strings.TrimSpace(opts.SourceKind)
	if sourceKind == "" {
		sourceKind = "cli"
	}
	sourceRef := strings.TrimSpace(opts.SourceRef)
	if sourceRef == "" {
		sourceRef = "loom driver run"
	}
	return s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:     opts.WorkspaceKey,
		RunID:            runID,
		DriverID:         driver.DriverID,
		DriverVersionID:  version.VersionID,
		Entrypoint:       entrypoint,
		SourceKind:       sourceKind,
		SourceRef:        sourceRef,
		EpicID:           opts.EpicID,
		TriggerBindingID: opts.TriggerBindingID,
		AgentServiceID:   opts.AgentServiceID,
		IdempotencyKey:   opts.IdempotencyKey,
		Payload:          payload,
	})
}

func activeDriverVersion(ctx context.Context, s store.Store, workspaceKey, driverID string) (*domain.Driver, *domain.DriverVersion, error) {
	driver, err := s.Drivers().Get(ctx, workspaceKey, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver: %w", err)
	}
	if driver.ActiveVersionID == "" {
		return nil, nil, fmt.Errorf("driver %q has no active version: %w", driverID, domain.ErrInvalid)
	}
	version, err := s.DriverVersions().Get(ctx, workspaceKey, driver.ActiveVersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get active driver version: %w", err)
	}
	if version.DriverID != driver.DriverID || version.ValidationStatus != domain.DriverVersionValidationPassed {
		return nil, nil, fmt.Errorf("driver %q active version %q is not a passed version: %w", driver.DriverID, driver.ActiveVersionID, domain.ErrInvalid)
	}
	return driver, version, nil
}

func resolveDriverRunVersion(ctx context.Context, s store.Store, workspaceKey, driverID, versionID string) (*domain.Driver, *domain.DriverVersion, error) {
	if strings.TrimSpace(versionID) == "" {
		return activeDriverVersion(ctx, s, workspaceKey, driverID)
	}
	driver, err := s.Drivers().Get(ctx, workspaceKey, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver: %w", err)
	}
	version, err := s.DriverVersions().Get(ctx, workspaceKey, strings.TrimSpace(versionID))
	if err != nil {
		return nil, nil, fmt.Errorf("get driver version: %w", err)
	}
	if version.DriverID != driver.DriverID || version.ValidationStatus != domain.DriverVersionValidationPassed {
		return nil, nil, fmt.Errorf("driver %q version %q is not a passed version for this driver: %w", driver.DriverID, version.VersionID, domain.ErrInvalid)
	}
	return driver, version, nil
}

func clonePayload(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// VerifyRunningDriverRun loads the parent DriverRun and proves the caller may
// act on its behalf. The run must be running; when it is locked (lease or
// fencing token set) the caller's owner credentials are verified through a
// fenced heartbeat, so a stale executor can never act after losing the lease.
// fencingToken is a resolver so callers with lazily-parsed credentials only
// pay (and surface) the parse when the run is actually locked. Shared by the
// driver CLI subcommands and the driver-op HTTP API.
func VerifyRunningDriverRun(ctx context.Context, st store.Store, ws, runID, nodeID, leaseID string, fencingToken func() (int64, error)) (*domain.DriverRun, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("driver-run-id required: %w", domain.ErrInvalid)
	}
	parent, err := st.DriverRuns().Get(ctx, ws, runID)
	if err != nil {
		return nil, fmt.Errorf("get parent driver run: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q is %s, want running: %w", runID, parent.Status, domain.ErrInvalidTransition)
	}
	if parent.LeaseID != "" || parent.FencingToken != 0 {
		ownerFence := int64(0)
		if fencingToken != nil {
			ownerFence, err = fencingToken()
			if err != nil {
				return nil, err
			}
		}
		if nodeID == "" || leaseID == "" || ownerFence == 0 {
			return nil, fmt.Errorf("driver run %q owner credentials required: %w", runID, domain.ErrNotOwner)
		}
		parent, err = st.DriverRuns().Heartbeat(ctx, ws, runID, nodeID, leaseID, ownerFence)
		if err != nil {
			return nil, fmt.Errorf("verify driver run owner: %w", err)
		}
	}
	return parent, nil
}

// DriverRunActor is the store actor identity a driver run acts as.
func DriverRunActor(runID string) string {
	return "driver-run:" + runID
}

// DriverRunPayloadEpicID extracts the epicId field from a driver run payload,
// returning "" when absent or unparseable.
func DriverRunPayloadEpicID(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	value, ok := object["epicId"].(string)
	if !ok {
		return ""
	}
	return value
}

const (
	// RunTokenSigningKeyEnv names the env var holding the hex-encoded
	// 32-byte HS256 signing key for run tokens. When unset, an ephemeral
	// per-process key is generated: tokens then die with the serve process,
	// which also kills the runs they were minted for (single-instance
	// T0-T2). Multi-replica deployments must set this var (or move to the
	// fleet SigningKeyManager Redis pattern).
	RunTokenSigningKeyEnv = "LOOM_RUN_TOKEN_SIGNING_KEY" //nolint:gosec // env var name, not a credential

	// RunTokenTTLEnv names the env var overriding the run token TTL. The
	// TTL equals the maximum run duration: expiry doubles as the hard
	// run-duration cap. Revocation happens via fenced run verification
	// regardless of expiry, so the TTL only bounds how long a stolen token
	// stays parseable.
	RunTokenTTLEnv = "LOOM_RUN_TOKEN_TTL" //nolint:gosec // env var name, not a credential

	// DefaultRunTokenTTL is the default maximum run duration.
	DefaultRunTokenTTL = 24 * time.Hour

	// runTokenKeyLen is the required signing key length in bytes.
	runTokenKeyLen = 32
)

// ErrRunTokenInvalid indicates a run token failed validation (bad signature,
// wrong algorithm, expired, malformed, or inconsistent claims). It wraps
// domain.ErrNotOwner: presenting a token that does not prove run identity is
// an ownership failure.
var ErrRunTokenInvalid = fmt.Errorf("driver: run token invalid: %w", domain.ErrNotOwner)

// RunTokenClaims bind a bearer token to one DriverRun for one lease window.
// A stolen token is therefore bounded to a single run and rejected once the
// lease moves on (fenced verification) or the TTL — the maximum run duration
// — elapses. Tokens are stateless and never persisted; idempotent re-claims
// mint fresh ones.
type RunTokenClaims struct {
	WorkspaceKey string `json:"workspaceKey"`
	RunID        string `json:"runId"`
	NodeID       string `json:"nodeId"`
	LeaseID      string `json:"leaseId"`
	FencingToken int64  `json:"fencingToken"`

	// Caps is reserved for future capability scoping. Empty means the full
	// current driver-op surface; capability enforcement stays with
	// connector grants for now.
	Caps []string `json:"caps,omitempty"`

	jwt.RegisteredClaims
}

// MintRunToken signs claims as an HS256 JWT with Subject set to
// DriverRunActor(claims.RunID) so the store actor identity travels in the
// token. IssuedAt and ExpiresAt are always stamped; ttl must be positive.
func MintRunToken(claims RunTokenClaims, key []byte, ttl time.Duration) (string, error) {
	if strings.TrimSpace(claims.RunID) == "" {
		return "", fmt.Errorf("mint run token: run id required: %w", domain.ErrInvalid)
	}
	if len(key) == 0 {
		return "", fmt.Errorf("mint run token: signing key required: %w", domain.ErrInvalid)
	}
	if ttl <= 0 {
		return "", fmt.Errorf("mint run token: ttl must be positive, got %s: %w", ttl, domain.ErrInvalid)
	}
	now := time.Now()
	claims.Subject = DriverRunActor(claims.RunID)
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("mint run token: sign: %w", err)
	}
	return signed, nil
}

// ParseRunToken validates an HS256 run token and returns its claims. The
// algorithm is pinned to HS256 (alg-confusion rejected), expiry is required,
// and the Subject must match DriverRunActor(RunID). Callers must still pass
// the claims through fenced run verification — parsing only proves the token
// was minted by this serve, not that the lease is still live.
func ParseRunToken(token string, key []byte) (*RunTokenClaims, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("parse run token: signing key required: %w", domain.ErrInvalid)
	}
	parsed, err := jwt.ParseWithClaims(token, &RunTokenClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRunTokenInvalid, err)
	}
	claims, ok := parsed.Claims.(*RunTokenClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("%w: claims missing", ErrRunTokenInvalid)
	}
	if strings.TrimSpace(claims.RunID) == "" {
		return nil, fmt.Errorf("%w: run id claim empty", ErrRunTokenInvalid)
	}
	if claims.Subject != DriverRunActor(claims.RunID) {
		return nil, fmt.Errorf("%w: subject %q does not match run %q", ErrRunTokenInvalid, claims.Subject, claims.RunID)
	}
	return claims, nil
}

// IsRunTokenExpired reports whether a ParseRunToken failure means the token
// was correctly signed but past its expiry (jwt/v5 verifies the signature
// before validating claims, so an expired-token failure proves authenticity).
// Lets callers surface a distinct "token_expired" signal without importing
// the jwt package.
func IsRunTokenExpired(err error) bool {
	return errors.Is(err, jwt.ErrTokenExpired)
}

// ephemeralRunTokenKey generates the per-process fallback signing key once.
var ephemeralRunTokenKey = sync.OnceValues(func() ([]byte, error) {
	key := make([]byte, runTokenKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate ephemeral run token signing key: %w", err)
	}
	return key, nil
})

// ResolveRunTokenSigningKey returns the HS256 signing key for run tokens:
// LOOM_RUN_TOKEN_SIGNING_KEY (hex, 32 bytes) when set, otherwise a stable
// ephemeral per-process key.
func ResolveRunTokenSigningKey() ([]byte, error) {
	if encoded := strings.TrimSpace(os.Getenv(RunTokenSigningKeyEnv)); encoded != "" {
		return decodeRunTokenSigningKey(encoded)
	}
	return ephemeralRunTokenKey()
}

func decodeRunTokenSigningKey(encoded string) ([]byte, error) {
	key, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%s: decode hex: %v: %w", RunTokenSigningKeyEnv, err, domain.ErrInvalid)
	}
	if len(key) != runTokenKeyLen {
		return nil, fmt.Errorf("%s: key is %d bytes, want %d: %w", RunTokenSigningKeyEnv, len(key), runTokenKeyLen, domain.ErrInvalid)
	}
	return key, nil
}

// RunTokenTTL returns the run token TTL (= maximum run duration):
// LOOM_RUN_TOKEN_TTL when set (Go duration, must be positive), otherwise
// DefaultRunTokenTTL.
func RunTokenTTL() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(RunTokenTTLEnv))
	if raw == "" {
		return DefaultRunTokenTTL, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: parse duration %q: %v: %w", RunTokenTTLEnv, raw, err, domain.ErrInvalid)
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("%s: ttl must be positive, got %s: %w", RunTokenTTLEnv, ttl, domain.ErrInvalid)
	}
	return ttl, nil
}

// run.finished lifecycle emission (ARCHITECTURE-PROPOSAL §7 step 8, chunk
// AW6). loom serve is the publisher: every server-side DriverRun terminal
// transition — finish (completed/failed/needs_review/cancelled) and
// stale-sweep recovery — emits one run.finished event so composition awaits
// (pattern "run.finished:{childRunId}", AW10) have a matchable,
// journal-backed record.
//
// The emission is best-effort-but-journaled-first:
//
//  1. JOURNAL: the event is appended directly to the trigger-event journal
//     (store.TriggerEventAppender) that the await registration scan reads
//     (RULE 2). A child that finishes before its parent registers the await
//     is found by that scan — the "already-terminal child resolves
//     immediately" guarantee — and the append is UNCONDITIONAL: neither
//     binding configuration nor the loop guard can suppress composition.
//     The dispatch-time await matcher (AW7) hooks in right after this append.
//  2. LOOPBACK: the event then feeds the C14 internal loopback (route key
//     "internal.run.finished") for binding fan-out. Bindings opt in
//     explicitly via the internal.* namespace, and the structural guard
//     applies: origin=system (server-originated lifecycle, never
//     workflow-forged) with C19 hop-depth stamping — a run admitted by a
//     trigger event emits run.finished at the admitting event's depth + 1,
//     capped, so internal.run.finished bindings cannot recursively amplify.
//
// Both steps are best-effort: failures are logged and never fail the
// transition (the watch reconciliation + stale sweeps converge state; a
// re-finish replays the deterministic event ID idempotently).

// RunFinishedEventType is the lifecycle event type terminal DriverRun
// transitions emit. Already normalized (NormalizeInternalEventType is a
// no-op on it), so the journaled type and the loopback route suffix match.
const RunFinishedEventType = "run.finished"

// runFinishedActor is the ActorRef stamped on run.finished events: the
// server itself. Composition awaits use no actor predicate (AW10's
// actor=system carve-out).
const runFinishedActor = "system"

// runFinishedSourceKind marks journal records produced by the lifecycle
// lane rather than an ingress connector.
const runFinishedSourceKind = "internal"

// RunFinishedEventID is the deterministic event ID for a terminal
// transition: "run-finished:{runID}:{status}". Deterministic so a re-run of
// the finish path (double-finish, sweep retry) re-emits idempotently — the
// journal append and the loopback idempotency key both dedup on it.
func RunFinishedEventID(runID string, status domain.DriverRunStatus) string {
	return "run-finished:" + runID + ":" + string(status)
}

// RunFinishedSubjectKey renders the await-matchable subject key for a run's
// terminal event — the exact pattern composition awaits register
// (domain.AwaitEventKey over the run.finished type and the run ID).
func RunFinishedSubjectKey(runID string) string {
	return domain.AwaitEventKey(RunFinishedEventType, runID)
}

// runFinishedPayload is the camelCase driver-wire payload of a run.finished
// event: enough for a resumed parent to branch on the child's outcome
// without a second fetch.
type runFinishedPayload struct {
	RunID       string `json:"runId"`
	Status      string `json:"status"`
	Summary     string `json:"summary,omitempty"`
	ErrorClass  string `json:"errorClass,omitempty"`
	ParentRunID string `json:"parentRunId,omitempty"`
}

// emitRunFinishedEvent publishes one terminal transition: journal-first
// append, then internal-loopback dispatch. Nil-safe and best-effort; a
// non-terminal (or suspended) run is ignored. src may be nil — a zero-config
// loopback over the same store is used; passing the serve-shared
// InternalSource keeps the hop-depth ledger warm across emissions.
func emitRunFinishedEvent(ctx context.Context, s store.Store, src *trigger.InternalSource, run *domain.DriverRun) {
	if s == nil || run == nil || !run.Status.IsTerminal() {
		return
	}
	if src == nil {
		src = &trigger.InternalSource{Store: s}
	}
	eventID := RunFinishedEventID(run.RunID, run.Status)
	parentEventID, hopDepth := runFinishedProvenance(ctx, s, src, run)
	payload := marshalRunFinishedPayload(ctx, run)
	appendRunFinishedJournal(ctx, s, run, eventID, hopDepth)
	dispatchRunFinishedAwaits(ctx, s, run, eventID, payload)
	emitRunFinishedLoopback(ctx, src, run, eventID, parentEventID, payload)
}

// dispatchRunFinishedAwaits runs the dispatch-time await matcher (AW7)
// directly off the journaled lifecycle event, BEFORE the loopback:
// composition awaits (pattern "run.finished:{runID}") resolve even when no
// internal.* binding listens or the hop-depth guard suppresses binding
// fan-out — matching the unconditional journal append above. When the
// loopback does dispatch, its own matcher pass replays as an idempotent
// no-op (the await is already terminal). Best-effort like every leg here.
func dispatchRunFinishedAwaits(ctx context.Context, s store.Store, run *domain.DriverRun, eventID string, payload json.RawMessage) {
	matcher := &trigger.AwaitMatcher{Store: s}
	if _, err := matcher.Dispatch(ctx, run.WorkspaceKey, trigger.AwaitDispatchEvent{
		EventID:    eventID,
		EventType:  RunFinishedEventType,
		SubjectRef: run.RunID,
		ActorRef:   runFinishedActor,
		Payload:    payload,
	}); err != nil {
		slog.WarnContext(ctx, "run.finished await dispatch failed",
			"runID", run.RunID, "status", string(run.Status), "error", err)
	}
}

// marshalRunFinishedPayload encodes the resume/fan-out payload; nil (with a
// log record) on the never-expected marshal failure so the lifecycle event
// still propagates without it.
func marshalRunFinishedPayload(ctx context.Context, run *domain.DriverRun) json.RawMessage {
	payload, err := json.Marshal(runFinishedPayload{
		RunID:       run.RunID,
		Status:      string(run.Status),
		Summary:     run.Summary,
		ErrorClass:  run.ErrorClass,
		ParentRunID: run.ParentRunID,
	})
	if err != nil {
		slog.WarnContext(ctx, "encode run.finished payload failed", "runID", run.RunID, "error", err)
		return nil
	}
	return payload
}

// runFinishedProvenance derives the C19 chain provenance for a run's
// terminal event. A run admitted by the trigger dispatch path carries the
// admitting event's ID in SourceRef; when that resolves to a persisted
// trigger event the run.finished continues its chain at depth parent+1.
// Anything else (CLI runs, epic runs, free-form source refs) is a depth-0
// system root.
func runFinishedProvenance(ctx context.Context, s store.Store, src *trigger.InternalSource, run *domain.DriverRun) (string, int) {
	parentEventID := strings.TrimSpace(run.SourceRef)
	if parentEventID == "" {
		return "", 0
	}
	if _, err := s.TriggerEvents().Get(ctx, run.WorkspaceKey, parentEventID); err != nil {
		return "", 0
	}
	return parentEventID, src.ChainHopDepth(ctx, run.WorkspaceKey, parentEventID) + 1
}

// appendRunFinishedJournal writes the journal record the await registration
// scan matches (subject key RunFinishedSubjectKey). Unconditional with
// respect to the loop guard: the stamped HopDepth may exceed the cap — the
// cap suppresses binding fan-out, never await visibility. Backends without
// the appender capability (fleet-db client) journal server-side in their
// dispatch wiring instead (IndexAwaitEvent, AW2/AW7).
func appendRunFinishedJournal(ctx context.Context, s store.Store, run *domain.DriverRun, eventID string, hopDepth int) {
	appender, ok := s.TriggerEvents().(store.TriggerEventAppender)
	if !ok {
		slog.DebugContext(ctx, "run.finished journal append skipped: backend journals server-side",
			"runID", run.RunID)
		return
	}
	now := time.Now().UTC()
	occurredAt := now
	if run.FinishedAt != nil && !run.FinishedAt.IsZero() {
		occurredAt = run.FinishedAt.UTC()
	}
	_, err := appender.AppendTriggerEvent(ctx, &domain.TriggerEvent{
		WorkspaceKey:    run.WorkspaceKey,
		EventID:         eventID,
		SourceKind:      runFinishedSourceKind,
		SourceEventID:   eventID,
		EventType:       RunFinishedEventType,
		SubjectRef:      run.RunID,
		ActorRef:        runFinishedActor,
		Origin:          domain.TriggerEventOriginSystem,
		HopDepth:        hopDepth,
		OccurredAt:      occurredAt,
		ReceivedAt:      now,
		IdempotencyKey:  trigger.InternalEventIdempotencyKey(run.WorkspaceKey, eventID),
		SignatureStatus: "internal",
	})
	if err != nil {
		slog.WarnContext(ctx, "append run.finished journal event failed",
			"runID", run.RunID, "status", string(run.Status), "error", err)
	}
}

// emitRunFinishedLoopback feeds the terminal event into the C14 loopback for
// binding fan-out. "Nobody listening" (domain.ErrNotFound) is the normal
// case and logged at debug; a guard drop was already audited by the source.
func emitRunFinishedLoopback(ctx context.Context, src *trigger.InternalSource, run *domain.DriverRun, eventID, parentEventID string, payload json.RawMessage) {
	_, err := src.Emit(ctx, run.WorkspaceKey, trigger.InternalEvent{
		EventID:       eventID,
		EventType:     RunFinishedEventType,
		Origin:        domain.TriggerEventOriginSystem,
		ParentEventID: parentEventID,
		SubjectRef:    run.RunID,
		ActorRef:      runFinishedActor,
		EpicID:        run.EpicID,
		Payload:       payload,
	})
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrNotFound):
		slog.DebugContext(ctx, "run.finished loopback: no internal binding listening",
			"runID", run.RunID, "status", string(run.Status))
	default:
		slog.WarnContext(ctx, "run.finished loopback dispatch failed",
			"runID", run.RunID, "status", string(run.Status), "error", err)
	}
}

const (
	PatchBackApplied         = "applied"
	PatchBackBaseUnreachable = "base_ref_unreachable"
	PatchBackBaseMismatch    = "base_ref_mismatch"
	PatchBackConflict        = "patch_conflict"
	PatchBackApplyFailed     = "patch_apply_failed"
)

type PatchBackOptions struct {
	WorktreePath string
	BaseRef      string
	Patch        []byte
	// Exclude lists path patterns passed to `git apply --exclude` — used by the daemon leaf to drop
	// monitor bookkeeping (e.g. .agent.lock.flock) that exists in both the runner's isolated worktree
	// and the daemon's host worktree and would otherwise conflict on apply. The driver host-bridge
	// patches a freshly-provisioned worktree, so it passes none.
	Exclude []string
	// Index applies the patch with `git apply --index`, staging exactly the patch's (non-excluded)
	// files. The daemon leaf sets this so a follow-up `git commit` records only the agent's change,
	// not unrelated working-tree noise (the monitor's .agent.lock). The driver host-bridge leaves it
	// false (apply to the working tree only, as before).
	Index bool
}

type PatchBackResult struct {
	Status         string `json:"status"`
	Applied        bool   `json:"applied"`
	PreservePatch  bool   `json:"preservePatch"`
	BaseRef        string `json:"baseRef,omitempty"`
	BaseSHA        string `json:"baseSha,omitempty"`
	CurrentHEAD    string `json:"currentHead,omitempty"`
	ErrorClass     string `json:"errorClass,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	PreservedPatch []byte `json:"-"`
}

//nolint:funlen // Patch-back must keep validation, merge-base checks, apply, and preservation metadata in one transaction.
func ApplyPatchBack(ctx context.Context, opts PatchBackOptions) (*PatchBackResult, error) {
	opts.WorktreePath = strings.TrimSpace(opts.WorktreePath)
	opts.BaseRef = strings.TrimSpace(opts.BaseRef)
	if opts.WorktreePath == "" || opts.BaseRef == "" || len(bytes.TrimSpace(opts.Patch)) == 0 {
		return nil, fmt.Errorf("worktree path, base ref, and patch required: %w", domain.ErrInvalid)
	}
	result := &PatchBackResult{BaseRef: opts.BaseRef}
	baseSHA, baseErr := gitOutput(ctx, opts.WorktreePath, nil, "rev-parse", "--verify", opts.BaseRef+"^{commit}")
	if baseErr != nil {
		result.Status = PatchBackBaseUnreachable
		result.PreservePatch = true
		result.ErrorClass = PatchBackBaseUnreachable
		result.ErrorMessage = strings.TrimSpace(baseErr.Error())
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	result.BaseSHA = strings.TrimSpace(baseSHA)
	headSHA, err := gitOutput(ctx, opts.WorktreePath, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve worktree HEAD: %w", err)
	}
	result.CurrentHEAD = strings.TrimSpace(headSHA)
	if result.CurrentHEAD != result.BaseSHA {
		result.Status = PatchBackBaseMismatch
		result.PreservePatch = true
		result.ErrorClass = PatchBackBaseMismatch
		result.ErrorMessage = fmt.Sprintf("worktree HEAD %s does not match patch base %s", result.CurrentHEAD, result.BaseSHA)
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	var excludeArgs []string
	for _, ex := range opts.Exclude {
		if strings.TrimSpace(ex) != "" {
			excludeArgs = append(excludeArgs, "--exclude="+ex)
		}
	}
	checkArgs := append([]string{"apply", "--check"}, excludeArgs...)
	applyArgs := append([]string{"apply"}, excludeArgs...)
	if opts.Index {
		applyArgs = append(applyArgs, "--index")
	}
	if _, err := gitOutput(ctx, opts.WorktreePath, opts.Patch, checkArgs...); err != nil {
		result.Status = PatchBackConflict
		result.PreservePatch = true
		result.ErrorClass = PatchBackConflict
		result.ErrorMessage = strings.TrimSpace(err.Error())
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	if _, err := gitOutput(ctx, opts.WorktreePath, opts.Patch, applyArgs...); err != nil {
		result.Status = PatchBackApplyFailed
		result.PreservePatch = true
		result.ErrorClass = PatchBackApplyFailed
		result.ErrorMessage = strings.TrimSpace(err.Error())
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	result.Status = PatchBackApplied
	result.Applied = true
	return result, nil
}

// CommitWorktree commits the ALREADY-STAGED changes in worktreePath with a fixed loom
// identity. The daemon TS leaf calls this after ApplyPatchBack{Index:true} (which staged
// exactly the agent's patched files) so the change lands on the worktree HEAD and the
// session finalize's `git diff beforeRef..HEAD` captures it — the Go leaf gets the same
// effect from the agent committing in place. It intentionally does NOT `git add -A`, so
// unrelated working-tree noise (the monitor's .agent.lock) is not folded into the commit.
func CommitWorktree(ctx context.Context, worktreePath, message string) error {
	if strings.TrimSpace(worktreePath) == "" {
		return fmt.Errorf("worktree path required: %w", domain.ErrInvalid)
	}
	if _, err := gitOutput(ctx, worktreePath, nil, "-c", "user.name=loom", "-c", "user.email=loom@local", "commit", "-m", message); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed executable; args are controlled by driver patch-back code.
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s: %w", msg, err)
	}
	return stdout.String(), nil
}
