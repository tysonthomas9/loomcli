package driver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/tysonthomas9/loomcli/internal/driver/eventpolicy"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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

// DeriveDriverRunLeaseToken derives the raw FleetDB lease credential from the
// same process-local key and immutable pre-claim identity used by the run
// token. The raw token is never serialized into the JWT, public results, or
// authority values; the driver API can independently re-derive it after
// validating signed claims.
func DeriveDriverRunLeaseToken(key []byte, workspaceKey, runID, nodeID, leaseID string) (string, error) {
	if len(key) == 0 || strings.TrimSpace(workspaceKey) == "" || strings.TrimSpace(runID) == "" ||
		strings.TrimSpace(nodeID) == "" || strings.TrimSpace(leaseID) == "" {
		return "", fmt.Errorf("derive DriverRun lease token: signing key and owner identity required: %w", domain.ErrInvalid)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("loom-driver-run-lease-v1\x00"))
	for _, value := range []string{workspaceKey, runID, nodeID, leaseID} {
		_, _ = mac.Write([]byte(value))
		_, _ = mac.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
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
// AW6). Every server-side terminal transition notifies Execution-owned awaits
// and publishes a narrow RunOutcome. Production composition maps that outcome
// into Automation admission; internal/driver never imports Automation or its
// application workflow. Both legs are best-effort so a publication failure
// can never roll back an already committed terminal DriverRun transition.

// RunFinishedEventType is the lifecycle event type terminal DriverRun
// transitions emit. Already normalized (NormalizeInternalEventType is a
// no-op on it), so the journaled type and the loopback route suffix match.
const RunFinishedEventType = eventpolicy.RunFinishedEventType

// RunFinishedActor is the ActorRef stamped on run.finished events: the
// server itself. Composition awaits require this exact actor in addition to
// using the reserved run.finished event type.
const RunFinishedActor = eventpolicy.RunFinishedActorRef

// AutomationEventTrustPolicy exposes Execution's concrete lifecycle-event
// policy through Automation's consumer-owned port. Composition depends on the
// driver package, never on the policy implementation subpackage directly.
func AutomationEventTrustPolicy() automation.EventTrustPolicy {
	return eventpolicy.Policy{}
}

// maxRunFinishedEventIDLength leaves room for the longest valid FleetDB
// workspace in Automation's 128-character "internal:{workspace}:{eventID}"
// idempotency key.
const maxRunFinishedEventIDLength = 86

// RunFinishedEventID is the deterministic event ID for a terminal transition.
// Existing short IDs retain "run-finished:{runID}:{status}". Opaque long run
// IDs use a collision-resistant bounded hash so publication cannot be poisoned
// by Automation's idempotency-key limit; the payload and SubjectRef still carry
// the exact run ID.
func RunFinishedEventID(runID string, status domain.DriverRunStatus) string {
	candidate := eventpolicy.RunFinishedSourceEventIDPrefix + runID + ":" + string(status)
	if len(candidate) <= maxRunFinishedEventIDLength && isSafeRunFinishedEventID(candidate) {
		return candidate
	}
	digest := sha256.Sum256([]byte(runID))
	return eventpolicy.RunFinishedSourceEventIDPrefix + "h:" + hex.EncodeToString(digest[:20]) + ":" + string(status)
}

func isSafeRunFinishedEventID(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
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
	Truncated   bool   `json:"truncated,omitempty"`
}

// emitRunFinishedEventWithExecution performs the production low-latency
// drain through Execution's system-authorized queue commands. The registered
// runtime uses the same coordinator for restart recovery.
func emitRunFinishedEventWithExecution(
	ctx context.Context,
	s store.Store,
	publisher RunOutcomePublisher,
	run *domain.DriverRun,
	queue execution.DriverRunOutcomeAPI,
	terminalWorkQueue execution.TerminalDriverRunWorkRecoveryQueueAPI,
	cascades execution.DriverRunAPI,
	authorities execution.SystemAuthorityResolver,
	notifier RunOutcomeAwaitNotifier,
) {
	if s == nil || run == nil || !run.Status.IsTerminal() {
		return
	}
	if queue == nil || terminalWorkQueue == nil || cascades == nil || authorities == nil || notifier == nil {
		slog.WarnContext(ctx, "Execution run.finished queue is unavailable",
			"runID", run.RunID, "status", string(run.Status))
		return
	}
	journal, ok := s.TriggerEvents().(RunOutcomeJournal)
	if !ok {
		slog.WarnContext(ctx, "Execution run.finished journal port is unavailable",
			"runID", run.RunID, "status", string(run.Status))
		return
	}
	reconciler, err := NewRunOutcomeReconcilerWithExecution(
		queue, terminalWorkQueue, notifier, journal, publisher, run.WorkspaceKey, nil,
		cascades, authorities, string(execution.DriverRunOutcomeComponentID),
	)
	if err == nil {
		_, err = reconciler.DrainOnce(ctx, time.Now().UTC())
	}
	if err != nil {
		slog.WarnContext(ctx, "reconcile durable run.finished outcome failed",
			"runID", run.RunID, "status", string(run.Status),
			"eventID", RunFinishedEventID(run.RunID, run.Status), "error", err)
	}
}

// newRunOutcome maps trusted DriverRun state onto the narrow outbound port.
// This stays in the already-baselined legacy Execution file while the Phase 4
// extraction still supplies a composite Store; the new port definition itself
// has no persistence dependency.
func newRunOutcome(ctx context.Context, st store.Store, run *domain.DriverRun) RunOutcome {
	occurredAt := time.Now().UTC()
	if run.FinishedAt != nil && !run.FinishedAt.IsZero() {
		occurredAt = run.FinishedAt.UTC()
	}
	return RunOutcome{
		WorkspaceKey:  run.WorkspaceKey,
		EventID:       RunFinishedEventID(run.RunID, run.Status),
		EventType:     RunFinishedEventType,
		RunID:         run.RunID,
		Status:        run.Status,
		ActorRef:      RunFinishedActor,
		ParentEventID: runFinishedParentEvent(ctx, st, run),
		EpicID:        run.EpicID,
		OccurredAt:    occurredAt,
		Payload:       marshalRunFinishedPayload(ctx, run),
	}
}

// dispatchRunFinishedAwaits is Execution's independent await notification
// lane. It resolves already-registered composition awaits without depending
// on Automation bindings. AwaitChildWorkflow also re-checks terminal child
// state after registration, closing the child-finished-before-await window on
// backends that do not expose a client-side event journal appender.
func dispatchRunFinishedAwaits(ctx context.Context, notifier RunOutcomeAwaitNotifier, outcome RunOutcome) {
	if err := notifier.NotifyRunOutcomeAwaits(ctx, outcome); err != nil {
		slog.WarnContext(ctx, "run.finished await dispatch failed",
			"runID", outcome.RunID, "status", string(outcome.Status), "eventID", outcome.EventID, "error", err)
	}
}

// marshalRunFinishedPayload encodes the resume/fan-out payload; nil (with a
// log record) on the never-expected marshal failure so the lifecycle event
// still propagates without it.
func marshalRunFinishedPayload(ctx context.Context, run *domain.DriverRun) json.RawMessage {
	payload, err := marshalBoundedRunFinishedPayload(
		run.RunID, run.Status, run.Summary, run.ErrorClass, run.ParentRunID,
	)
	if err != nil {
		slog.WarnContext(ctx, "encode run.finished payload failed", "runID", run.RunID, "error", err)
		return nil
	}
	return payload
}

// runFinishedParentEvent returns the durable admitting event when SourceRef
// names one. Automation derives hop depth from that persisted parent. Free-
// form source refs remain parentless system roots.
func runFinishedParentEvent(ctx context.Context, s store.Store, run *domain.DriverRun) string {
	parentEventID := strings.TrimSpace(run.SourceRef)
	if parentEventID == "" {
		return ""
	}
	if _, err := s.TriggerEvents().Get(ctx, run.WorkspaceKey, parentEventID); err != nil {
		return ""
	}
	return parentEventID
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
