package driver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/eventpolicy"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	runOutcomeJournalEventIDPrefix  = "execution-await-event:"
	runFinishedSummaryPayloadLimit  = 16 * 1024
	runFinishedErrorPayloadLimit    = 4 * 1024
	runFinishedIdentityPayloadLimit = 8 * 1024
)

// RunOutcome is Execution's immutable terminal-outcome event. EventID is the
// deterministic idempotency anchor; actor and parent provenance are derived
// from trusted DriverRun state rather than request content.
type RunOutcome struct {
	WorkspaceKey  string
	EventID       string
	EventType     string
	RunID         string
	Status        domain.DriverRunStatus
	ActorRef      string
	ParentEventID string
	EpicID        string
	OccurredAt    time.Time
	Payload       json.RawMessage
}

// RunOutcomeJournalEventID is the deterministic durable identity of
// Execution's base event-journal record. It is intentionally distinct from
// Automation's admission identity while SourceEventID remains the shared
// canonical run.finished identity used by await resolution.
func RunOutcomeJournalEventID(workspace, sourceEventID string) string {
	sum := sha256.Sum256([]byte(workspace + "\x00" + sourceEventID))
	return runOutcomeJournalEventIDPrefix + hex.EncodeToString(sum[:16])
}

func runOutcomeJournalIdempotencyKey(workspace, sourceEventID string) string {
	sum := sha256.Sum256([]byte(workspace + "\x00" + sourceEventID))
	return "execution-await:" + hex.EncodeToString(sum[:])
}

func runOutcomeJournalEvent(outcome RunOutcome) *domain.TriggerEvent {
	return &domain.TriggerEvent{
		WorkspaceKey: outcome.WorkspaceKey,
		EventID:      RunOutcomeJournalEventID(outcome.WorkspaceKey, outcome.EventID),
		SourceKind:   eventpolicy.SourceKindExecution, SourceEventID: outcome.EventID,
		EventType: outcome.EventType, SubjectRef: outcome.RunID, ActorRef: outcome.ActorRef,
		EpicID:     outcome.EpicID,
		Origin:     domain.TriggerEventOriginSystem,
		OccurredAt: outcome.OccurredAt.UTC(), ReceivedAt: outcome.OccurredAt.UTC(),
		IdempotencyKey:  runOutcomeJournalIdempotencyKey(outcome.WorkspaceKey, outcome.EventID),
		SignatureStatus: "internal",
		Payload:         append(json.RawMessage(nil), outcome.Payload...),
	}
}

