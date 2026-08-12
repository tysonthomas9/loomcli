package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

type runtimeTestCronPort struct {
	occurrences []CronOccurrence
	claimErr    error
	completeErr error
	claimCalls  int
	claim       CronClaim
	claims      []CronClaim
	completions []CronCompletion
}

func (port *runtimeTestCronPort) ClaimDueCron(_ context.Context, claim CronClaim) ([]CronOccurrence, error) {
	port.claimCalls++
	port.claim = claim
	port.claims = append(port.claims, claim)
	return append([]CronOccurrence(nil), port.occurrences...), port.claimErr
}

func TestSweepCronClaimReplayAndReclaimKeepOneAdmission(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("cron-replay", "cron:cron-replay")
	binding.SourceKind, binding.Schedule = SourceKindCron, "@daily"
	h.persistence.seedBinding(binding)
	occurrence := CronOccurrence{
		WorkspaceKey: "ws", BindingID: binding.BindingID, RouteKey: binding.RouteKey,
		OccurrenceID: "cron:cron-replay:1784203260", OccurredAt: h.now,
	}
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{occurrence}}
	WithRuntimePorts(cronPort, nil)(h.service)
	current := h.now
	h.service.now = func() time.Time { return current }

	for pass := 0; pass < 2; pass++ {
		result, err := h.service.SweepCron(t.Context(), h.issueSystem(ActionSweepCron), SweepCronCommand{WorkspaceKey: "ws"})
		if err != nil || result.Admitted != 1 {
			t.Fatalf("exact replay pass %d = %+v, %v", pass, result, err)
		}
	}
	if len(cronPort.claims) != 2 || cronPort.claims[0].IdempotencyKey != cronPort.claims[1].IdempotencyKey {
		t.Fatalf("exact claim replay keys = %+v", cronPort.claims)
	}
	if len(h.persistence.events) != 1 || len(h.execution.calls) != 1 {
		t.Fatalf("exact replay duplicated work: events=%d dispatches=%d", len(h.persistence.events), len(h.execution.calls))
	}

	current = current.Add(CronOccurrenceClaimLease + time.Second)
	result, err := h.service.SweepCron(t.Context(), h.issueSystem(ActionSweepCron), SweepCronCommand{WorkspaceKey: "ws"})
	if err != nil || result.Admitted != 1 {
		t.Fatalf("reclaim = %+v, %v", result, err)
	}
	if cronPort.claims[2].IdempotencyKey == cronPort.claims[1].IdempotencyKey ||
		!cronPort.claims[2].Before.After(cronPort.claims[1].ClaimUntil) {
		t.Fatalf("reclaim claim = previous:%+v next:%+v", cronPort.claims[1], cronPort.claims[2])
	}
	if len(h.persistence.events) != 1 || len(h.execution.calls) != 1 {
		t.Fatalf("reclaim duplicated admitted occurrence: events=%d dispatches=%d", len(h.persistence.events), len(h.execution.calls))
	}
}

func (port *runtimeTestCronPort) CompleteCron(_ context.Context, completion CronCompletion) error {
	port.completions = append(port.completions, completion)
	return port.completeErr
}

type runtimeAwaitProbe struct {
	calls   []AwaitEventNotification
	journal []AwaitEventNotification
	awaits  map[string]*runtimeAwaitProbeRow
}

type runtimeAwaitProbeRow struct {
	pattern            string
	actorAllow         []string
	satisfied          bool
	satisfiedByEventID string
	satisfiedPayload   json.RawMessage
}

func (notifier *runtimeAwaitProbe) NotifyAwaitEvent(_ context.Context, event AwaitEventNotification) error {
	event.Payload = cloneRawMessage(event.Payload)
	notifier.calls = append(notifier.calls, event)
	notifier.journal = append(notifier.journal, event)
	for _, row := range notifier.awaits {
		notifier.resolve(row, event)
	}
	return nil
}

func (notifier *runtimeAwaitProbe) register(instanceKey, pattern string, actorAllow []string) *runtimeAwaitProbeRow {
	if notifier.awaits == nil {
		notifier.awaits = make(map[string]*runtimeAwaitProbeRow)
	}
	row := &runtimeAwaitProbeRow{pattern: pattern, actorAllow: append([]string(nil), actorAllow...)}
	notifier.awaits[instanceKey] = row
	for _, event := range notifier.journal {
		if notifier.resolve(row, event) {
			break
		}
	}
	return row
}

func (*runtimeAwaitProbe) resolve(row *runtimeAwaitProbeRow, event AwaitEventNotification) bool {
	if row == nil || row.satisfied || row.pattern != event.EventType+":"+event.SubjectRef ||
		len(row.actorAllow) > 0 && !slices.Contains(row.actorAllow, event.ActorRef) {
		return false
	}
	row.satisfied = true
	row.satisfiedByEventID = event.EventID
	row.satisfiedPayload = cloneRawMessage(event.Payload)
	return true
}

