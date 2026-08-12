package driver

// Test-only legacy workflow composition driver-ops (ARCHITECTURE-PROPOSAL §7 step 8, chunk
// AW10).
//
// workflows/start creates a CHILD DriverRun for a verified parent run:
//   - the child run ID is deterministic over (parentRunID, idempotencyKey or
//     "start-{startIndex}") so a re-entered parent re-issuing the same start
//     gets the SAME child back — never a duplicate (§9.4 re-entrancy; §4
//     "deterministic child ids prevent double effects");
//   - the child records ParentRunID (orthogonal to EpicID — neither is
//     derived from the other) and SourceKind "workflow";
//   - nesting is bounded by the composition depth cap, the parent-chain twin
//     of the C19 hop-depth guard: starting a child deeper than the cap is
//     refused with domain.ErrCompositionDepthExceeded.
//
// workflows/await is await-machinery sugar over the run.finished lifecycle
// lane (AW6): it validates the child actually belongs to the caller, then
// delegates to AwaitEvent with pattern "run.finished:{childRunId}" — RULE 2's
// registration-time journal scan finds the journaled run.finished of an
// already-terminal child, so it resolves inline with no lost wakeup, and the
// await consumes a normal awaitIndex slot so parent re-entry replays the
// child result deterministically (RULE 3). The await requires actor=system:
// run.finished is a server-owned lifecycle event and external/session events
// must never satisfy this internal composition lane.
//
// cascadeCancelChildren is the composition cascade (locked decision): when a
// parent reaches a terminal status its QUEUED children are cancelled (each
// emitting its own run.finished, recursively cascading) and its RUNNING
// children get a cooperative cancel request the owning executor observes on
// heartbeat. Detached runs (no ParentRunID) are never touched. Suspended
// children are left to their own await deadlines (RULE 5 bounds them).

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

// cascadeChildListLimit bounds the workspace run listing the cascade filters
// for children.
const cascadeChildListLimit = 1000

// childWorkflowIdempotencyKey is the store-level idempotency key guarding the
// child creation (SetNX semantics: the first create wins, replays read it).
func childWorkflowIdempotencyKey(parentRunID, key string) string {
	return "workflow-start:" + parentRunID + ":" + key
}

// StartChildWorkflowOptions carries one workflows/start invocation.
// ParentRunID is the VERIFIED identity of the calling run, never body data.
type StartChildWorkflowOptions struct {
	WorkspaceKey string
	ParentRunID  string
	// WorkflowName names the registered driver to run (workflow names are
	// driver IDs on this surface; the active passed version is pinned).
	WorkflowName string
	// Input is the child's initial payload. Empty means {}.
	Input json.RawMessage
	// IdempotencyKey, when set, keys the deterministic child identity.
	IdempotencyKey string
	// StartIndex is the 1-based ordinal of this start within the run, used
	// as "start-{n}" when no IdempotencyKey is given (the SDK's per-process
	// monotonic counter, mirroring awaitIndex determinism).
	StartIndex int
	// MaxDepth overrides the composition depth cap (tests). Zero means
	// CompositionMaxDepthEnvVar, falling back to DefaultCompositionMaxDepth.
	MaxDepth int
}

// startKey resolves the deterministic child key: explicit idempotency key,
// else the start ordinal.
func (o StartChildWorkflowOptions) startKey() (string, error) {
	return ResolveChildWorkflowStartKey(o.IdempotencyKey, o.StartIndex)
}

// maxDepth resolves the effective composition depth cap: explicit option,
// env override, default.
func (o StartChildWorkflowOptions) maxDepth() int {
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return compositionMaxDepthFromEnv()
}

// StartChildWorkflow creates (or idempotently re-reads) the deterministic
// child run of the verified parent.
func StartChildWorkflow(ctx context.Context, st store.Store, opts StartChildWorkflowOptions) (*domain.DriverRun, error) {
	workflowName := strings.TrimSpace(opts.WorkflowName)
	if st == nil || strings.TrimSpace(opts.WorkspaceKey) == "" || strings.TrimSpace(opts.ParentRunID) == "" {
		return nil, fmt.Errorf("store, workspace key and parent run id required: %w", domain.ErrInvalid)
	}
	if workflowName == "" {
		return nil, fmt.Errorf("workflowName required: %w", domain.ErrInvalid)
	}
	key, err := opts.startKey()
	if err != nil {
		return nil, err
	}
	if err := ensureCompositionDepth(ctx, st, opts.WorkspaceKey, opts.ParentRunID, opts.maxDepth()); err != nil {
		return nil, err
	}
	driver, version, err := activeDriverVersion(ctx, st, opts.WorkspaceKey, workflowName)
	if err != nil {
		return nil, err
	}
	payload := clonePayload(opts.Input)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("child input must be valid JSON: %w", domain.ErrInvalid)
	}
	return createChildRun(ctx, st, opts, driver.DriverID, version.VersionID, key, payload)
}