// marshalBoundedRunFinishedPayload keeps base Execution outcomes eligible for
// await delivery even when terminal summaries contain arbitrarily large or
// escape-heavy output. It preserves useful bounded detail, then
// deterministically sheds optional fields before finally hashing an absurdly
// large run identity. Status and the truncation marker always survive.
func marshalBoundedRunFinishedPayload(
	runID string,
	status domain.DriverRunStatus,
	summary, errorClass, parentRunID string,
) (json.RawMessage, error) {
	boundedRunID, runIDTruncated := boundedRunFinishedText(runID, runFinishedIdentityPayloadLimit)
	boundedParent, parentTruncated := boundedRunFinishedText(parentRunID, runFinishedIdentityPayloadLimit)
	boundedSummary, summaryTruncated := boundedRunFinishedText(summary, runFinishedSummaryPayloadLimit)
	boundedError, errorTruncated := boundedRunFinishedText(errorClass, runFinishedErrorPayloadLimit)
	payload := runFinishedPayload{
		RunID: boundedRunID, Status: string(status), Summary: boundedSummary,
		ErrorClass: boundedError, ParentRunID: boundedParent,
		Truncated: runIDTruncated || parentTruncated || summaryTruncated || errorTruncated,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(encoded) <= domain.DefaultAwaitResumePayloadCap {
		return encoded, nil
	}
	payload.Summary, payload.ErrorClass, payload.Truncated = "", "", true
	encoded, err = json.Marshal(payload)
	if err != nil || len(encoded) <= domain.DefaultAwaitResumePayloadCap {
		return encoded, err
	}
	payload.ParentRunID = ""
	encoded, err = json.Marshal(payload)
	if err != nil || len(encoded) <= domain.DefaultAwaitResumePayloadCap {
		return encoded, err
	}
	sum := sha256.Sum256([]byte(runID))
	payload.RunID = "h:" + hex.EncodeToString(sum[:])
	return json.Marshal(payload)
}

func boundedRunFinishedText(value string, limit int) (string, bool) {
	valid := strings.ToValidUTF8(value, "\uFFFD")
	changed := valid != value
	if limit < 1 || len(valid) <= limit {
		return valid, changed
	}
	end := limit
	for end > 0 && !utf8.ValidString(valid[:end]) {
		end--
	}
	return valid[:end], true
}

// RunOutcomePublisher is Execution's only cross-capability outcome port.
// Implementations must be idempotent by WorkspaceKey+EventID. A publication
// error is observable but cannot invalidate the committed DriverRun terminal
// transition.
type RunOutcomePublisher interface {
	PublishRunOutcome(context.Context, RunOutcome) error
}

// runOutcomeLocks close the only in-process race that cannot be delegated to
// the AwaitStore: a child terminal transition can overlap its parent's await
// registration. The bounded stripes serialize only the child's local outcome
// notification with that registration/re-check; durable await state remains
// the source of truth and cross-process correctness uses the same atomic
// register/resolve APIs.
var runOutcomeLocks [64]sync.Mutex

func lockRunOutcome(workspace, runID string) func() {
	sum := sha256.Sum256([]byte(workspace + "\x00" + runID))
	index := binary.LittleEndian.Uint64(sum[:8]) % uint64(len(runOutcomeLocks))
	runOutcomeLocks[index].Lock()
	return runOutcomeLocks[index].Unlock
}

// RunOutcomeAwaitNotifier is the Execution-owned durable composition leg. It
// is independent of Automation bindings and idempotent by outcome EventID.
type RunOutcomeAwaitNotifier interface {
	NotifyRunOutcomeAwaits(context.Context, RunOutcome) error
}

// RunOutcomeAwaitResolver is the command edge used by the driver coordinator.
// Production injects the typed Execution adapter; the legacy Store capability
// is confined to characterization tests.
type RunOutcomeAwaitResolver interface {
	ResolveRunOutcomeAwaitAndResume(
		context.Context,
		string, string, string,
		json.RawMessage,
	) error
}

type storeRunOutcomeAwaitNotifier struct {
	awaits   store.AwaitStore
	resolver RunOutcomeAwaitResolver
}

func NewRunOutcomeAwaitNotifierWithResolver(
	awaits store.AwaitStore,
	resolver RunOutcomeAwaitResolver,
) (RunOutcomeAwaitNotifier, error) {
	if awaits == nil {
		return nil, fmt.Errorf("run outcome await store is required")
	}
	if resolver == nil {
		return nil, fmt.Errorf("await store lacks atomic run outcome resolve-and-resume capability")
	}
	return &storeRunOutcomeAwaitNotifier{awaits: awaits, resolver: resolver}, nil
}

func (notifier *storeRunOutcomeAwaitNotifier) NotifyRunOutcomeAwaits(ctx context.Context, outcome RunOutcome) error {
	if notifier == nil || notifier.awaits == nil || notifier.resolver == nil {
		return fmt.Errorf("run outcome await notifier is unavailable")
	}
	candidates, err := notifier.awaits.ListAwaitsByPattern(
		ctx,
		outcome.WorkspaceKey,
		RunFinishedSubjectKey(outcome.RunID),
	)
	if err != nil {
		return fmt.Errorf("list run outcome awaits for %q: %w", outcome.EventID, err)
	}
	for _, candidate := range candidates {
		if candidate == nil {
			return fmt.Errorf("run outcome await candidate for %q is nil", outcome.EventID)
		}
		if err := notifier.resolver.ResolveRunOutcomeAwaitAndResume(
			ctx,
			outcome.WorkspaceKey,
			candidate.InstanceKey,
			outcome.EventID,
			outcome.Payload,
		); err != nil {
			return fmt.Errorf("resolve run outcome await %q by %q: %w", candidate.InstanceKey, outcome.EventID, err)
		}
	}
	return nil
}

const (
	DefaultRunOutcomeReconcileLimit = execution.DefaultReconciliationQueueLimit
)

type RunOutcomeWorkspaceLister interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
}