type runtimeTestRetryPort struct {
	candidates []RetryCandidate
	claimErr   error
	claimCalls int
	workspace  string
	before     time.Time
	claimUntil time.Time
	limit      int
}

func (port *runtimeTestRetryPort) ClaimDueDeliveries(_ context.Context, workspace string, before, claimUntil time.Time, limit int) ([]RetryCandidate, error) {
	port.claimCalls++
	port.workspace, port.before, port.claimUntil, port.limit = workspace, before, claimUntil, limit
	return append([]RetryCandidate(nil), port.candidates...), port.claimErr
}

func TestSweepCronAdmitsClaimedOccurrenceAndCompletesIt(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("cron-nightly", "cron.custom.nightly")
	binding.SourceKind = SourceKindCron
	binding.Schedule = "@daily"
	h.persistence.seedBinding(binding)
	firedAt := time.Date(2026, 7, 16, 12, 1, 0, 0, time.UTC)
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{{
		WorkspaceKey: "ws", BindingID: binding.BindingID, RouteKey: binding.RouteKey,
		OccurrenceID: "cron:cron-nightly:1784203260", OccurredAt: firedAt,
	}}}
	WithRuntimePorts(cronPort, nil)(h.service)

	result, err := h.service.SweepCron(t.Context(), h.issueSystem(ActionSweepCron), SweepCronCommand{
		WorkspaceKey: "ws", Limit: 7,
	})
	if err != nil {
		t.Fatalf("SweepCron: %v", err)
	}
	if result == nil || *result != (SweepCronResult{Claimed: 1, Admitted: 1}) {
		t.Fatalf("SweepCron result = %+v", result)
	}
	if cronPort.claimCalls != 1 || cronPort.claim.WorkspaceKey != "ws" || !cronPort.claim.Before.Equal(h.now) ||
		!cronPort.claim.ClaimUntil.Equal(h.now.Add(CronOccurrenceClaimLease)) || cronPort.claim.Limit != 7 ||
		cronPort.claim.IdempotencyKey != cronClaimIdempotencyKey("ws", h.now, h.now.Add(CronOccurrenceClaimLease), 7) {
		t.Fatalf("claim = calls:%d value:%+v", cronPort.claimCalls, cronPort.claim)
	}
	if want := []CronCompletion{{
		WorkspaceKey: "ws", BindingID: binding.BindingID,
		OccurrenceID: "cron:cron-nightly:1784203260", Status: CronCompletionAdmitted,
	}}; !reflect.DeepEqual(cronPort.completions, want) {
		t.Fatalf("completions = %+v, want %+v", cronPort.completions, want)
	}

	reservation := h.persistence.lastReservation
	if reservation.Event == nil {
		t.Fatal("cron admission did not reserve an event")
	}
	event := reservation.Event
	if event.WorkspaceKey != "ws" || event.SourceKind != SourceKindCron ||
		event.RouteKey != binding.RouteKey || event.SourceEventID != "cron:cron-nightly:1784203260" ||
		event.EventType != CronEventType || event.SubjectRef != binding.BindingID || event.Origin != EventOriginSystem ||
		event.SignatureStatus != SignatureStatusInternal || event.IdempotencyKey != "cron:cron-nightly:1784203260" ||
		!event.OccurredAt.Equal(firedAt) {
		t.Fatalf("reserved cron event = %+v", event)
	}
	var payload struct {
		Tick string `json:"tick"`
	}
	if err := json.Unmarshal(reservation.Payload, &payload); err != nil || payload.Tick != firedAt.Format(time.RFC3339) {
		t.Fatalf("cron payload = %s (%+v, %v)", reservation.Payload, payload, err)
	}
	if len(h.execution.calls) != 1 || h.execution.calls[0].TriggerBindingID != binding.BindingID {
		t.Fatalf("execution calls = %+v", h.execution.calls)
	}
	if len(h.awaits.calls) != 1 {
		t.Fatalf("await notifications = %+v, want one", h.awaits.calls)
	}
	if notification := h.awaits.calls[0]; notification.WorkspaceKey != "ws" ||
		notification.EventID != "cron:cron-nightly:1784203260" || notification.EventType != CronEventType ||
		notification.SubjectRef != binding.BindingID || notification.ActorRef != "subject-system" ||
		!bytes.Equal(notification.Payload, reservation.Payload) {
		t.Fatalf("await notification = %+v, payload %s", notification, notification.Payload)
	}
}