// createChildRun runs the SetNX-shaped create: the deterministic run ID plus
// the store idempotency key make the first create win and every replay read
// the existing child back.
func createChildRun(ctx context.Context, st store.Store, opts StartChildWorkflowOptions, driverID, versionID, key string, payload json.RawMessage) (*domain.DriverRun, error) {
	childRunID := ChildWorkflowRunID(opts.ParentRunID, key)
	created, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    opts.WorkspaceKey,
		RunID:           childRunID,
		DriverID:        driverID,
		DriverVersionID: versionID,
		Entrypoint:      EntrypointRun,
		SourceKind:      ChildRunSourceKind,
		SourceRef:       opts.ParentRunID,
		ParentRunID:     opts.ParentRunID,
		IdempotencyKey:  childWorkflowIdempotencyKey(opts.ParentRunID, key),
		Payload:         payload,
	})
	if errors.Is(err, domain.ErrAlreadyExists) {
		// Backends without idempotency-key read-back surface the duplicate
		// as a conflict on the deterministic run ID: re-read it.
		return existingChildRun(ctx, st, opts.WorkspaceKey, opts.ParentRunID, childRunID)
	}
	if err != nil {
		return nil, fmt.Errorf("create child run %s: %w", childRunID, err)
	}
	if created.RunID != childRunID || created.ParentRunID != opts.ParentRunID {
		// Defensive: an idempotency-key collision handed back someone
		// else's run. The key embeds parent + start key, so this means
		// corrupted state, not a replay.
		return nil, fmt.Errorf("child run create for %s returned run %s of parent %q: %w",
			childRunID, created.RunID, created.ParentRunID, domain.ErrConflict)
	}
	return created, nil
}

// existingChildRun resolves the idempotent-replay leg: the deterministic run
// already exists and must belong to the calling parent.
func existingChildRun(ctx context.Context, st store.Store, ws, parentRunID, childRunID string) (*domain.DriverRun, error) {
	existing, err := st.DriverRuns().Get(ctx, ws, childRunID)
	if err != nil {
		return nil, fmt.Errorf("read existing child run %s: %w", childRunID, err)
	}
	if existing.ParentRunID != parentRunID {
		return nil, fmt.Errorf("run %s exists with parent %q, not %q: %w",
			childRunID, existing.ParentRunID, parentRunID, domain.ErrConflict)
	}
	return existing, nil
}

// ensureCompositionDepth refuses a start whose child would sit deeper than
// maxDepth on the ParentRunID chain (root runs are depth 0). The walk is
// bounded by maxDepth itself, so a corrupt parent cycle also lands in the
// refusal rather than an unbounded loop.
func ensureCompositionDepth(ctx context.Context, st store.Store, ws, parentRunID string, maxDepth int) error {
	childDepth := 1 // the new child sits one level below its parent
	cur := parentRunID
	for childDepth <= maxDepth {
		run, err := st.DriverRuns().Get(ctx, ws, cur)
		if err != nil || run.ParentRunID == "" {
			// Chain root (or an unreadable ancestor, treated as the root).
			return nil
		}
		cur = run.ParentRunID
		childDepth++
	}
	return fmt.Errorf("child of run %s would exceed composition depth %d: %w",
		parentRunID, maxDepth, domain.ErrCompositionDepthExceeded)
}

// AwaitChildWorkflowOptions carries one workflows/await invocation. RunID,
// NodeID, LeaseID and FencingToken are the VERIFIED owner identity of the
// calling parent run.
type AwaitChildWorkflowOptions struct {
	WorkspaceKey string
	RunID        string
	NodeID       string
	LeaseID      string
	FencingToken int64

	// ChildRunID names the awaited child; it must carry ParentRunID ==
	// RunID — a run can never await arbitrary runs.
	ChildRunID string
	// TimeoutMs is the mandatory await timeout (RULE 5 applies to
	// composition too).
	TimeoutMs int64
	// AwaitIndex is the 1-based await ordinal: workflows/await consumes a
	// normal awaitIndex slot (RULE 3) so re-entry replays deterministically.
	AwaitIndex int
	// MaxTimeout overrides the timeout bound (tests); see AwaitEventOptions.
	MaxTimeout time.Duration
}