// RunOutcomeReconciler drains Execution's durable terminal-outcome outbox.
// Atomic composition notification always runs first; optional Automation
// publication is at-least-once, and RunFinishedEventID makes its admission
// idempotent across crashes after publish but before completion.
type RunOutcomeReconciler struct {
	queue             execution.DriverRunOutcomeAPI
	terminalWorkQueue execution.TerminalDriverRunWorkRecoveryQueueAPI
	awaits            RunOutcomeAwaitNotifier
	journal           store.TriggerEventAppender
	publisher         RunOutcomePublisher
	workspace         string
	workspaces        RunOutcomeWorkspaceLister
	claimPrefix       string
	claimCounter      atomic.Uint64
	limit             int
	cascades          execution.DriverRunAPI
	authorities       execution.SystemAuthorityResolver
	componentID       string
}

// NewRunOutcomeReconcilerWithExecution composes the system-authorized queue
// consumer and the recovery lane that closes a finalize-before-child-cascade
// crash window. The queue row is not completed until every deterministic leg
// has converged.
func NewRunOutcomeReconcilerWithExecution(
	queue execution.DriverRunOutcomeAPI,
	terminalWorkQueue execution.TerminalDriverRunWorkRecoveryQueueAPI,
	awaits RunOutcomeAwaitNotifier,
	journal store.TriggerEventAppender,
	publisher RunOutcomePublisher,
	workspace string,
	workspaces RunOutcomeWorkspaceLister,
	cascades execution.DriverRunAPI,
	authorities execution.SystemAuthorityResolver,
	componentID string,
) (*RunOutcomeReconciler, error) {
	if queue == nil || terminalWorkQueue == nil || awaits == nil || journal == nil || cascades == nil || authorities == nil || strings.TrimSpace(componentID) == "" {
		return nil, fmt.Errorf("run outcome queue, terminal-work recovery queue, await notifier, journal, cascade API, authority resolver, and component ID are required")
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" && workspaces == nil {
		return nil, fmt.Errorf("run outcome workspace lister is required for an unscoped reconciler")
	}
	return &RunOutcomeReconciler{
		queue: queue, terminalWorkQueue: terminalWorkQueue, awaits: awaits, journal: journal, publisher: publisher, workspace: workspace, workspaces: workspaces,
		claimPrefix: newRunOutcomeClaimPrefix(), limit: DefaultRunOutcomeReconcileLimit,
		cascades: cascades, authorities: authorities, componentID: componentID,
	}, nil
}

func (reconciler *RunOutcomeReconciler) RunOnce(ctx context.Context, now time.Time) error {
	_, err := reconciler.DrainOnce(ctx, now)
	return err
}

// DrainOnce performs one bounded pass and returns how many durable rows were
// claimed for runtime observability and opportunistic-drain accounting.
func (reconciler *RunOutcomeReconciler) DrainOnce(ctx context.Context, now time.Time) (int, error) {
	if reconciler == nil || reconciler.queue == nil || reconciler.terminalWorkQueue == nil || reconciler.authorities == nil || reconciler.awaits == nil || reconciler.journal == nil {
		return 0, fmt.Errorf("run outcome reconciler is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	workspaces, err := reconciler.workspaceKeys(ctx)
	if err != nil {
		return 0, err
	}
	var errs []error
	claimed := 0
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		count, err := reconciler.runWorkspace(ctx, workspace, now)
		claimed += count
		if err != nil {
			errs = append(errs, err)
		}
	}
	return claimed, errors.Join(errs...)
}

func (reconciler *RunOutcomeReconciler) workspaceKeys(ctx context.Context) ([]string, error) {
	if reconciler.workspace != "" {
		return []string{reconciler.workspace}, nil
	}
	values, err := reconciler.workspaces.ListWorkspaceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list run outcome workspaces: %w", err)
	}
	return values, nil
}

func (reconciler *RunOutcomeReconciler) runWorkspace(ctx context.Context, workspace string, now time.Time) (int, error) {
	var errs []error
	terminalWorkClaimed, terminalWorkErr := reconciler.runTerminalWorkRecoveryQueue(ctx, workspace, now)
	if terminalWorkErr != nil {
		errs = append(errs, terminalWorkErr)
	}
	outcomeClaimed, outcomeErr := reconciler.runOutcomeQueue(ctx, workspace, now)
	if outcomeErr != nil {
		errs = append(errs, outcomeErr)
	}
	return terminalWorkClaimed + outcomeClaimed, errors.Join(errs...)
}