func TestSweepCronPendingAwaitResolvesFromCanonicalNotification(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("cron-await-pending", "cron:cron-await-pending")
	binding.SourceKind, binding.Schedule = SourceKindCron, "@daily"
	h.persistence.seedBinding(binding)
	occurrence := CronOccurrence{
		WorkspaceKey: "ws", BindingID: binding.BindingID, RouteKey: binding.RouteKey,
		OccurrenceID: "cron:cron-await-pending:1784203260", OccurredAt: h.now,
	}
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{occurrence}}
	WithRuntimePorts(cronPort, nil)(h.service)

	auth := h.issueSystem(ActionSweepCron)
	notifier := &runtimeAwaitProbe{}
	pending := notifier.register("run-cron-pending#await-1", CronEventType+":"+binding.BindingID, []string{auth.Subject()})
	WithAwaitEventNotifier(notifier)(h.service)

	result, err := h.service.SweepCron(t.Context(), auth, SweepCronCommand{WorkspaceKey: "ws"})
	if err != nil || result == nil || result.Admitted != 1 {
		t.Fatalf("SweepCron = %+v, %v", result, err)
	}
	wantPayload := json.RawMessage(`{"tick":"2026-07-16T12:00:00Z"}`)
	if !pending.satisfied || pending.satisfiedByEventID != occurrence.OccurrenceID ||
		!bytes.Equal(pending.satisfiedPayload, wantPayload) {
		t.Fatalf("satisfied await = %+v, want source event %q and payload %s", pending, occurrence.OccurrenceID, wantPayload)
	}
	if len(notifier.calls) != 1 || notifier.calls[0].ActorRef != auth.Subject() ||
		!bytes.Equal(notifier.calls[0].Payload, wantPayload) {
		t.Fatalf("notifications = %+v", notifier.calls)
	}
}

func TestSweepCronEventBeforeRegistrationCatchesUpWithSameIdentityAndPayload(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("cron-await-catchup", "cron:cron-await-catchup")
	binding.SourceKind, binding.Schedule = SourceKindCron, "@daily"
	h.persistence.seedBinding(binding)
	occurrence := CronOccurrence{
		WorkspaceKey: "ws", BindingID: binding.BindingID, RouteKey: binding.RouteKey,
		OccurrenceID: "cron:cron-await-catchup:1784203260", OccurredAt: h.now,
	}
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{occurrence}}
	WithRuntimePorts(cronPort, nil)(h.service)

	auth := h.issueSystem(ActionSweepCron)
	notifier := &runtimeAwaitProbe{}
	WithAwaitEventNotifier(notifier)(h.service)

	result, err := h.service.SweepCron(t.Context(), auth, SweepCronCommand{WorkspaceKey: "ws"})
	if err != nil || result == nil || result.Admitted != 1 {
		t.Fatalf("SweepCron = %+v, %v", result, err)
	}
	registered := notifier.register("run-cron-catchup#await-1", CronEventType+":"+binding.BindingID, []string{auth.Subject()})
	wantPayload := json.RawMessage(`{"tick":"2026-07-16T12:00:00Z"}`)
	if !registered.satisfied || registered.satisfiedByEventID != occurrence.OccurrenceID ||
		!bytes.Equal(registered.satisfiedPayload, wantPayload) {
		t.Fatalf("registration = %+v, want catch-up event %q with payload %s", registered, occurrence.OccurrenceID, wantPayload)
	}
}

func TestSweepCronAwaitNotificationFailureRetriesAfterAdmissionReplay(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("cron-await-retry", "cron:cron-await-retry")
	binding.SourceKind, binding.Schedule = SourceKindCron, "@daily"
	h.persistence.seedBinding(binding)
	occurrence := CronOccurrence{
		WorkspaceKey: "ws", BindingID: binding.BindingID, RouteKey: binding.RouteKey,
		OccurrenceID: "cron:cron-await-retry:1784203260", OccurredAt: h.now,
	}
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{occurrence}}
	WithRuntimePorts(cronPort, nil)(h.service)
	injected := errors.New("injected await notification failure")
	h.awaits.failCount, h.awaits.err = 1, injected
	auth := h.issueSystem(ActionSweepCron)

	first, err := h.service.SweepCron(t.Context(), auth, SweepCronCommand{WorkspaceKey: "ws"})
	if first == nil || first.Failed != 1 || first.Admitted != 0 || !errors.Is(err, injected) {
		t.Fatalf("first SweepCron = %+v, %v", first, err)
	}
	second, err := h.service.SweepCron(t.Context(), auth, SweepCronCommand{WorkspaceKey: "ws"})
	if err != nil || second == nil || second.Admitted != 1 {
		t.Fatalf("replay SweepCron = %+v, %v", second, err)
	}
	if len(h.awaits.calls) != 2 || !reflect.DeepEqual(h.awaits.calls[0], h.awaits.calls[1]) {
		t.Fatalf("await replay notifications = %+v, want two identical calls", h.awaits.calls)
	}
	if len(h.persistence.events) != 1 || len(h.execution.calls) != 1 {
		t.Fatalf("admission replay duplicated work: events=%d dispatches=%d", len(h.persistence.events), len(h.execution.calls))
	}
	if len(cronPort.completions) != 2 || cronPort.completions[0].Status != CronCompletionFailed ||
		cronPort.completions[1].Status != CronCompletionAdmitted {
		t.Fatalf("cron completions = %+v, want failed then admitted", cronPort.completions)
	}
}

