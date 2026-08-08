package driver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/eventpolicy"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAwaitEventReconcilerResolvesPendingAwaitAndCompletesNotification(t *testing.T) {
	st, run, instanceKey := setupAwaitEventReconcileRun(t, "run-1", "approval.granted:deploy-1")
	payload := json.RawMessage(`{"approved":true}`)
	event := appendAwaitReconcileEvent(t, st, "stored-event-1", "approval-source-1", "approval.granted", "deploy-1", "alice", payload)
	outbox := st.TriggerEvents().(store.AwaitEventNotificationStore)
	reconciler, err := NewAwaitEventReconciler(outbox, testAwaitMatcher(t, st), "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := event.ReceivedAt.Add(time.Millisecond)
	if count, err := reconciler.DrainOnce(t.Context(), now); err != nil || count != 1 {
		t.Fatalf("DrainOnce = %d, %v", count, err)
	}
	assertAwaitEventReconciled(t, st, run.RunID, instanceKey, "approval-source-1", payload)
	if count, err := reconciler.DrainOnce(t.Context(), now.Add(time.Hour)); err != nil || count != 0 {
		t.Fatalf("completed replay DrainOnce = %d, %v; want empty", count, err)
	}
}

func TestAutomationAwaitEventNotifierRequiresExplicitResolver(t *testing.T) {
	st := memstore.New()
	if notifier, err := trigger.NewAutomationAwaitEventNotifier(st.Awaits(), st.DriverRuns()); err == nil || notifier != nil {
		t.Fatalf("legacy notifier = %T, %v; want fail-closed composition error", notifier, err)
	}
	if notifier, err := trigger.NewAutomationAwaitEventNotifierWithResolver(st.Awaits(), st.DriverRuns(), nil); err == nil || notifier != nil {
		t.Fatalf("nil-resolver notifier = %T, %v; want fail-closed composition error", notifier, err)
	}
}

func TestAwaitEventReconcilerResponseLossRetriesAndConverges(t *testing.T) {
	st, run, instanceKey := setupAwaitEventReconcileRun(t, "run-loss", "approval.granted:deploy-loss")
	event := appendAwaitReconcileEvent(t, st, "stored-event-loss", "approval-source-loss",
		"approval.granted", "deploy-loss", "alice", nil)
	outbox := st.TriggerEvents().(store.AwaitEventNotificationStore)
	dispatcher := &responseLossAwaitEventDispatcher{inner: testAwaitMatcher(t, st)}
	first, err := NewAwaitEventReconciler(outbox, dispatcher, "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := event.ReceivedAt.Add(time.Millisecond)
	if err := first.RunOnce(t.Context(), now); err == nil {
		t.Fatal("lost response returned nil")
	}
	assertAwaitEventReconciled(t, st, run.RunID, instanceKey, "approval-source-loss", nil)

	restarted, err := NewAwaitEventReconciler(outbox, testAwaitMatcher(t, st), "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := restarted.DrainOnce(t.Context(), now.Add(500*time.Millisecond)); err != nil || count != 0 {
		t.Fatalf("before retry DrainOnce = %d, %v", count, err)
	}
	if count, err := restarted.DrainOnce(t.Context(), now.Add(2*time.Second)); err != nil || count != 1 {
		t.Fatalf("retry DrainOnce = %d, %v", count, err)
	}
	assertAwaitEventReconciled(t, st, run.RunID, instanceKey, "approval-source-loss", nil)
}

func TestAwaitEventReconcilerCompletionKeepsRegistrationCatchUp(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	event := appendAwaitReconcileEvent(t, st, "stored-event-before", "approval-source-before",
		"approval.granted", "deploy-before", "alice", json.RawMessage(`{"approved":true}`))
	outbox := st.TriggerEvents().(store.AwaitEventNotificationStore)
	reconciler, err := NewAwaitEventReconciler(outbox, testAwaitMatcher(t, st), "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := reconciler.DrainOnce(ctx, event.ReceivedAt.Add(time.Millisecond)); err != nil || count != 1 {
		t.Fatalf("DrainOnce = %d, %v", count, err)
	}
	result, err := st.Awaits().RegisterAwaitAndCheck(ctx, "WS", store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey("run-later", 1), RunID: "run-later",
		Pattern: "approval.granted:deploy-before", ActorAllow: []string{"alice"},
		Deadline: time.Now().Add(time.Hour).UTC(),
	})
	if err != nil || !result.Satisfied || result.Instance.SatisfiedByEventID != "approval-source-before" {
		t.Fatalf("registration catch-up = %+v, %v", result, err)
	}
}