func (reconciler *RunOutcomeReconciler) runOutcomeQueue(ctx context.Context, workspace string, now time.Time) (int, error) {
	var errs []error
	claimID := fmt.Sprintf("%s-%d", reconciler.claimPrefix, reconciler.claimCounter.Add(1))
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionClaimDriverRunOutcomes, reconciler.componentID,
	)
	if err != nil {
		errs = append(errs, fmt.Errorf("resolve run outcome claim authority in %q: %w", workspace, err))
		return 0, errors.Join(errs...)
	}
	claimed, err := reconciler.queue.ClaimDriverRunOutcomes(ctx, auth, execution.ClaimDriverRunOutcomesCommand{
		WorkspaceKey: workspace, ClaimID: claimID, Before: now, Limit: reconciler.limit,
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("claim run outcomes in %q: %w", workspace, err))
		return 0, errors.Join(errs...)
	}
	for _, persisted := range claimed {
		outcome, reportErr, retryCause := reconciler.deliverRunOutcome(ctx, persisted)
		if reportErr != nil {
			errs = append(errs, reportErr)
			if retryErr := reconciler.retry(ctx, workspace, claimID, persisted, now, retryCause); retryErr != nil {
				errs = append(errs, retryErr)
			}
			continue
		}
		if completeErr := reconciler.complete(ctx, workspace, claimID, persisted.RunID, now); completeErr != nil {
			errs = append(errs, fmt.Errorf("complete run outcome %q: %w", outcome.EventID, completeErr))
		}
	}
	return len(claimed), errors.Join(errs...)
}

func (reconciler *RunOutcomeReconciler) runTerminalWorkRecoveryQueue(
	ctx context.Context,
	workspace string,
	now time.Time,
) (int, error) {
	var errs []error
	claimID := fmt.Sprintf("%s-terminal-work-%d", reconciler.claimPrefix, reconciler.claimCounter.Add(1))
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionClaimTerminalDriverRunWorkRecoveries, reconciler.componentID,
	)
	if err != nil {
		return 0, fmt.Errorf("resolve terminal DriverRun work recovery claim authority in %q: %w", workspace, err)
	}
	claimed, err := reconciler.terminalWorkQueue.ClaimTerminalDriverRunWorkRecoveries(
		ctx,
		auth,
		execution.ClaimTerminalDriverRunWorkRecoveriesCommand{
			WorkspaceKey: workspace, ClaimID: claimID, Before: now, Limit: reconciler.limit,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("claim terminal DriverRun work recoveries in %q: %w", workspace, err)
	}
	for _, persisted := range claimed {
		recoveryErr := validateTerminalDriverRunWorkRecoverySnapshot(workspace, persisted)
		if recoveryErr == nil {
			recoveryErr = reconciler.recoverTerminalDriverRunWork(ctx, persisted)
		}
		if recoveryErr != nil {
			errs = append(errs, fmt.Errorf("recover terminal DriverRun work for %q: %w", persisted.RunID, recoveryErr))
			if retryErr := reconciler.retryTerminalDriverRunWorkRecovery(
				ctx, workspace, claimID, persisted, now, recoveryErr,
			); retryErr != nil {
				errs = append(errs, retryErr)
			}
			continue
		}
		if completeErr := reconciler.completeTerminalDriverRunWorkRecovery(
			ctx, workspace, claimID, persisted.RunID, now,
		); completeErr != nil {
			errs = append(errs, fmt.Errorf("complete terminal DriverRun work recovery %q: %w", persisted.RunID, completeErr))
		}
	}
	return len(claimed), errors.Join(errs...)
}

func validateTerminalDriverRunWorkRecoverySnapshot(workspace string, persisted execution.DriverRunOutcome) error {
	if persisted.WorkspaceKey != workspace || strings.TrimSpace(persisted.RunID) == "" ||
		!persisted.Status.IsTerminal() || persisted.OccurredAt.IsZero() {
		return fmt.Errorf("invalid terminal DriverRun work recovery snapshot")
	}
	return nil
}