func TestSweepCronNotifiesDurableEventWhenDeliveryDispatchFails(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("cron-await-dispatch-failure", "cron:cron-await-dispatch-failure")
	binding.SourceKind, binding.Schedule = SourceKindCron, "@daily"
	h.persistence.seedBinding(binding)
	occurrence := CronOccurrence{
		WorkspaceKey: "ws", BindingID: binding.BindingID, RouteKey: binding.RouteKey,
		OccurrenceID: "cron:cron-await-dispatch-failure:1784203260", OccurredAt: h.now,
	}
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{occurrence}}
	WithRuntimePorts(cronPort, nil)(h.service)
	injected := errors.New("injected execution dispatch failure")
	h.execution.outcomes[binding.BindingID] = []fakeDispatchOutcome{{err: injected}}

	result, err := h.service.SweepCron(
		t.Context(), h.issueSystem(ActionSweepCron), SweepCronCommand{WorkspaceKey: "ws"},
	)
	if result == nil || result.Failed != 1 || result.Admitted != 0 || !errors.Is(err, injected) {
		t.Fatalf("SweepCron = %+v, %v", result, err)
	}
	if len(h.persistence.events) != 1 || len(h.awaits.calls) != 1 ||
		h.awaits.calls[0].EventID != occurrence.OccurrenceID {
		t.Fatalf("durable admission notification = events:%d calls:%+v", len(h.persistence.events), h.awaits.calls)
	}
	if len(cronPort.completions) != 1 || cronPort.completions[0].Status != CronCompletionFailed {
		t.Fatalf("cron completion = %+v, want retryable failure", cronPort.completions)
	}
}

func TestSweepCronMissingAwaitNotifierFailsClosedAfterAdmission(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("cron-await-unwired", "cron:cron-await-unwired")
	binding.SourceKind, binding.Schedule = SourceKindCron, "@daily"
	h.persistence.seedBinding(binding)
	occurrence := CronOccurrence{
		WorkspaceKey: "ws", BindingID: binding.BindingID, RouteKey: binding.RouteKey,
		OccurrenceID: "cron:cron-await-unwired:1784203260", OccurredAt: h.now,
	}
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{occurrence}}
	WithRuntimePorts(cronPort, nil)(h.service)
	WithAwaitEventNotifier(nil)(h.service)

	result, err := h.service.SweepCron(
		t.Context(), h.issueSystem(ActionSweepCron), SweepCronCommand{WorkspaceKey: "ws"},
	)
	if result == nil || result.Failed != 1 || result.Admitted != 0 || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SweepCron = %+v, %v, want fail-closed unavailable notifier", result, err)
	}
	if len(h.persistence.events) != 1 || len(cronPort.completions) != 1 ||
		cronPort.completions[0].Status != CronCompletionFailed {
		t.Fatalf("durable retry state = events:%d completions:%+v", len(h.persistence.events), cronPort.completions)
	}
}

func TestSweepCronFallsBackToDerivedRouteAndRecordsAdmissionFailure(t *testing.T) {
	h := newTestHarness(t)
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{{
		WorkspaceKey: "ws", BindingID: "missing-binding", OccurrenceID: "cron:missing-binding:1", OccurredAt: h.now,
	}}}
	WithRuntimePorts(cronPort, nil)(h.service)

	result, err := h.service.SweepCron(t.Context(), h.issueSystem(ActionSweepCron), SweepCronCommand{WorkspaceKey: "ws"})
	if result == nil || result.Claimed != 1 || result.Failed != 1 || result.Admitted != 0 {
		t.Fatalf("SweepCron result = %+v", result)
	}
	if !errors.Is(err, ErrNoMatchingBinding) {
		t.Fatalf("SweepCron error = %v, want %v", err, ErrNoMatchingBinding)
	}
	if cronPort.claim.Limit != DefaultRuntimeSweepLimit {
		t.Fatalf("default limit = %d, want %d", cronPort.claim.Limit, DefaultRuntimeSweepLimit)
	}
	want := CronCompletion{
		WorkspaceKey: "ws", BindingID: "missing-binding", OccurrenceID: "cron:missing-binding:1",
		Status: CronCompletionFailed, ErrorClass: cronAdmissionErrorClass,
	}
	if len(cronPort.completions) != 1 || cronPort.completions[0] != want {
		t.Fatalf("completion = %+v, want %+v", cronPort.completions, want)
	}
}