func TestAwaitEventReconcilerQuarantinesOversizedEventWithoutPoisoningLaterWinner(t *testing.T) {
	st, run, instanceKey := setupAwaitEventReconcileRun(t, "run-large", "approval.granted:deploy-large")
	oversized := json.RawMessage(make([]byte, domain.DefaultAwaitResumePayloadCap+1))
	first := appendAwaitReconcileEvent(t, st, "stored-event-large", "approval-source-large",
		"approval.granted", "deploy-large", "alice", oversized)
	outbox := st.TriggerEvents().(store.AwaitEventNotificationStore)
	reconciler, err := NewAwaitEventReconciler(outbox, testAwaitMatcher(t, st), "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := reconciler.DrainOnce(t.Context(), first.ReceivedAt.Add(time.Millisecond)); err != nil || count != 1 {
		t.Fatalf("oversized DrainOnce = %d, %v; want one audited no-op", count, err)
	}
	stillSuspended, err := st.DriverRuns().Get(t.Context(), "WS", run.RunID)
	if err != nil || stillSuspended.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("run after oversized event = %+v, %v; want suspended", stillSuspended, err)
	}
	if count, err := reconciler.DrainOnce(t.Context(), first.ReceivedAt.Add(time.Hour)); err != nil || count != 0 {
		t.Fatalf("quarantined replay DrainOnce = %d, %v", count, err)
	}
	validPayload := json.RawMessage(`{"approved":true}`)
	second := appendAwaitReconcileEvent(t, st, "stored-event-valid", "approval-source-valid",
		"approval.granted", "deploy-large", "alice", validPayload)
	if count, err := reconciler.DrainOnce(t.Context(), second.ReceivedAt.Add(time.Millisecond)); err != nil || count != 1 {
		t.Fatalf("valid DrainOnce = %d, %v", count, err)
	}
	assertAwaitEventReconciled(t, st, run.RunID, instanceKey, "approval-source-valid", validPayload)
}

func TestAwaitEventReconcilerCompletesForgedRunFinishedAndLaterGenuineEventWins(t *testing.T) {
	pattern := domain.AwaitEventKey(eventpolicy.RunFinishedEventType, "child-1")
	st, run, instanceKey := setupAwaitEventReconcileRunWithActors(
		t, "run-parent", pattern, []string{eventpolicy.RunFinishedActorRef},
	)
	payload := json.RawMessage(`{"runId":"child-1","status":"completed"}`)
	canonicalID := eventpolicy.RunFinishedSourceEventIDPrefix + "child-1:completed"
	forged := appendAwaitReconcileEventWithProvenance(
		t, st, "stored-forged-run-finished", canonicalID,
		eventpolicy.RunFinishedEventType, "child-1", eventpolicy.RunFinishedActorRef,
		"github", automation.EventOriginExternal, payload,
	)
	outbox := st.TriggerEvents().(store.AwaitEventNotificationStore)
	reconciler, err := NewAwaitEventReconciler(outbox, testAwaitMatcher(t, st), "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := forged.ReceivedAt.Add(time.Millisecond)
	if count, err := reconciler.DrainOnce(t.Context(), now); err != nil || count != 1 {
		t.Fatalf("forged DrainOnce = %d, %v; want one audited successful no-op", count, err)
	}
	if count, err := reconciler.DrainOnce(t.Context(), now.Add(time.Hour)); err != nil || count != 0 {
		t.Fatalf("forged notification replay = %d, %v; want completed", count, err)
	}
	if got, err := st.DriverRuns().Get(t.Context(), "WS", run.RunID); err != nil || got.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("run after forged event = %+v, %v; want suspended", got, err)
	}
	if pending, err := st.Awaits().ListAwaitsByPattern(t.Context(), "WS", pattern); err != nil || len(pending) != 1 {
		t.Fatalf("await after forged event = %+v, %v; want pending", pending, err)
	}

	genuine := appendAwaitReconcileEventWithProvenance(
		t, st, "stored-genuine-run-finished", canonicalID,
		eventpolicy.RunFinishedEventType, "child-1", eventpolicy.RunFinishedActorRef,
		eventpolicy.SourceKindExecution, automation.EventOriginSystem, payload,
	)
	if count, err := reconciler.DrainOnce(t.Context(), genuine.ReceivedAt.Add(time.Millisecond)); err != nil || count != 1 {
		t.Fatalf("genuine DrainOnce = %d, %v", count, err)
	}
	assertAwaitEventReconciled(t, st, run.RunID, instanceKey, canonicalID, payload)
}

func TestAwaitEventReconcilerRetriesTruncatedPayloadMetadataWithoutResolving(t *testing.T) {
	st, run, instanceKey := setupAwaitEventReconcileRun(t, "run-truncated", "approval.granted:deploy-truncated")
	payload := json.RawMessage(`{"approved":true}`)
	event := appendAwaitReconcileEvent(t, st, "stored-event-truncated", "approval-source-truncated",
		"approval.granted", "deploy-truncated", "alice", payload)
	inner := st.TriggerEvents().(store.AwaitEventNotificationStore)
	outbox := &payloadSizeMismatchAwaitEventOutbox{AwaitEventNotificationStore: inner}
	reconciler, err := NewAwaitEventReconciler(outbox, testAwaitMatcher(t, st), "WS", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := event.ReceivedAt.Add(time.Millisecond)
	if count, err := reconciler.DrainOnce(t.Context(), now); err == nil || count != 1 {
		t.Fatalf("mismatched DrainOnce = %d, %v; want retryable metadata error", count, err)
	}
	stillSuspended, err := st.DriverRuns().Get(t.Context(), "WS", run.RunID)
	if err != nil || stillSuspended.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("run after truncated payload = %+v, %v; want suspended", stillSuspended, err)
	}
	if count, err := reconciler.DrainOnce(t.Context(), now.Add(2*time.Second)); err != nil || count != 1 {
		t.Fatalf("clean retry DrainOnce = %d, %v", count, err)
	}
	assertAwaitEventReconciled(t, st, run.RunID, instanceKey, "approval-source-truncated", payload)
}

