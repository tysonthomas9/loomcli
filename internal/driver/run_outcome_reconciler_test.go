package driver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type runOutcomeCascadeProbe struct {
	execution.DriverRunAPI
	commands []execution.RecoverChildDriverRunCascadeCommand
	err      error
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
	action      authority.Action
	componentID string
	delegate    execution.SystemAuthorityResolver
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
	return probe.delegate.ResolveExecutionSystemAuthority(ctx, workspace, action, componentID)
}

func TestRunOutcomeReconcilerFailureRestartConvergesWithoutDuplicate(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver", Name: "driver",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", DriverID: "driver", VersionID: "v1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver", DriverVersionID: "v1", EpicID: "WS-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "WS", created.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	final, err := st.DriverRuns().Finish(ctx, "WS", created.RunID, store.DriverRunFinish{
		NodeID: "node", LeaseID: "lease", FencingToken: claimed.FencingToken,
		Status: domain.DriverRunFailed, Summary: "runtime failed", ErrorClass: "driver_runtime",
	})
	if err != nil {
		t.Fatal(err)
	}

	failing := &recordingRunOutcomePublisher{err: errors.New("automation unavailable")}
	outbox := st.DriverRuns().(store.DriverRunOutcomeStore)
	notifier, err := NewRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	journal := st.TriggerEvents().(store.TriggerEventAppender)
	first, err := NewRunOutcomeReconciler(outbox, notifier, journal, failing, "WS", nil)
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
	restarted, err := NewRunOutcomeReconciler(outbox, notifier, journal, succeeding, "WS", nil)
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
	notifier, err := NewRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	journal := st.TriggerEvents().(store.TriggerEventAppender)
	queue, queueAuthorities, err := testRunOutcomeQueue(outbox)
	if err != nil {
		t.Fatal(err)
	}
	authorities := &runOutcomeCascadeAuthorityProbe{delegate: queueAuthorities}
	responseLost := &runOutcomeCascadeProbe{err: errors.New("response lost before cascade acknowledgement")}
	first, err := NewRunOutcomeReconcilerWithExecution(
		queue, notifier, journal, nil, "WS", nil, responseLost, authorities,
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
	command := responseLost.commands[0]
	if command.ParentRunID != "child" || command.ParentStatus != execution.DriverRunCompleted ||
		command.RequestID != execution.CascadeChildDriverRunsRequestID("child", execution.DriverRunCompleted) ||
		!command.CascadedAt.Equal(finishedAt) || command.MaxDepth != DefaultCompositionMaxDepth {
		t.Fatalf("recovery command = %+v", command)
	}
	if command.Reason != childDriverRunCascadeReason(domain.DriverRunCompleted) || command.ErrorClass != childDriverRunCascadeErrorClass {
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
		queue, notifier, journal, nil, "WS", nil, replayed, authorities,
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
	assertNoClaimableRunOutcomes(t, outbox, firstNow.Add(time.Hour))
}

func TestRunOutcomeReconcilerRecoversCompositionWithoutSynchronousEmitOrAutomation(t *testing.T) {
	st, outbox, parent, finishedAt := setupDurableCompositionOutcome(t)
	notifier, err := NewRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(store.TriggerEventAppender), nil, "WS", nil,
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
	notifier, err := NewRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(store.TriggerEventAppender), nil, "WS", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	lateParent := createClaimedOutcomeParent(t, st, "late-parent")
	result, err := st.Awaits().RegisterAwaitAndCheck(t.Context(), "WS", store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey(lateParent.RunID, 1), RunID: lateParent.RunID,
		Pattern: RunFinishedSubjectKey("child"), ActorAllow: []string{RunFinishedActor},
		Deadline: time.Now().Add(time.Hour).UTC(),
	})
	if err != nil || !result.Satisfied || result.Instance == nil ||
		result.Instance.SatisfiedByEventID != RunFinishedEventID("child", domain.DriverRunCompleted) ||
		result.Instance.SatisfiedActor != RunFinishedActor {
		t.Fatalf("late registration = %+v, %v", result, err)
	}
}

func TestRunOutcomeReconcilerJournalClosesListRegisterRace(t *testing.T) {
	st, outbox, _, finishedAt := setupDurableCompositionOutcome(t)
	inner, err := NewRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	lateParent := createClaimedOutcomeParent(t, st, "race-parent")
	notifier := &registerAfterRunOutcomeListNotifier{
		inner: inner, awaits: st.Awaits(), parent: lateParent,
	}
	reconciler, err := NewRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(store.TriggerEventAppender), nil, "WS", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if notifier.result == nil || !notifier.result.Satisfied || notifier.result.Instance == nil ||
		notifier.result.Instance.SatisfiedByEventID != RunFinishedEventID("child", domain.DriverRunCompleted) {
		t.Fatalf("registration after notifier list = %+v", notifier.result)
	}
}