func TestSweepCronAuthorityAndClaimedScopeFailClosed(t *testing.T) {
	h := newTestHarness(t)
	cronPort := &runtimeTestCronPort{occurrences: []CronOccurrence{{
		WorkspaceKey: "other", BindingID: "cron-a", OccurrenceID: "cron:cron-a:1", OccurredAt: h.now,
	}}}
	WithRuntimePorts(cronPort, nil)(h.service)

	if _, err := h.service.SweepCron(t.Context(), h.issueSystem(ActionRetryDeliveries), SweepCronCommand{WorkspaceKey: "ws"}); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action SweepCron error = %v", err)
	}
	if cronPort.claimCalls != 0 {
		t.Fatalf("wrong-action authority reached claim port: %d calls", cronPort.claimCalls)
	}

	result, err := h.service.SweepCron(t.Context(), h.issueSystem(ActionSweepCron), SweepCronCommand{WorkspaceKey: "ws"})
	if result == nil || result.Claimed != 1 || result.Failed != 1 || !errors.Is(err, ErrWrongWorkspace) {
		t.Fatalf("wrong-workspace occurrence = (%+v, %v)", result, err)
	}
	if len(cronPort.completions) != 0 || h.persistence.reserveCalls != 0 {
		t.Fatalf("invalid occurrence mutated state: completions=%v reservations=%d", cronPort.completions, h.persistence.reserveCalls)
	}
}