func (reconciler *RunOutcomeReconciler) deliverRunOutcome(
	ctx context.Context,
	persisted execution.DriverRunOutcome,
) (*RunOutcome, error, error) {
	outcome, err := persistedRunOutcome(persisted)
	if err != nil {
		return nil, err, err
	}
	if reconciler.cascades != nil {
		if err := reconciler.recoverTerminalDriverRunWork(ctx, persisted); err != nil {
			return nil, fmt.Errorf("recover terminal DriverRun work for %q: %w", outcome.EventID, err), err
		}
		if err := reconciler.recoverChildDriverRunCascade(ctx, persisted); err != nil {
			return nil, fmt.Errorf("recover child DriverRun cascade for %q: %w", outcome.EventID, err), err
		}
	}
	if _, err := reconciler.journal.AppendTriggerEvent(ctx, runOutcomeJournalEvent(outcome)); err != nil {
		return nil, fmt.Errorf("journal base run outcome %q: %w", outcome.EventID, err), err
	}
	if err := reconciler.awaits.NotifyRunOutcomeAwaits(ctx, outcome); err != nil {
		return nil, fmt.Errorf("notify run outcome awaits %q: %w", outcome.EventID, err), err
	}
	if reconciler.publisher != nil {
		if err := reconciler.publisher.PublishRunOutcome(ctx, outcome); err != nil {
			return nil, fmt.Errorf("publish run outcome %q: %w", outcome.EventID, err), err
		}
	}
	return &outcome, nil, nil
}

const childDriverRunCascadeErrorClass = "parent_run_terminal"

func childDriverRunCascadeReason(status domain.DriverRunStatus) string {
	return "parent driver run became " + string(status)
}

// recoverTerminalDriverRunWork closes the terminal-parent crash window for
// TaskRuns and Work Item claims before any downstream outcome is published.
// The Fleet command is atomic and exact-generation fenced: a stale parent may
// terminalize its own TaskRun, but it cannot clear a successor Work Item claim.
func (reconciler *RunOutcomeReconciler) recoverTerminalDriverRunWork(
	ctx context.Context,
	persisted execution.DriverRunOutcome,
) error {
	if reconciler == nil || reconciler.cascades == nil || reconciler.authorities == nil {
		return execution.ErrUnavailable
	}
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx,
		persisted.WorkspaceKey,
		execution.ActionRecoverTerminalDriverRunWork,
		string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		return fmt.Errorf("resolve terminal DriverRun work recovery authority: %w", err)
	}
	_, err = reconciler.cascades.RecoverTerminalDriverRunWork(ctx, auth, execution.RecoverTerminalDriverRunWorkCommand{
		WorkspaceKey: persisted.WorkspaceKey,
		RequestID: execution.RecoverTerminalDriverRunWorkRequestID(
			persisted.RunID,
			persisted.Status,
		),
		DriverRunID:  persisted.RunID,
		ParentStatus: persisted.Status,
		Reason:       childDriverRunCascadeReason(domain.DriverRunStatus(persisted.Status)),
		ErrorClass:   childDriverRunCascadeErrorClass,
		RecoveredAt:  persisted.OccurredAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("recover terminal DriverRun work: %w", err)
	}
	return nil
}

func (reconciler *RunOutcomeReconciler) recoverChildDriverRunCascade(
	ctx context.Context,
	persisted execution.DriverRunOutcome,
) error {
	if reconciler == nil || reconciler.cascades == nil || reconciler.authorities == nil {
		return execution.ErrUnavailable
	}
	status := persisted.Status
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx,
		persisted.WorkspaceKey,
		execution.ActionRecoverChildDriverRunCascade,
		string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		return fmt.Errorf("resolve child DriverRun cascade recovery authority: %w", err)
	}
	_, err = reconciler.cascades.RecoverChildDriverRunCascade(ctx, auth, execution.RecoverChildDriverRunCascadeCommand{
		WorkspaceKey: persisted.WorkspaceKey,
		RequestID: execution.CascadeChildDriverRunsRequestID(
			persisted.RunID,
			status,
		),
		ParentRunID:  persisted.RunID,
		ParentStatus: status,
		Reason:       childDriverRunCascadeReason(domain.DriverRunStatus(persisted.Status)),
		ErrorClass:   childDriverRunCascadeErrorClass,
		CascadedAt:   persisted.OccurredAt.UTC(),
		MaxDepth:     DefaultCompositionMaxDepth,
	})
	if err != nil {
		return fmt.Errorf("recover child DriverRun cascade: %w", err)
	}
	return nil
}

