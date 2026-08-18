package driver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type runOutcomeCascadeProbe struct {
	execution.DriverRunAPI
	terminalCommands []execution.RecoverTerminalDriverRunWorkCommand
	commands         []execution.RecoverChildDriverRunCascadeCommand
	terminalErr      error
	terminalErrors   map[string]error
	err              error
}

type recordingRunOutcomePublisher struct {
	mu       sync.Mutex
	outcomes []RunOutcome
	err      error
}

func (publisher *recordingRunOutcomePublisher) PublishRunOutcome(_ context.Context, outcome RunOutcome) error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	outcome.Payload = append(json.RawMessage(nil), outcome.Payload...)
	publisher.outcomes = append(publisher.outcomes, outcome)
	return publisher.err
}

func (publisher *recordingRunOutcomePublisher) snapshot() []RunOutcome {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return append([]RunOutcome(nil), publisher.outcomes...)
}

func (probe *runOutcomeCascadeProbe) RecoverTerminalDriverRunWork(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverTerminalDriverRunWorkCommand,
) (execution.RecoverTerminalDriverRunWorkResult, error) {
	probe.terminalCommands = append(probe.terminalCommands, command)
	if err := probe.terminalErrors[command.DriverRunID]; err != nil {
		return execution.RecoverTerminalDriverRunWorkResult{}, err
	}
	if probe.terminalErr != nil {
		return execution.RecoverTerminalDriverRunWorkResult{}, probe.terminalErr
	}
	return execution.RecoverTerminalDriverRunWorkResult{ActionID: command.RequestID}, nil
}

type runOutcomeQueueAPIProbe struct {
	outcomes    []execution.DriverRunOutcome
	claimErr    error
	claims      []execution.ClaimDriverRunOutcomesCommand
	completions []execution.CompleteDriverRunOutcomeCommand
	retries     []execution.RetryDriverRunOutcomeCommand
}

func (probe *runOutcomeQueueAPIProbe) ClaimDriverRunOutcomes(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.ClaimDriverRunOutcomesCommand,
) ([]execution.DriverRunOutcome, error) {
	probe.claims = append(probe.claims, command)
	return append([]execution.DriverRunOutcome(nil), probe.outcomes...), probe.claimErr
}

func (probe *runOutcomeQueueAPIProbe) CompleteDriverRunOutcome(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.CompleteDriverRunOutcomeCommand,
) error {
	probe.completions = append(probe.completions, command)
	return nil
}

func (probe *runOutcomeQueueAPIProbe) RetryDriverRunOutcome(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RetryDriverRunOutcomeCommand,
) error {
	probe.retries = append(probe.retries, command)
	return nil
}

type terminalWorkRecoveryQueueAPIProbe struct {
	outcomes    []execution.DriverRunOutcome
	claimErr    error
	claims      []execution.ClaimTerminalDriverRunWorkRecoveriesCommand
	completions []execution.CompleteTerminalDriverRunWorkRecoveryCommand
	retries     []execution.RetryTerminalDriverRunWorkRecoveryCommand
}

func (probe *terminalWorkRecoveryQueueAPIProbe) ClaimTerminalDriverRunWorkRecoveries(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.ClaimTerminalDriverRunWorkRecoveriesCommand,
) ([]execution.DriverRunOutcome, error) {
	probe.claims = append(probe.claims, command)
	return append([]execution.DriverRunOutcome(nil), probe.outcomes...), probe.claimErr
}

func (probe *terminalWorkRecoveryQueueAPIProbe) CompleteTerminalDriverRunWorkRecovery(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.CompleteTerminalDriverRunWorkRecoveryCommand,
) error {
	probe.completions = append(probe.completions, command)
	return nil
}

func (probe *terminalWorkRecoveryQueueAPIProbe) RetryTerminalDriverRunWorkRecovery(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RetryTerminalDriverRunWorkRecoveryCommand,
) error {
	probe.retries = append(probe.retries, command)
	return nil
}