func runtimeTestCandidate(status DeliveryStatus, attempt, maxAttempts int, backoff time.Duration) RetryCandidate {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	next := now.Add(DeliveryRetryClaimLease)
	event := &Event{
		WorkspaceKey: "ws", EventID: "event-1", TriggerBindingID: "binding-a",
		SourceKind: "github", SourceEventID: "delivery-source-1", EventType: "pull_request",
		SubjectRef: "acme/widgets#42", ActorRef: "octocat", RouteKey: "github.pull_request.opened",
		Origin: EventOriginExternal, OccurredAt: now.Add(-2 * time.Minute), ReceivedAt: now.Add(-time.Minute),
		IdempotencyKey: "github:delivery-source-1", RawPayloadRef: "artifact://payload-1",
		RawPayloadDigest: "sha256:payload-1", SignatureStatus: SignatureStatusVerified,
		Payload: json.RawMessage(`{"action":"opened"}`), SubjectAttrs: map[string]string{"repo": "acme/widgets"},
		EpicID: "epic-1",
	}
	delivery := &Delivery{
		WorkspaceKey: "ws", DeliveryID: "delivery-1", TriggerEventID: event.EventID,
		TriggerBindingID: "binding-a", Status: status, SubjectKey: "acme/widgets#42",
		DriverID: "driver-a", DriverVersionID: "version-active", TargetEntrypoint: "run",
		TargetAgentServiceID: "agent-service-a", SourceKind: "github", ConcurrencyPolicy: ConcurrencyQueue,
		RetryMaxAttempts: maxAttempts, RetryBackoffSeconds: int(backoff / time.Second),
		Attempt: attempt, NextRetryAt: &next,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	target := &DispatchTarget{
		DriverID: "driver-a", DriverVersionID: "version-active", Entrypoint: "run",
		TargetAgentServiceID: "agent-service-a", SourceKind: "github", BindingID: "binding-a", ConcurrencyPolicy: ConcurrencyQueue,
		RetryMaxAttempts: maxAttempts, RetryBackoff: backoff,
	}
	return RetryCandidate{
		Delivery: delivery, Target: target, Event: event,
		Payload: cloneRawMessage(event.Payload), SubjectAttrs: cloneStringMap(event.SubjectAttrs), EpicID: event.EpicID,
	}
}

func seedRuntimeTestDelivery(h *testHarness, candidate RetryCandidate) {
	h.t.Helper()
	h.persistence.mu.Lock()
	defer h.persistence.mu.Unlock()
	h.persistence.deliveries[deliveryMapKey(candidate.Delivery.WorkspaceKey, candidate.Delivery.DeliveryID)] = cloneDelivery(candidate.Delivery)
}

func TestRetryDeliveriesUsesImmutableSnapshotAndCommittedDispatch(t *testing.T) {
	h := newTestHarness(t)
	candidate := runtimeTestCandidate(DeliveryFailed, 2, 5, 30*time.Second)
	seedRuntimeTestDelivery(h, candidate)
	retryPort := &runtimeTestRetryPort{candidates: []RetryCandidate{candidate}}
	WithRuntimePorts(nil, retryPort)(h.service)

	result, err := h.service.RetryDeliveries(t.Context(), h.issueSystem(ActionRetryDeliveries), RetryDeliveriesCommand{
		WorkspaceKey: "ws", Limit: 9,
	})
	if err != nil {
		t.Fatalf("RetryDeliveries: %v", err)
	}
	if result == nil || *result != (RetryDeliveriesResult{Claimed: 1, Dispatched: 1}) {
		t.Fatalf("RetryDeliveries result = %+v", result)
	}
	if retryPort.claimCalls != 1 || retryPort.workspace != "ws" || !retryPort.before.Equal(h.now) ||
		!retryPort.claimUntil.Equal(h.now.Add(DeliveryRetryClaimLease)) || retryPort.limit != 9 {
		t.Fatalf("claim = calls:%d workspace:%q before:%s until:%s limit:%d", retryPort.claimCalls, retryPort.workspace, retryPort.before, retryPort.claimUntil, retryPort.limit)
	}
	wantDispatch := ExecutionDispatchRequest{
		WorkspaceKey: "ws", IdempotencyKey: "github:delivery-source-1#binding-a",
		DeliveryID: "delivery-1", ExpectedDeliveryStatus: DeliveryFailed, ExpectedDeliveryAttempt: 2,
		DriverID: "driver-a", DriverVersionID: "version-active", Entrypoint: "run",
		TargetAgentServiceID: "agent-service-a", SourceKind: "github", SourceRef: "event-1", TriggerBindingID: "binding-a",
		SubjectRef: "acme/widgets#42", SubjectKey: "acme/widgets#42", ConcurrencyPolicy: ConcurrencyQueue,
		EpicID: "epic-1", ActorRef: "octocat", RawPayloadRef: "artifact://payload-1",
		Payload: json.RawMessage(`{"action":"opened"}`), SubjectAttrs: map[string]string{"repo": "acme/widgets"},
	}
	if len(h.execution.calls) != 1 || !reflect.DeepEqual(h.execution.calls[0], wantDispatch) {
		t.Fatalf("execution call = %#v, want %#v", h.execution.calls, wantDispatch)
	}
	if len(h.persistence.transitionCalls) != 0 {
		t.Fatalf("committed dispatch was transitioned again: %+v", h.persistence.transitionCalls)
	}
	stored := h.persistence.deliveries[deliveryMapKey("ws", "delivery-1")]
	if stored.Status != DeliveryDispatched || stored.Attempt != 2 || stored.DriverRunID != "run-binding-a" {
		t.Fatalf("committed delivery = %+v", stored)
	}
}

func TestRetryDeliveriesBacksOffHoldsAndExhausts(t *testing.T) {
	tests := []struct {
		name           string
		candidate      RetryCandidate
		outcome        fakeDispatchOutcome
		wantResult     RetryDeliveriesResult
		wantStatus     DeliveryStatus
		wantErrorClass string
		wantNext       time.Time
	}{
		{
			name:       "failed dispatch doubles durable backoff",
			candidate:  runtimeTestCandidate(DeliveryFailed, 2, 5, 30*time.Second),
			outcome:    fakeDispatchOutcome{err: errors.New("execution unavailable")},
			wantResult: RetryDeliveriesResult{Claimed: 1, Failed: 1}, wantStatus: DeliveryFailed,
			wantErrorClass: DeliveryErrorDispatchFailed, wantNext: time.Date(2026, 7, 16, 12, 1, 0, 0, time.UTC),
		},
		{
			name:      "busy queue remains held",
			candidate: runtimeTestCandidate(DeliveryHeld, 2, 5, 30*time.Second),
			outcome: fakeDispatchOutcome{
				result: &ExecutionDispatchResult{Busy: true, BusyRunID: "run-busy"}, committedStatus: DeliveryHeld,
			},
			wantResult: RetryDeliveriesResult{Claimed: 1, Held: 1}, wantStatus: DeliveryHeld,
		},
		{
			name:       "attempt budget is terminal",
			candidate:  runtimeTestCandidate(DeliveryFailed, 3, 3, 30*time.Second),
			outcome:    fakeDispatchOutcome{err: errors.New("still unavailable")},
			wantResult: RetryDeliveriesResult{Claimed: 1, Exhausted: 1}, wantStatus: DeliveryFailed,
			wantErrorClass: DeliveryErrorRetriesExhausted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			seedRuntimeTestDelivery(h, test.candidate)
			h.execution.outcomes[test.candidate.Delivery.TriggerBindingID] = []fakeDispatchOutcome{test.outcome}
			retryPort := &runtimeTestRetryPort{candidates: []RetryCandidate{test.candidate}}
			WithRuntimePorts(nil, retryPort)(h.service)

			result, err := h.service.RetryDeliveries(t.Context(), h.issueSystem(ActionRetryDeliveries), RetryDeliveriesCommand{WorkspaceKey: "ws"})
			if err != nil {
				t.Fatalf("RetryDeliveries: %v", err)
			}
			if result == nil || *result != test.wantResult {
				t.Fatalf("result = %+v, want %+v", result, test.wantResult)
			}
			if test.outcome.committedStatus != "" {
				if len(h.persistence.transitionCalls) != 0 {
					t.Fatalf("committed dispatch was transitioned again: %+v", h.persistence.transitionCalls)
				}
				stored := h.persistence.deliveries[deliveryMapKey("ws", test.candidate.Delivery.DeliveryID)]
				if stored.Status != test.wantStatus || stored.NextRetryAt == nil {
					t.Fatalf("committed delivery = %+v", stored)
				}
			} else {
				transition := h.persistence.transitionCalls[0]
				if transition.Status != test.wantStatus || transition.ErrorClass != test.wantErrorClass || transition.Attempt != test.candidate.Delivery.Attempt {
					t.Fatalf("transition = %+v", transition)
				}
				if test.wantNext.IsZero() {
					if transition.NextRetryAt != nil {
						t.Fatalf("next retry = %s, want nil", transition.NextRetryAt)
					}
				} else if transition.NextRetryAt == nil || !transition.NextRetryAt.Equal(test.wantNext) {
					t.Fatalf("next retry = %v, want %s", transition.NextRetryAt, test.wantNext)
				}
			}
		})
	}
}