func (reconciler *RunOutcomeReconciler) retry(
	ctx context.Context,
	workspace, claimID string,
	persisted execution.DriverRunOutcome,
	now time.Time,
	cause error,
) error {
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionRetryDriverRunOutcome, reconciler.componentID,
	)
	if err != nil {
		return fmt.Errorf("resolve run outcome retry authority: %w", err)
	}
	if err := reconciler.queue.RetryDriverRunOutcome(ctx, auth, execution.RetryDriverRunOutcomeCommand{
		WorkspaceKey: workspace, RunID: persisted.RunID, ClaimID: claimID,
		Attempt: persisted.Attempt, FailedAt: now, Cause: cause.Error(),
	}); err != nil {
		return fmt.Errorf("schedule run outcome %q retry: %w", persisted.RunID, err)
	}
	return nil
}

func (reconciler *RunOutcomeReconciler) complete(
	ctx context.Context,
	workspace, claimID, runID string,
	now time.Time,
) error {
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionCompleteDriverRunOutcome, reconciler.componentID,
	)
	if err != nil {
		return fmt.Errorf("resolve run outcome completion authority: %w", err)
	}
	return reconciler.queue.CompleteDriverRunOutcome(ctx, auth, execution.CompleteDriverRunOutcomeCommand{
		WorkspaceKey: workspace, RunID: runID, ClaimID: claimID, CompletedAt: now,
	})
}

func (reconciler *RunOutcomeReconciler) retryTerminalDriverRunWorkRecovery(
	ctx context.Context,
	workspace, claimID string,
	persisted execution.DriverRunOutcome,
	now time.Time,
	cause error,
) error {
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionRetryTerminalDriverRunWorkRecovery, reconciler.componentID,
	)
	if err != nil {
		return fmt.Errorf("resolve terminal DriverRun work recovery retry authority: %w", err)
	}
	attempt := persisted.Attempt
	if attempt < 1 {
		attempt = 1
	}
	if err := reconciler.terminalWorkQueue.RetryTerminalDriverRunWorkRecovery(
		ctx,
		auth,
		execution.RetryTerminalDriverRunWorkRecoveryCommand{
			WorkspaceKey: workspace, RunID: persisted.RunID, ClaimID: claimID,
			Attempt: attempt, FailedAt: now, Cause: cause.Error(),
		},
	); err != nil {
		return fmt.Errorf("schedule terminal DriverRun work recovery %q retry: %w", persisted.RunID, err)
	}
	return nil
}

func (reconciler *RunOutcomeReconciler) completeTerminalDriverRunWorkRecovery(
	ctx context.Context,
	workspace, claimID, runID string,
	now time.Time,
) error {
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionCompleteTerminalDriverRunWorkRecovery, reconciler.componentID,
	)
	if err != nil {
		return fmt.Errorf("resolve terminal DriverRun work recovery completion authority: %w", err)
	}
	return reconciler.terminalWorkQueue.CompleteTerminalDriverRunWorkRecovery(
		ctx,
		auth,
		execution.CompleteTerminalDriverRunWorkRecoveryCommand{
			WorkspaceKey: workspace, RunID: runID, ClaimID: claimID, CompletedAt: now,
		},
	)
}

func persistedRunOutcome(persisted execution.DriverRunOutcome) (RunOutcome, error) {
	if persisted.WorkspaceKey == "" || persisted.RunID == "" || !persisted.Status.IsTerminal() || persisted.OccurredAt.IsZero() {
		return RunOutcome{}, fmt.Errorf("invalid persisted run outcome for %q", persisted.RunID)
	}
	payload, err := marshalBoundedRunFinishedPayload(
		persisted.RunID, domain.DriverRunStatus(persisted.Status), persisted.Summary, persisted.ErrorClass, persisted.ParentRunID,
	)
	if err != nil {
		return RunOutcome{}, fmt.Errorf("encode persisted run outcome %q: %w", persisted.RunID, err)
	}
	return RunOutcome{
		WorkspaceKey:  persisted.WorkspaceKey,
		EventID:       RunFinishedEventID(persisted.RunID, domain.DriverRunStatus(persisted.Status)),
		EventType:     RunFinishedEventType,
		RunID:         persisted.RunID,
		Status:        domain.DriverRunStatus(persisted.Status),
		ActorRef:      RunFinishedActor,
		ParentEventID: persisted.ParentEventID,
		EpicID:        persisted.EpicID,
		OccurredAt:    persisted.OccurredAt.UTC(),
		Payload:       payload,
	}, nil
}

func newRunOutcomeClaimPrefix() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return "run-outcome-" + hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("run-outcome-%d", time.Now().UTC().UnixNano())
}