func TestRunOutcomeReconcilerBoundsHugeTerminalPayloadWithoutStrandingParent(t *testing.T) {
	st, outbox, parent, finishedAt := setupDurableCompositionOutcomeWithDetails(
		t,
		strings.Repeat("summary\x00", 20000),
		strings.Repeat("error\x00", 10000),
	)
	notifier, err := NewRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewRunOutcomeReconciler(
		outbox, notifier, st.TriggerEvents().(store.TriggerEventAppender), nil, "WS", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(t.Context(), finishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	assertParentResumedByChildOutcome(t, st, parent.RunID)
	instance, err := st.Awaits().GetSatisfiedAwait(t.Context(), "WS", domain.AwaitInstanceKey(parent.RunID, 1))
	if err != nil || len(instance.SatisfiedPayload) > domain.DefaultAwaitResumePayloadCap {
		t.Fatalf("bounded satisfied payload = %d bytes, %v", len(instance.SatisfiedPayload), err)
	}
	var payload runFinishedPayload
	if err := json.Unmarshal(instance.SatisfiedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RunID != "child" || payload.Status != string(domain.DriverRunCompleted) || !payload.Truncated {
		t.Fatalf("bounded payload = %+v", payload)
	}
}

func TestRunOutcomeReconcilerResponseLossAfterAtomicResolveConvergesOnRestart(t *testing.T) {
	st, outbox, parent, finishedAt := setupDurableCompositionOutcome(t)
	atomicNotifier, err := NewRunOutcomeAwaitNotifier(st.Awaits())
	if err != nil {
		t.Fatal(err)
	}
	losingResponse := &responseLossRunOutcomeAwaitNotifier{inner: atomicNotifier}
	journal := st.TriggerEvents().(store.TriggerEventAppender)
	first, err := NewRunOutcomeReconciler(outbox, losingResponse, journal, nil, "WS", nil)
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

	restarted, err := NewRunOutcomeReconciler(outbox, atomicNotifier, journal, nil, "WS", nil)
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
	awaits store.AwaitStore
	parent *domain.DriverRun
	result *store.AwaitResult
}

func (notifier *registerAfterRunOutcomeListNotifier) NotifyRunOutcomeAwaits(ctx context.Context, outcome RunOutcome) error {
	if err := notifier.inner.NotifyRunOutcomeAwaits(ctx, outcome); err != nil {
		return err
	}
	result, err := notifier.awaits.RegisterAwaitAndCheck(ctx, outcome.WorkspaceKey, store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey(notifier.parent.RunID, 1), RunID: notifier.parent.RunID,
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
) (*memstore.Store, store.DriverRunOutcomeStore, *domain.DriverRun, time.Time) {
	return setupDurableCompositionOutcomeWithDetails(t, "done", "")
}

func setupDurableCompositionOutcomeWithDetails(
	t *testing.T,
	summary, errorClass string,
) (*memstore.Store, store.DriverRunOutcomeStore, *domain.DriverRun, time.Time) {
	t.Helper()
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver", Name: "driver",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", DriverID: "driver", VersionID: "v1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	parent, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "parent", DriverID: "driver", DriverVersionID: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err = st.DriverRuns().Claim(ctx, "WS", parent.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	instanceKey := domain.AwaitInstanceKey(parent.RunID, 1)
	if _, err := st.Awaits().RegisterAwaitAndCheck(ctx, "WS", store.AwaitRegistration{
		InstanceKey: instanceKey, RunID: parent.RunID, Pattern: RunFinishedSubjectKey("child"),
		ActorAllow: []string{RunFinishedActor}, Deadline: time.Now().Add(time.Hour).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, "WS", parent.RunID, "node", "lease", parent.FencingToken, instanceKey); err != nil {
		t.Fatal(err)
	}
	child, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "child", DriverID: "driver", DriverVersionID: "v1", ParentRunID: parent.RunID,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err = st.DriverRuns().Claim(ctx, "WS", child.RunID, "child-node", "child-lease")
	if err != nil {
		t.Fatal(err)
	}
	final, err := st.DriverRuns().Finish(ctx, "WS", child.RunID, store.DriverRunFinish{
		NodeID: "child-node", LeaseID: "child-lease", FencingToken: child.FencingToken,
		Status: domain.DriverRunCompleted, Summary: summary, ErrorClass: errorClass,
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, st.DriverRuns().(store.DriverRunOutcomeStore), parent, final.FinishedAt.UTC()
}

func createClaimedOutcomeParent(t *testing.T, st *memstore.Store, runID string) *domain.DriverRun {
	t.Helper()
	run, err := st.DriverRuns().Create(t.Context(), store.DriverRunCreate{
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

func assertParentResumedByChildOutcome(t *testing.T, st store.Store, runID string) {
	t.Helper()
	run, err := st.DriverRuns().Get(t.Context(), "WS", runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != RunFinishedEventID("child", domain.DriverRunCompleted) {
		t.Fatalf("parent run = %+v", run)
	}
}

func assertNoClaimableRunOutcomes(t *testing.T, outbox store.DriverRunOutcomeStore, now time.Time) {
	t.Helper()
	values, err := outbox.ClaimDriverRunOutcomes(t.Context(), store.DriverRunOutcomeClaim{
		WorkspaceKey: "WS", ClaimID: "assert-completed", Before: now, ClaimUntil: now.Add(time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("claimable completed outcomes = %+v", values)
	}
}