func TestRetryDeliveriesRejectsWrongAuthorityAndStaleClaim(t *testing.T) {
	h := newTestHarness(t)
	candidate := runtimeTestCandidate(DeliveryFailed, 2, 5, 30*time.Second)
	seedRuntimeTestDelivery(h, candidate)
	retryPort := &runtimeTestRetryPort{candidates: []RetryCandidate{candidate}}
	WithRuntimePorts(nil, retryPort)(h.service)

	if _, err := h.service.RetryDeliveries(t.Context(), h.issueSystem(ActionSweepCron), RetryDeliveriesCommand{WorkspaceKey: "ws"}); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action RetryDeliveries error = %v", err)
	}
	if retryPort.claimCalls != 0 {
		t.Fatalf("wrong-action authority reached claim port: %d calls", retryPort.claimCalls)
	}

	h.persistence.deliveries[deliveryMapKey("ws", candidate.Delivery.DeliveryID)].Attempt = 3
	result, err := h.service.RetryDeliveries(t.Context(), h.issueSystem(ActionRetryDeliveries), RetryDeliveriesCommand{WorkspaceKey: "ws"})
	if result == nil || result.Claimed != 1 || result.Failed != 1 || !errors.Is(err, ErrConflict) {
		t.Fatalf("stale retry claim = (%+v, %v)", result, err)
	}
	stored := h.persistence.deliveries[deliveryMapKey("ws", candidate.Delivery.DeliveryID)]
	if stored.Attempt != 3 || stored.Status != DeliveryFailed {
		t.Fatalf("stale transition overwrote delivery = %+v", stored)
	}
}

type runtimeTestCommands struct {
	cronCalls  []SweepCronCommand
	retryCalls []RetryDeliveriesCommand
	cronErr    error
	retryErr   error
}

func (commands *runtimeTestCommands) SweepCron(_ context.Context, _ authority.SystemAuthority, command SweepCronCommand) (*SweepCronResult, error) {
	commands.cronCalls = append(commands.cronCalls, command)
	return &SweepCronResult{}, commands.cronErr
}

func (commands *runtimeTestCommands) RetryDeliveries(_ context.Context, _ authority.SystemAuthority, command RetryDeliveriesCommand) (*RetryDeliveriesResult, error) {
	commands.retryCalls = append(commands.retryCalls, command)
	return &RetryDeliveriesResult{}, commands.retryErr
}

type runtimeTestAuthorityProvider struct {
	calls []runtimeTestAuthorityCall
	auth  authority.SystemAuthority
	err   error
}

type runtimeTestAuthorityCall struct {
	componentID platformruntime.ComponentID
	workspace   string
	action      authority.Action
}

type runtimeTestWorkspaceLister struct {
	keys  []string
	calls int
}

func (lister *runtimeTestWorkspaceLister) ListWorkspaceKeys(context.Context) ([]string, error) {
	lister.calls++
	return append([]string(nil), lister.keys...), nil
}

func (provider *runtimeTestAuthorityProvider) AuthorityForAutomationRuntime(_ context.Context, componentID platformruntime.ComponentID, workspace string, action authority.Action) (authority.SystemAuthority, error) {
	provider.calls = append(provider.calls, runtimeTestAuthorityCall{componentID: componentID, workspace: workspace, action: action})
	return provider.auth, provider.err
}