func (probe *runOutcomeCascadeProbe) RecoverChildDriverRunCascade(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverChildDriverRunCascadeCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	probe.commands = append(probe.commands, command)
	if probe.err != nil {
		return execution.CascadeChildDriverRunsResult{}, probe.err
	}
	return execution.CascadeChildDriverRunsResult{ActionID: command.RequestID}, nil
}

type runOutcomeCascadeAuthorityProbe struct {
	action              authority.Action
	componentID         string
	terminalAction      authority.Action
	terminalComponentID string
	delegate            execution.SystemAuthorityResolver
}

func (probe *runOutcomeCascadeAuthorityProbe) ResolveExecutionSystemAuthority(
	ctx context.Context,
	workspace string,
	action authority.Action,
	componentID string,
) (authority.SystemAuthority, error) {
	if action == execution.ActionRecoverChildDriverRunCascade {
		probe.action = action
		probe.componentID = componentID
	}
	if action == execution.ActionRecoverTerminalDriverRunWork {
		probe.terminalAction = action
		probe.terminalComponentID = componentID
	}
	return probe.delegate.ResolveExecutionSystemAuthority(ctx, workspace, action, componentID)
}

func TestRunOutcomeReconcilerFailureRestartConvergesWithoutDuplicate(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver", Name: "driver",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey: "WS", DriverID: "driver", VersionID: "v1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver", DriverVersionID: "v1", EpicID: "WS-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "WS", created.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	final, err := st.DriverRuns().Finish(ctx, "WS", created.RunID, execution.DriverRunFinish{
		NodeID: "node", LeaseID: "lease", FencingToken: claimed.FencingToken,
		Status: execution.DriverRunFailed, Summary: "runtime failed", ErrorClass: "driver_runtime",
	})
	if err != nil {
		t.Fatal(err)
	}

	failing := &recordingRunOutcomePublisher{err: errors.New("automation unavailable")}
	outbox := st.DriverRuns().(execution.DriverRunOutcomeStore)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	journal := st.TriggerEvents().(automation.TriggerEventAppender)
	first, err := newTestRunOutcomeReconciler(outbox, notifier, journal, failing, "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstNow := final.FinishedAt.Add(time.Millisecond)
	if err := first.RunOnce(ctx, firstNow); err == nil {
		t.Fatal("failed publication returned nil")
	}
	if got := len(failing.snapshot()); got != 1 {
		t.Fatalf("failed publisher calls = %d, want 1", got)
	}

	// A new reconciler models a process restart. The persisted retry time keeps
	// it quiet before backoff, then the same deterministic event converges.
	succeeding := &recordingRunOutcomePublisher{}
	restarted, err := newTestRunOutcomeReconciler(outbox, notifier, journal, succeeding, "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RunOnce(ctx, firstNow.Add(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got := len(succeeding.snapshot()); got != 0 {
		t.Fatalf("publication before persisted retry = %d", got)
	}
	if err := restarted.RunOnce(ctx, firstNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	got := succeeding.snapshot()
	if len(got) != 1 || got[0].EventID != "run-finished:run-1:failed" || got[0].EpicID != "WS-1" {
		t.Fatalf("restarted outcomes = %+v", got)
	}
	if err := restarted.RunOnce(ctx, firstNow.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := len(succeeding.snapshot()); got != 1 {
		t.Fatalf("completed outcome republished %d times, want 1", got)
	}
}

func TestRunOutcomeReconcilerRecoversFinalizeBeforeCascadeCrash(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	journal := st.TriggerEvents().(automation.TriggerEventAppender)
	queue, queueAuthorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		t.Fatal(err)
	}
	authorities := &runOutcomeCascadeAuthorityProbe{delegate: queueAuthorities}
	responseLost := &runOutcomeCascadeProbe{err: errors.New("response lost before cascade acknowledgement")}
	first, err := NewRunOutcomeReconcilerWithExecution(
		queue, noOpTerminalDriverRunWorkRecoveryQueue{}, notifier, journal, nil, "WS", nil, responseLost, authorities,
		string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstNow := finishedAt.Add(time.Millisecond)
	if err := first.RunOnce(t.Context(), firstNow); err == nil {
		t.Fatal("lost cascade response returned nil")
	}
	if len(responseLost.commands) != 1 {
		t.Fatalf("cascade attempts = %d, want 1", len(responseLost.commands))
	}
	if len(responseLost.terminalCommands) != 1 {
		t.Fatalf("terminal work recovery attempts = %d, want 1", len(responseLost.terminalCommands))
	}
	terminalCommand := responseLost.terminalCommands[0]
	if terminalCommand.DriverRunID != "child" || terminalCommand.ParentStatus != execution.DriverRunCompleted ||
		terminalCommand.RequestID != execution.RecoverTerminalDriverRunWorkRequestID("child", execution.DriverRunCompleted) ||
		!terminalCommand.RecoveredAt.Equal(finishedAt) {
		t.Fatalf("terminal work recovery command = %+v", terminalCommand)
	}
	command := responseLost.commands[0]
	if command.ParentRunID != "child" || command.ParentStatus != execution.DriverRunCompleted ||
		command.RequestID != execution.CascadeChildDriverRunsRequestID("child", execution.DriverRunCompleted) ||
		!command.CascadedAt.Equal(finishedAt) || command.MaxDepth != DefaultCompositionMaxDepth {
		t.Fatalf("recovery command = %+v", command)
	}
	if command.Reason != childDriverRunCascadeReason(execution.DriverRunCompleted) || command.ErrorClass != childDriverRunCascadeErrorClass {
		t.Fatalf("recovery policy = reason %q class %q", command.Reason, command.ErrorClass)
	}
	if authorities.action != execution.ActionRecoverChildDriverRunCascade ||
		authorities.componentID != string(execution.DriverRunOutcomeComponentID) {
		t.Fatalf("authority = action %q component %q", authorities.action, authorities.componentID)
	}

	// A fresh process retries the same durable action identity, then may
	// complete the outcome row. This is the finalize -> crash recovery proof.
	replayed := &runOutcomeCascadeProbe{}
	restarted, err := NewRunOutcomeReconcilerWithExecution(
		queue, noOpTerminalDriverRunWorkRecoveryQueue{}, notifier, journal, nil, "WS", nil, replayed, authorities,
		string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RunOnce(t.Context(), firstNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(replayed.commands) != 1 || replayed.commands[0].RequestID != command.RequestID {
		t.Fatalf("replayed cascade commands = %+v, want request %q", replayed.commands, command.RequestID)
	}
	if len(replayed.terminalCommands) != 1 || replayed.terminalCommands[0].RequestID != terminalCommand.RequestID {
		t.Fatalf("replayed terminal work commands = %+v, want request %q", replayed.terminalCommands, terminalCommand.RequestID)
	}
	assertNoClaimableRunOutcomes(t, outbox, firstNow.Add(time.Hour))
}

func TestRunOutcomeReconcilerRetriesTerminalWorkBeforeChildCascade(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	queue, queueAuthorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		t.Fatal(err)
	}
	probe := &runOutcomeCascadeProbe{terminalErr: errors.New("terminal work response lost")}
	reconciler, err := NewRunOutcomeReconcilerWithExecution(
		queue, noOpTerminalDriverRunWorkRecoveryQueue{}, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
		probe, queueAuthorities, string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err == nil {
		t.Fatal("terminal work recovery failure returned nil")
	}
	if len(probe.terminalCommands) != 1 {
		t.Fatalf("terminal work attempts = %d, want 1", len(probe.terminalCommands))
	}
	if len(probe.commands) != 0 {
		t.Fatalf("child cascade ran before terminal work converged: %+v", probe.commands)
	}
}

func TestRunOutcomeReconcilerTerminalWorkQueueContinuesAfterRowFailure(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	_, authorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := &runOutcomeQueueAPIProbe{}
	recovery := &terminalWorkRecoveryQueueAPIProbe{outcomes: []execution.DriverRunOutcome{
		{WorkspaceKey: "WS", RunID: "run-fails", Status: execution.DriverRunFailed, OccurredAt: finishedAt, Attempt: 1},
		{WorkspaceKey: "WS", RunID: "run-succeeds", Status: execution.DriverRunCompleted, OccurredAt: finishedAt.Add(time.Second), Attempt: 1},
	}}
	cascades := &runOutcomeCascadeProbe{terminalErrors: map[string]error{"run-fails": errors.New("temporary conflict")}}
	reconciler, err := NewRunOutcomeReconcilerWithExecution(
		ordinary, recovery, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
		cascades, authorities, string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := reconciler.DrainOnce(t.Context(), finishedAt.Add(2*time.Second))
	if claimed != 2 || err == nil || !strings.Contains(err.Error(), "temporary conflict") {
		t.Fatalf("claimed=%d error=%v, want two rows and isolated first-row failure", claimed, err)
	}
	if len(cascades.terminalCommands) != 2 || len(cascades.commands) != 0 {
		t.Fatalf("terminal commands=%+v child cascades=%+v", cascades.terminalCommands, cascades.commands)
	}
	if len(recovery.retries) != 1 || recovery.retries[0].RunID != "run-fails" ||
		len(recovery.completions) != 1 || recovery.completions[0].RunID != "run-succeeds" {
		t.Fatalf("recovery retries=%+v completions=%+v", recovery.retries, recovery.completions)
	}
	if len(ordinary.claims) != 1 || len(ordinary.completions) != 0 || len(ordinary.retries) != 0 {
		t.Fatalf("ordinary lane was mutated by recovery lane: %+v", ordinary)
	}
}

func TestRunOutcomeReconcilerRecoveryClaimFailureDoesNotBlockOrdinaryDelivery(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	_, authorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		t.Fatal(err)
	}
	persisted := execution.DriverRunOutcome{
		WorkspaceKey: "WS", RunID: "same-run", Status: execution.DriverRunCompleted, OccurredAt: finishedAt, Attempt: 1,
	}
	ordinary := &runOutcomeQueueAPIProbe{outcomes: []execution.DriverRunOutcome{persisted}}
	recovery := &terminalWorkRecoveryQueueAPIProbe{claimErr: errors.New("recovery queue unavailable")}
	cascades := &runOutcomeCascadeProbe{}
	reconciler, err := NewRunOutcomeReconcilerWithExecution(
		ordinary, recovery, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
		cascades, authorities, string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := reconciler.DrainOnce(t.Context(), finishedAt.Add(time.Second))
	if claimed != 1 || err == nil || !strings.Contains(err.Error(), "recovery queue unavailable") {
		t.Fatalf("claimed=%d error=%v, want ordinary delivery plus recovery claim error", claimed, err)
	}
	if len(ordinary.completions) != 1 || ordinary.completions[0].RunID != persisted.RunID || len(ordinary.retries) != 0 {
		t.Fatalf("ordinary completions=%+v retries=%+v", ordinary.completions, ordinary.retries)
	}
	if len(cascades.terminalCommands) != 1 || len(cascades.commands) != 1 {
		t.Fatalf("ordinary deterministic work did not run: terminal=%+v cascade=%+v", cascades.terminalCommands, cascades.commands)
	}
}

func TestRunOutcomeReconcilerOrdinaryClaimFailureDoesNotBlockRecovery(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	_, authorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := &runOutcomeQueueAPIProbe{claimErr: errors.New("ordinary queue unavailable")}
	recovery := &terminalWorkRecoveryQueueAPIProbe{outcomes: []execution.DriverRunOutcome{{
		WorkspaceKey: "WS", RunID: "recovery-run", Status: execution.DriverRunFailed, OccurredAt: finishedAt, Attempt: 1,
	}}}
	cascades := &runOutcomeCascadeProbe{}
	reconciler, err := NewRunOutcomeReconcilerWithExecution(
		ordinary, recovery, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
		cascades, authorities, string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := reconciler.DrainOnce(t.Context(), finishedAt.Add(time.Second))
	if claimed != 1 || err == nil || !strings.Contains(err.Error(), "ordinary queue unavailable") {
		t.Fatalf("claimed=%d error=%v, want recovery completion plus ordinary claim error", claimed, err)
	}
	if len(recovery.completions) != 1 || recovery.completions[0].RunID != "recovery-run" || len(recovery.retries) != 0 {
		t.Fatalf("recovery completions=%+v retries=%+v", recovery.completions, recovery.retries)
	}
	if len(cascades.terminalCommands) != 1 || len(cascades.commands) != 0 {
		t.Fatalf("recovery lane crossed into child cascade: terminal=%+v cascade=%+v", cascades.terminalCommands, cascades.commands)
	}
}

func TestRunOutcomeReconcilerSameRunInterleavingUsesDeterministicRecoveryAndSeparateClaims(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	_, authorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		t.Fatal(err)
	}
	persisted := execution.DriverRunOutcome{
		WorkspaceKey: "WS", RunID: "interleaved-run", Status: execution.DriverRunNeedsReview,
		OccurredAt: finishedAt, Attempt: 1,
	}
	ordinary := &runOutcomeQueueAPIProbe{outcomes: []execution.DriverRunOutcome{persisted}}
	recovery := &terminalWorkRecoveryQueueAPIProbe{outcomes: []execution.DriverRunOutcome{persisted}}
	cascades := &runOutcomeCascadeProbe{}
	reconciler, err := NewRunOutcomeReconcilerWithExecution(
		ordinary, recovery, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
		cascades, authorities, string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := reconciler.DrainOnce(t.Context(), finishedAt.Add(time.Second))
	if err != nil || claimed != 2 {
		t.Fatalf("claimed=%d error=%v, want both queue rows", claimed, err)
	}
	if len(cascades.terminalCommands) != 2 {
		t.Fatalf("terminal recovery commands=%+v", cascades.terminalCommands)
	}
	first, second := cascades.terminalCommands[0], cascades.terminalCommands[1]
	if first.RequestID != second.RequestID || first.RequestID != execution.RecoverTerminalDriverRunWorkRequestID(persisted.RunID, persisted.Status) ||
		!first.RecoveredAt.Equal(persisted.OccurredAt) || !second.RecoveredAt.Equal(persisted.OccurredAt) {
		t.Fatalf("interleaved deterministic commands=%+v / %+v", first, second)
	}
	if len(recovery.claims) != 1 || len(ordinary.claims) != 1 || recovery.claims[0].ClaimID == ordinary.claims[0].ClaimID ||
		!strings.Contains(recovery.claims[0].ClaimID, "terminal-work") {
		t.Fatalf("recovery claims=%+v ordinary claims=%+v", recovery.claims, ordinary.claims)
	}
	if len(recovery.completions) != 1 || len(ordinary.completions) != 1 || len(cascades.commands) != 1 {
		t.Fatalf("recovery complete=%+v ordinary complete=%+v child cascades=%+v", recovery.completions, ordinary.completions, cascades.commands)
	}
}

func TestRunOutcomeReconcilerRecoversCompositionWithoutSynchronousEmitOrAutomation(t *testing.T) {
	st, outbox, parent, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := newTestRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	assertParentResumedByChildOutcome(t, st, parent.RunID)
	assertNoClaimableRunOutcomes(t, outbox, finishedAt.Add(time.Hour))
}

func TestRunOutcomeReconcilerJournalsBeforeLateRegistrationWithoutAutomation(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := newTestRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	lateParent := createClaimedOutcomeParent(t, st, "late-parent")
	result, err := st.Awaits().RegisterAwaitAndCheck(t.Context(), "WS", execution.AwaitRegistration{
		InstanceKey: execution.AwaitInstanceKey(lateParent.RunID, 1), RunID: lateParent.RunID,
		Pattern: RunFinishedSubjectKey("child"), ActorAllow: []string{RunFinishedActor},
		Deadline: time.Now().Add(time.Hour).UTC(),
	})
	if err != nil || !result.Satisfied || result.Instance == nil ||
		result.Instance.SatisfiedByEventID != RunFinishedEventID("child", execution.DriverRunCompleted) ||
		result.Instance.SatisfiedActor != RunFinishedActor {
		t.Fatalf("late registration = %+v, %v", result, err)
	}
}

func TestRunOutcomeReconcilerJournalClosesListRegisterRace(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	inner, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	lateParent := createClaimedOutcomeParent(t, st, "race-parent")
	notifier := &registerAfterRunOutcomeListNotifier{
		inner: inner, awaits: st.Awaits(), parent: lateParent,
	}
	reconciler, err := newTestRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if notifier.result == nil || !notifier.result.Satisfied || notifier.result.Instance == nil ||
		notifier.result.Instance.SatisfiedByEventID != RunFinishedEventID("child", execution.DriverRunCompleted) {
		t.Fatalf("registration after notifier list = %+v", notifier.result)
	}
}

func TestRunOutcomeReconcilerBoundsHugeTerminalPayloadWithoutStrandingParent(t *testing.T) {
	st, outbox, parent, finishedAt := setupDurableCompositionOutcomeWithDetails(
		t,
		strings.Repeat("summary\x00", 20000),
		strings.Repeat("error\x00", 10000),
	)
	notifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := newTestRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(automation.TriggerEventAppender), nil, "WS", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	assertParentResumedByChildOutcome(t, st, parent.RunID)
	instance, err := st.Awaits().GetSatisfiedAwait(t.Context(), "WS", execution.AwaitInstanceKey(parent.RunID, 1))
	if err != nil || len(instance.SatisfiedPayload) > execution.DefaultAwaitResumePayloadCap {
		t.Fatalf("bounded satisfied payload = %d bytes, %v", len(instance.SatisfiedPayload), err)
	}
	var payload runFinishedPayload
	if err := json.Unmarshal(instance.SatisfiedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RunID != "child" || payload.Status != string(execution.DriverRunCompleted) || !payload.Truncated {
		t.Fatalf("bounded payload = %+v", payload)
	}
}

func TestRunOutcomeReconcilerResponseLossAfterAtomicResolveConvergesOnRestart(t *testing.T) {
	st, outbox, parent, finishedAt := setupDurableCompositionOutcome(t)
	atomicNotifier, err := newTestRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	losingResponse := &responseLossRunOutcomeAwaitNotifier{inner: atomicNotifier}
	journal := st.TriggerEvents().(automation.TriggerEventAppender)
	first, err := newTestRunOutcomeReconciler(outbox, losingResponse, journal, nil, "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstNow := finishedAt.Add(time.Millisecond)
	if err := first.RunOnce(t.Context(), firstNow); err == nil {
		t.Fatal("lost atomic-command response returned nil")
	}
	// The atomic command committed before the response was lost: the parent
	// is already queued even though the durable outcome is scheduled to retry.
	assertParentResumedByChildOutcome(t, st, parent.RunID)

	restarted, err := newTestRunOutcomeReconciler(outbox, atomicNotifier, journal, nil, "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RunOnce(t.Context(), firstNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertParentResumedByChildOutcome(t, st, parent.RunID)
	assertNoClaimableRunOutcomes(t, outbox, firstNow.Add(time.Hour))
}

type responseLossRunOutcomeAwaitNotifier struct {
	inner RunOutcomeAwaitNotifier
	lost  bool
}

type registerAfterRunOutcomeListNotifier struct {
	inner  RunOutcomeAwaitNotifier
	awaits execution.AwaitStore
	parent *execution.DriverRunRecord
	result *execution.AwaitRegistrationResult
}

func (notifier *registerAfterRunOutcomeListNotifier) NotifyRunOutcomeAwaits(ctx context.Context, outcome RunOutcome) error {
	if err := notifier.inner.NotifyRunOutcomeAwaits(ctx, outcome); err != nil {
		return err
	}
	result, err := notifier.awaits.RegisterAwaitAndCheck(ctx, outcome.WorkspaceKey, execution.AwaitRegistration{
		InstanceKey: execution.AwaitInstanceKey(notifier.parent.RunID, 1), RunID: notifier.parent.RunID,
		Pattern: RunFinishedSubjectKey(outcome.RunID), ActorAllow: []string{RunFinishedActor},
		Deadline: time.Now().Add(time.Hour).UTC(),
	})
	notifier.result = result
	return err
}

func (notifier *responseLossRunOutcomeAwaitNotifier) NotifyRunOutcomeAwaits(ctx context.Context, outcome RunOutcome) error {
	if err := notifier.inner.NotifyRunOutcomeAwaits(ctx, outcome); err != nil {
		return err
	}
	if !notifier.lost {
		notifier.lost = true
		return errors.New("response lost after atomic await commit")
	}
	return nil
}

func setupDurableCompositionOutcome(
	t *testing.T,
) (*memstore.Store, execution.DriverRunOutcomeStore, *execution.DriverRunRecord, time.Time) {
	return setupDurableCompositionOutcomeWithDetails(t, "done", "")
}

func setupDurableCompositionOutcomeWithDetails(
	t *testing.T,
	summary, errorClass string,
) (*memstore.Store, execution.DriverRunOutcomeStore, *execution.DriverRunRecord, time.Time) {
	t.Helper()
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver", Name: "driver",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey: "WS", DriverID: "driver", VersionID: "v1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatal(err)
	}
	parent, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "parent", DriverID: "driver", DriverVersionID: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err = st.DriverRuns().Claim(ctx, "WS", parent.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	instanceKey := execution.AwaitInstanceKey(parent.RunID, 1)
	if _, err := st.Awaits().RegisterAwaitAndCheck(ctx, "WS", execution.AwaitRegistration{
		InstanceKey: instanceKey, RunID: parent.RunID, Pattern: RunFinishedSubjectKey("child"),
		ActorAllow: []string{RunFinishedActor}, Deadline: time.Now().Add(time.Hour).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, "WS", parent.RunID, "node", "lease", parent.FencingToken, instanceKey); err != nil {
		t.Fatal(err)
	}
	child, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "child", DriverID: "driver", DriverVersionID: "v1", ParentRunID: parent.RunID,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err = st.DriverRuns().Claim(ctx, "WS", child.RunID, "child-node", "child-lease")
	if err != nil {
		t.Fatal(err)
	}
	final, err := st.DriverRuns().Finish(ctx, "WS", child.RunID, execution.DriverRunFinish{
		NodeID: "child-node", LeaseID: "child-lease", FencingToken: child.FencingToken,
		Status: execution.DriverRunCompleted, Summary: summary, ErrorClass: errorClass,
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, st.DriverRuns().(execution.DriverRunOutcomeStore), parent, final.FinishedAt.UTC()
}

func createClaimedOutcomeParent(t *testing.T, st *memstore.Store, runID string) *execution.DriverRunRecord {
	t.Helper()
	run, err := st.DriverRuns().Create(t.Context(), execution.DriverRunCreate{
		WorkspaceKey: "WS", RunID: runID, DriverID: "driver", DriverVersionID: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = st.DriverRuns().Claim(t.Context(), "WS", run.RunID, "node-"+runID, "lease-"+runID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func assertParentResumedByChildOutcome(t *testing.T, st *memstore.Store, runID string) {
	t.Helper()
	run, err := st.DriverRuns().Get(t.Context(), "WS", runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != execution.DriverRunQueued || run.ResumeSourceEventID != RunFinishedEventID("child", execution.DriverRunCompleted) {
		t.Fatalf("parent run = %+v", run)
	}
}

func assertNoClaimableRunOutcomes(t *testing.T, outbox execution.DriverRunOutcomeStore, now time.Time) {
	t.Helper()
	values, err := outbox.ClaimDriverRunOutcomes(t.Context(), execution.DriverRunOutcomeLease{
		WorkspaceKey: "WS", ClaimID: "assert-completed", Before: now, ClaimUntil: now.Add(time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("claimable completed outcomes = %+v", values)
	}
}

func newTestRunOutcomeAwaitNotifier(awaits execution.AwaitStore) (RunOutcomeAwaitNotifier, error) {
	resolver, ok := awaits.(execution.RunOutcomeAwaitStore)
	if !ok {
		return nil, errors.New("test await store lacks run-outcome resolver")
	}
	return NewRunOutcomeAwaitNotifierWithResolver(awaits, resolver)
}