// AwaitChildWorkflow validates the parent/child link, then runs the standard
// await flow over the child's run.finished subject key. It returns the await
// outcome plus the child run (as read this request — terminal info for
// satisfied awaits, current status otherwise).
func AwaitChildWorkflow(ctx context.Context, st store.Store, opts AwaitChildWorkflowOptions) (*AwaitEventOutcome, *domain.DriverRun, error) { //nolint:funlen // Parent-child validation and await registration form one ordered composition operation.
	childRunID := strings.TrimSpace(opts.ChildRunID)
	if childRunID == "" {
		return nil, nil, fmt.Errorf("childRunId required: %w", domain.ErrInvalid)
	}
	child, err := st.DriverRuns().Get(ctx, opts.WorkspaceKey, childRunID)
	if err != nil {
		return nil, nil, fmt.Errorf("get child run %s: %w", childRunID, err)
	}
	if child.ParentRunID == "" || child.ParentRunID != opts.RunID {
		return nil, nil, fmt.Errorf("run %s is not a child of run %s: %w", childRunID, opts.RunID, domain.ErrNotOwner)
	}
	awaitOptions := AwaitEventOptions{
		WorkspaceKey: opts.WorkspaceKey,
		RunID:        opts.RunID,
		NodeID:       opts.NodeID,
		LeaseID:      opts.LeaseID,
		FencingToken: opts.FencingToken,
		Pattern:      RunFinishedSubjectKey(childRunID),
		ActorAllow:   []string{RunFinishedActor},
		TimeoutMs:    opts.TimeoutMs,
		AwaitIndex:   opts.AwaitIndex,
		MaxTimeout:   opts.MaxTimeout,
	}
	// Serialize this child's outcome notification with registration and the
	// post-registration terminal re-read. This covers all orderings:
	// finished-before-register resolves inline, registered-before-finish is
	// resolved by the normal notification, and a concurrent finish cannot
	// fall between the two without one side observing the other's durable row.
	unlock := lockRunOutcome(opts.WorkspaceKey, childRunID)
	registered, instanceKey, err := registerAwait(ctx, st, awaitOptions)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	if registered.Satisfied {
		if registered.Instance == nil {
			unlock()
			return nil, nil, fmt.Errorf("satisfied child await %s returned no instance: %w", instanceKey, domain.ErrInvalidTransition)
		}
		if registered.Instance.Status == domain.AwaitSatisfied {
			// Registration-time catch-up may inspect rows written by an older
			// deployment. Re-read the child after the atomic scan, then require
			// the recorded winner to be the exact trusted terminal outcome.
			child, err = st.DriverRuns().Get(ctx, opts.WorkspaceKey, childRunID)
			if err != nil {
				unlock()
				return nil, nil, fmt.Errorf("recheck child run %s after satisfied await registration: %w", childRunID, err)
			}
			if err := ValidateSatisfiedChildAwait(ctx, registered.Instance, child); err != nil {
				unlock()
				return nil, child, err
			}
		}
		unlock()
		return &AwaitEventOutcome{Status: string(registered.Instance.Status), Instance: registered.Instance}, child, nil
	}
	child, err = st.DriverRuns().Get(ctx, opts.WorkspaceKey, childRunID)
	if err != nil {
		unlock()
		return nil, nil, fmt.Errorf("recheck child run %s after await registration: %w", childRunID, err)
	}
	if child.Status.IsTerminal() {
		outcome, resolveErr := resolveTerminalChildAwait(ctx, st, awaitOptions, instanceKey, child)
		unlock()
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		return outcome, child, nil
	}
	unlock()
	outcome, err := suspendForAwait(ctx, st, awaitOptions, instanceKey, registered.Instance)
	if err != nil {
		return nil, nil, err
	}
	return outcome, child, nil
}