func TestRuntimeRegistrationsRetainIDsPoliciesAndExactActions(t *testing.T) {
	commands := &runtimeTestCommands{}
	provider := &runtimeTestAuthorityProvider{}
	registrations, err := RuntimeRegistrations(commands, provider, RuntimeConfig{WorkspaceKey: "ws"})
	if err != nil {
		t.Fatalf("RuntimeRegistrations: %v", err)
	}
	if len(registrations) != 2 {
		t.Fatalf("registration count = %d, want 2", len(registrations))
	}
	want := []struct {
		id      platformruntime.ComponentID
		cadence time.Duration
	}{
		{id: CronSchedulerComponentID, cadence: DefaultCronSchedulerCadence},
		{id: DeliverySweeperComponentID, cadence: DefaultDeliverySweepCadence},
	}
	for index, registration := range registrations {
		if registration.Component.ID() != want[index].id {
			t.Errorf("registration %d ID = %q, want %q", index, registration.Component.ID(), want[index].id)
		}
		wantPolicy := platformruntime.Policy{Cadence: want[index].cadence, Immediate: true}
		if registration.Policy != wantPolicy {
			t.Errorf("registration %d policy = %+v, want %+v", index, registration.Policy, wantPolicy)
		}
		if err := registration.Component.RunOnce(t.Context(), time.Now()); err != nil {
			t.Fatalf("component %q RunOnce: %v", registration.Component.ID(), err)
		}
	}
	wantCalls := []runtimeTestAuthorityCall{
		{componentID: CronSchedulerComponentID, workspace: "ws", action: ActionSweepCron},
		{componentID: DeliverySweeperComponentID, workspace: "ws", action: ActionRetryDeliveries},
	}
	if !reflect.DeepEqual(provider.calls, wantCalls) {
		t.Fatalf("authority calls = %+v, want %+v", provider.calls, wantCalls)
	}
	if wantCron := []SweepCronCommand{{WorkspaceKey: "ws", Limit: DefaultRuntimeSweepLimit}}; !reflect.DeepEqual(commands.cronCalls, wantCron) {
		t.Fatalf("cron calls = %+v, want %+v", commands.cronCalls, wantCron)
	}
	if wantRetry := []RetryDeliveriesCommand{{WorkspaceKey: "ws", Limit: DefaultRuntimeSweepLimit}}; !reflect.DeepEqual(commands.retryCalls, wantRetry) {
		t.Fatalf("retry calls = %+v, want %+v", commands.retryCalls, wantRetry)
	}
}

func TestUnscopedRuntimeDiscoversWorkspacesOnEveryPass(t *testing.T) {
	commands := &runtimeTestCommands{}
	provider := &runtimeTestAuthorityProvider{}
	lister := &runtimeTestWorkspaceLister{keys: []string{"ws-b", "ws-a"}}
	registrations, err := RuntimeRegistrations(commands, provider, RuntimeConfig{WorkspaceLister: lister})
	if err != nil {
		t.Fatalf("RuntimeRegistrations: %v", err)
	}
	cron := registrations[0].Component
	if err := cron.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	lister.keys = append(lister.keys, "ws-c")
	if err := cron.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	want := []SweepCronCommand{
		{WorkspaceKey: "ws-a", Limit: DefaultRuntimeSweepLimit},
		{WorkspaceKey: "ws-b", Limit: DefaultRuntimeSweepLimit},
		{WorkspaceKey: "ws-a", Limit: DefaultRuntimeSweepLimit},
		{WorkspaceKey: "ws-b", Limit: DefaultRuntimeSweepLimit},
		{WorkspaceKey: "ws-c", Limit: DefaultRuntimeSweepLimit},
	}
	if lister.calls != 2 || !reflect.DeepEqual(commands.cronCalls, want) {
		t.Fatalf("dynamic workspace calls = lister:%d cron:%+v, want %+v", lister.calls, commands.cronCalls, want)
	}
	for index, call := range provider.calls {
		if call.componentID != CronSchedulerComponentID || call.action != ActionSweepCron || call.workspace != want[index].WorkspaceKey {
			t.Errorf("authority call %d = %+v, want workspace %q cron scope", index, call, want[index].WorkspaceKey)
		}
	}
}

func TestRuntimeRegistrationAndAuthorityFailuresAreInert(t *testing.T) {
	provider := &runtimeTestAuthorityProvider{}
	commands := &runtimeTestCommands{}
	if _, err := RuntimeRegistrations(nil, provider, RuntimeConfig{WorkspaceKey: "ws"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil commands error = %v", err)
	}
	if _, err := RuntimeRegistrations(commands, nil, RuntimeConfig{WorkspaceKey: "ws"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil provider error = %v", err)
	}
	if _, err := RuntimeRegistrations(commands, provider, RuntimeConfig{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty workspace error = %v", err)
	}

	denied := authority.ErrAdmissionDenied
	provider.err = denied
	registrations, err := RuntimeRegistrations(commands, provider, RuntimeConfig{WorkspaceKey: "ws", CronLimit: MaxRuntimeSweepLimit + 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := registrations[0].Component.RunOnce(t.Context(), time.Now()); !errors.Is(err, denied) {
		t.Fatalf("authority failure = %v, want %v", err, denied)
	}
	if len(commands.cronCalls) != 0 {
		t.Fatalf("authority denial reached commands: %+v", commands.cronCalls)
	}
}