type payloadSizeMismatchAwaitEventOutbox struct {
	store.AwaitEventNotificationStore
	mutated bool
}

func (outbox *payloadSizeMismatchAwaitEventOutbox) ClaimAwaitEventNotifications(
	ctx context.Context,
	claim store.AwaitEventNotificationClaim,
) ([]store.AwaitEventNotification, error) {
	values, err := outbox.AwaitEventNotificationStore.ClaimAwaitEventNotifications(ctx, claim)
	if err == nil && !outbox.mutated && len(values) > 0 {
		outbox.mutated = true
		values[0].PayloadSize = len(values[0].Event.Payload) + 1
	}
	return values, err
}

type responseLossAwaitEventDispatcher struct {
	inner *trigger.AwaitMatcher
	lost  bool
}

func (dispatcher *responseLossAwaitEventDispatcher) Dispatch(
	ctx context.Context,
	workspace string,
	event trigger.AwaitDispatchEvent,
) (*trigger.AwaitDispatchResult, error) {
	result, err := dispatcher.inner.Dispatch(ctx, workspace, event)
	if err != nil {
		return result, err
	}
	if !dispatcher.lost {
		dispatcher.lost = true
		return result, errors.New("response lost after atomic await dispatch")
	}
	return result, nil
}

func setupAwaitEventReconcileRun(t *testing.T, runID, pattern string) (*memstore.Store, *domain.DriverRun, string) {
	return setupAwaitEventReconcileRunWithActors(t, runID, pattern, []string{"alice"})
}

func setupAwaitEventReconcileRunWithActors(
	t *testing.T,
	runID, pattern string,
	actorAllow []string,
) (*memstore.Store, *domain.DriverRun, string) {
	t.Helper()
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver", Name: "driver",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", DriverID: "driver", VersionID: "v1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	run, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: runID, DriverID: "driver", DriverVersionID: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = st.DriverRuns().Claim(ctx, "WS", run.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	instanceKey := domain.AwaitInstanceKey(runID, 1)
	if _, err := st.Awaits().RegisterAwaitAndCheck(ctx, "WS", store.AwaitRegistration{
		InstanceKey: instanceKey, RunID: runID, Pattern: pattern,
		ActorAllow: actorAllow, Deadline: time.Now().Add(time.Hour).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, "WS", run.RunID, "node", "lease", run.FencingToken, instanceKey); err != nil {
		t.Fatal(err)
	}
	return st, run, instanceKey
}

func appendAwaitReconcileEvent(
	t *testing.T,
	st *memstore.Store,
	storedID, sourceID, eventType, subject, actor string,
	payload json.RawMessage,
) *automation.Event {
	return appendAwaitReconcileEventWithProvenance(
		t, st, storedID, sourceID, eventType, subject, actor,
		"test", automation.EventOriginExternal, payload,
	)
}

func appendAwaitReconcileEventWithProvenance(
	t *testing.T,
	st *memstore.Store,
	storedID, sourceID, eventType, subject, actor, sourceKind string,
	origin automation.EventOrigin,
	payload json.RawMessage,
) *automation.Event {
	t.Helper()
	now := time.Now().UTC()
	appender := st.TriggerEvents().(store.TriggerEventAppender)
	event, err := appender.AppendTriggerEvent(t.Context(), &automation.Event{
		WorkspaceKey: "WS", EventID: storedID, SourceEventID: sourceID, SourceKind: sourceKind,
		EventType: eventType, SubjectRef: subject, ActorRef: actor,
		Origin: origin, OccurredAt: now, ReceivedAt: now, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func assertAwaitEventReconciled(
	t *testing.T,
	st *memstore.Store,
	runID, instanceKey, eventID string,
	payload json.RawMessage,
) {
	t.Helper()
	run, err := st.DriverRuns().Get(t.Context(), "WS", runID)
	if err != nil || run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != eventID {
		t.Fatalf("run = %+v, %v; want queued by %q", run, err, eventID)
	}
	await, err := st.Awaits().GetSatisfiedAwait(t.Context(), "WS", instanceKey)
	if err != nil || await.SatisfiedByEventID != eventID || string(await.SatisfiedPayload) != string(payload) {
		t.Fatalf("await = %+v, %v; want event %q payload %s", await, err, eventID, payload)
	}
}