// resolveTerminalChildAwait records a deterministic run.finished outcome on
// the already-registered await while the parent is still running. There is no
// resume call: the workflow continues inline in the same fenced execution.
// Re-entry reads the satisfied row by the same awaitIndex.
func resolveTerminalChildAwait(ctx context.Context, st store.Store, opts AwaitEventOptions, instanceKey string, child *domain.DriverRun) (*AwaitEventOutcome, error) {
	eventID := RunFinishedEventID(child.RunID, child.Status)
	resolution, err := st.Awaits().ResolveAwait(ctx, opts.WorkspaceKey, instanceKey, eventID,
		marshalRunFinishedPayload(ctx, child), RunFinishedActor)
	if err != nil {
		return nil, fmt.Errorf("resolve terminal child await %s by %s: %w", instanceKey, eventID, err)
	}
	instance := resolution.Instance
	if instance == nil || instance.Status != domain.AwaitSatisfied || instance.SatisfiedByEventID != eventID {
		return nil, fmt.Errorf("terminal child await %s returned invalid resolution: %w", instanceKey, domain.ErrInvalidTransition)
	}
	return &AwaitEventOutcome{Status: string(instance.Status), Instance: instance}, nil
}

// cascadeCancelChildren applies the composition cascade for one terminal
// parent: queued children are cancelled (emitting their own run.finished and
// cascading recursively), running children get a cooperative cancel request.
// Best-effort like every lifecycle leg — failures are logged, never returned;
// backends without store.DriverRunCancelSupport skip the cascade entirely.
func cascadeCancelChildren(
	ctx context.Context,
	st store.Store,
	outcomes RunOutcomePublisher,
	parent *domain.DriverRun,
	depth int,
	awaitNotifiers ...RunOutcomeAwaitNotifier,
) {
	if st == nil || parent == nil || !parent.Status.IsTerminal() || depth > compositionMaxDepthFromEnv() {
		return
	}
	canceller, ok := st.DriverRuns().(store.DriverRunCancelSupport)
	if !ok {
		slog.DebugContext(ctx, "composition cancel cascade skipped: backend lacks cancel support",
			"runID", parent.RunID)
		return
	}
	children, err := listChildRuns(ctx, st, parent.WorkspaceKey, parent.RunID)
	if err != nil {
		slog.WarnContext(ctx, "composition cancel cascade: list children failed",
			"runID", parent.RunID, "error", err)
		return
	}
	reason := fmt.Sprintf("parent run %s reached terminal status %s", parent.RunID, parent.Status)
	for _, child := range children {
		cascadeCancelChild(ctx, st, outcomes, canceller, child, reason, depth, awaitNotifiers...)
	}
}

// cascadeCancelChild applies one child's cascade leg per the locked decision:
// queued -> cancelled, running -> cancel-requested, anything else untouched.
func cascadeCancelChild(
	ctx context.Context,
	st store.Store,
	outcomes RunOutcomePublisher,
	canceller store.DriverRunCancelSupport,
	child *domain.DriverRun,
	reason string,
	depth int,
	awaitNotifiers ...RunOutcomeAwaitNotifier,
) {
	switch child.Status {
	case domain.DriverRunQueued:
		cancelled, err := canceller.CancelQueuedRun(ctx, child.WorkspaceKey, child.RunID, reason, CancelErrorClassParentTerminal)
		if err != nil {
			// Likely claimed inside the race window; the next heartbeat
			// cascade pass is out of scope — log and move on.
			slog.WarnContext(ctx, "composition cancel cascade: cancel queued child failed",
				"childRunID", child.RunID, "error", err)
			return
		}
		// A cancelled child is a terminal transition like any other: its
		// own waiters resolve and its own children cascade.
		emitRunFinishedEvent(ctx, st, outcomes, cancelled, awaitNotifiers...)
		cascadeCancelChildren(ctx, st, outcomes, cancelled, depth+1, awaitNotifiers...)
	case domain.DriverRunRunning:
		if _, err := canceller.RequestCancel(ctx, child.WorkspaceKey, child.RunID, reason); err != nil {
			slog.WarnContext(ctx, "composition cancel cascade: request cancel failed",
				"childRunID", child.RunID, "error", err)
		}
	default:
		// Terminal children need nothing; suspended children hold no slot
		// and are bounded by their await deadlines (RULE 5).
	}
}

// listChildRuns returns the workspace runs whose ParentRunID is runID. The
// run filter has no parent predicate, so this filters a bounded listing.
func listChildRuns(ctx context.Context, st store.Store, ws, runID string) ([]*domain.DriverRun, error) {
	runs, err := st.DriverRuns().List(ctx, ws, store.DriverRunFilter{Limit: cascadeChildListLimit})
	if err != nil {
		return nil, err
	}
	children := runs[:0]
	for _, run := range runs {
		if run.ParentRunID == runID {
			children = append(children, run)
		}
	}
	return children, nil
}
