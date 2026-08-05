package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

type transportFake struct {
	binding              *automation.Binding
	bindings             []*automation.Binding
	match                *TransportBindingMatchSnapshot
	event                *automation.Event
	events               []*automation.Event
	delivery             *automation.Delivery
	deliveries           []*automation.Delivery
	reservation          *TransportReservationResult
	claimed              []TransportClaimedDelivery
	cronOccurrences      []TransportCronOccurrence
	cronClaim            TransportCronClaim
	cronCompletion       TransportCronCompletion
	err                  error
	reserveRequest       TransportEventReservation
	transition           TransportDeliveryTransition
	claimKeys            []string
	createCalls          int
	lastEventFilter      automation.EventFilter
	managedReplacement   TransportManagedBindingReplacement
	managedDelete        TransportManagedBindingSnapshot
	unmanagedReplacement TransportUnmanagedBindingReplacement
	unmanagedDelete      TransportUnmanagedBindingSnapshot
}

func (f *transportFake) CreateBinding(context.Context, *automation.Binding) (*automation.Binding, error) {
	f.createCalls++
	return f.binding, f.err
}
func (f *transportFake) GetBinding(context.Context, string, string) (*automation.Binding, error) {
	return f.binding, f.err
}
func (f *transportFake) ListBindings(context.Context, string, automation.BindingFilter) ([]*automation.Binding, error) {
	return f.bindings, f.err
}
func (f *transportFake) UpdateBinding(context.Context, *automation.Binding) (*automation.Binding, error) {
	return f.binding, f.err
}
func (f *transportFake) DeleteBinding(context.Context, string, string) error { return f.err }
func (f *transportFake) ReplaceUnmanagedBinding(_ context.Context, replacement TransportUnmanagedBindingReplacement) (*automation.Binding, error) {
	f.unmanagedReplacement = replacement
	return f.binding, f.err
}
func (f *transportFake) DeleteUnmanagedBindingIfUnchanged(_ context.Context, expected TransportUnmanagedBindingSnapshot) error {
	f.unmanagedDelete = expected
	return f.err
}
func (f *transportFake) CreateManagedBinding(context.Context, *automation.Binding) (*automation.Binding, error) {
	f.createCalls++
	return f.binding, f.err
}
func (f *transportFake) ReplaceManagedBinding(_ context.Context, replacement TransportManagedBindingReplacement) (*automation.Binding, error) {
	f.managedReplacement = replacement
	return f.binding, f.err
}
func (f *transportFake) DeleteManagedBindingIfUnchanged(_ context.Context, expected TransportManagedBindingSnapshot) error {
	f.managedDelete = expected
	return f.err
}
func (f *transportFake) MatchBindings(context.Context, string, string) (*TransportBindingMatchSnapshot, error) {
	return f.match, f.err
}
func (f *transportFake) GetEvent(context.Context, string, string) (*automation.Event, error) {
	return f.event, f.err
}
func (f *transportFake) ListEvents(_ context.Context, _ string, filter automation.EventFilter) ([]*automation.Event, error) {
	f.lastEventFilter = filter
	return f.events, f.err
}
func (f *transportFake) GetDelivery(context.Context, string, string) (*automation.Delivery, error) {
	return f.delivery, f.err
}
func (f *transportFake) ListDeliveries(context.Context, string, automation.DeliveryFilter) ([]*automation.Delivery, error) {
	return f.deliveries, f.err
}
func (f *transportFake) ReserveEvent(_ context.Context, request TransportEventReservation) (*TransportReservationResult, error) {
	f.reserveRequest = request
	return f.reservation, f.err
}
func (f *transportFake) ClaimDueDeliveries(_ context.Context, _, key string, _, _ time.Time, _ int) ([]TransportClaimedDelivery, error) {
	f.claimKeys = append(f.claimKeys, key)
	return f.claimed, f.err
}
func (f *transportFake) ClaimDueCron(_ context.Context, claim TransportCronClaim) ([]TransportCronOccurrence, error) {
	f.cronClaim = claim
	return f.cronOccurrences, f.err
}
func (f *transportFake) CompleteCron(_ context.Context, completion TransportCronCompletion) error {
	f.cronCompletion = completion
	return f.err
}
func (f *transportFake) TransitionDelivery(_ context.Context, transition TransportDeliveryTransition) (*automation.Delivery, error) {
	f.transition = transition
	return f.delivery, f.err
}

func TestAdapterRedactsReadsAndRejectsPlaintextBindingSecrets(t *testing.T) {
	persisted := &automation.Binding{
		WorkspaceKey: "WS", BindingID: "binding-a", WebhookSecret: "must-not-cross",
		EventTypePatterns: []string{"github.*"}, ActorFilter: &automation.ActorFilter{AllowActors: []string{"octocat"}},
	}
	fake := &transportFake{
		binding: persisted,
		match: &TransportBindingMatchSnapshot{
			WorkspaceKey: "WS", RouteKey: "github.push", BindingSetRevision: 4,
			Bindings: []*automation.Binding{{WorkspaceKey: "WS", BindingID: "exact", RouteKey: "github.push", WebhookSecret: "secret"},
				{WorkspaceKey: "WS", BindingID: "pattern", EventTypePatterns: []string{"github.*"}, WebhookSecret: "secret"}},
		},
	}
	adapter, err := New(fake)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := adapter.CreateBinding(t.Context(), persisted); !errors.Is(err, automation.ErrInvalid) || fake.createCalls != 0 {
		t.Fatalf("secret create = calls:%d err:%v", fake.createCalls, err)
	}
	got, err := adapter.GetBinding(t.Context(), "WS", "binding-a")
	if err != nil || got.WebhookSecret != "" || persisted.WebhookSecret == "" {
		t.Fatalf("GetBinding = %+v, %v (source=%+v)", got, err, persisted)
	}
	got.EventTypePatterns[0] = "changed"
	got.ActorFilter.AllowActors[0] = "changed"
	if persisted.EventTypePatterns[0] != "github.*" || persisted.ActorFilter.AllowActors[0] != "octocat" {
		t.Fatal("binding result was not defensively copied")
	}

	snapshot, err := adapter.MatchBindings(t.Context(), "WS", "github.push")
	if err != nil {
		t.Fatalf("MatchBindings: %v", err)
	}
	if got := []string{snapshot.Bindings[0].BindingID, snapshot.Bindings[1].BindingID}; !reflect.DeepEqual(got, []string{"exact", "pattern"}) {
		t.Fatalf("match order = %v", got)
	}
	for _, binding := range snapshot.Bindings {
		if binding.WebhookSecret != "" {
			t.Fatalf("match leaked secret: %+v", binding)
		}
	}
}

func TestAdapterManagedBindingCarriesExactConditionalSnapshotAndMapsConflict(t *testing.T) {
	createdAt := time.Date(2026, 7, 16, 12, 0, 0, 123000, time.UTC)
	updatedAt := createdAt.Add(time.Second)
	replacement := &automation.Binding{
		WorkspaceKey: "WS", BindingID: "binding-a", Name: "renamed", TargetAgentServiceID: "agent-1",
		RouteKey: "internal:agent-1", CreatedAt: createdAt, UpdatedAt: updatedAt.Add(time.Microsecond),
	}
	fake := &transportFake{binding: replacement}
	adapter, err := New(fake)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	expected := automation.ManagedBindingSnapshot{
		WorkspaceKey: "WS", BindingID: "binding-a", ExpectedTargetAgentServiceID: "agent-1",
		ExpectedRouteKey: "internal:agent-1", ExpectedCreatedAt: createdAt, ExpectedUpdatedAt: updatedAt,
	}
	got, err := adapter.ReplaceManagedBinding(t.Context(), automation.ManagedBindingReplacement{Expected: expected, Binding: replacement})
	if err != nil || got.WebhookSecret != "" {
		t.Fatalf("ReplaceManagedBinding = %+v, %v", got, err)
	}
	want := transportManagedBindingSnapshot(expected)
	if !reflect.DeepEqual(fake.managedReplacement.Expected, want) || fake.managedReplacement.Binding == replacement {
		t.Fatalf("transport replacement = %#v, want snapshot %#v and defensive copy", fake.managedReplacement, want)
	}
	if err := adapter.DeleteManagedBindingIfUnchanged(t.Context(), expected); err != nil || !reflect.DeepEqual(fake.managedDelete, want) {
		t.Fatalf("DeleteManagedBindingIfUnchanged = %v / %#v", err, fake.managedDelete)
	}

	fake.err = ErrTransportManagedBindingConflict
	if _, err := adapter.ReplaceManagedBinding(t.Context(), automation.ManagedBindingReplacement{Expected: expected, Binding: replacement}); !errors.Is(err, automation.ErrManagedBinding) || !errors.Is(err, ErrTransportManagedBindingConflict) {
		t.Fatalf("managed conflict = %v", err)
	}
	if err := adapter.DeleteManagedBindingIfUnchanged(t.Context(), expected); !errors.Is(err, automation.ErrManagedBinding) || !errors.Is(err, ErrTransportManagedBindingConflict) {
		t.Fatalf("managed delete conflict = %v", err)
	}
}

func TestAdapterUnmanagedBindingCarriesExactConditionalSnapshotAndMapsOwnershipConflict(t *testing.T) {
	createdAt := time.Date(2026, 7, 16, 12, 30, 0, 123000, time.UTC)
	updatedAt := createdAt.Add(time.Second)
	replacement := &automation.Binding{
		WorkspaceKey: "WS", BindingID: "ordinary-a", Name: "renamed",
		RouteKey: "internal:ordinary-a", CreatedAt: createdAt, UpdatedAt: updatedAt.Add(time.Microsecond),
	}
	fake := &transportFake{binding: replacement}
	adapter, err := New(fake)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	expected := automation.UnmanagedBindingSnapshot{
		WorkspaceKey: "WS", BindingID: "ordinary-a", ExpectedRouteKey: "internal:ordinary-a",
		ExpectedCreatedAt: createdAt, ExpectedUpdatedAt: updatedAt,
	}
	got, err := adapter.ReplaceUnmanagedBinding(t.Context(), automation.UnmanagedBindingReplacement{Expected: expected, Binding: replacement})
	if err != nil || got.WebhookSecret != "" {
		t.Fatalf("ReplaceUnmanagedBinding = %+v, %v", got, err)
	}
	want := transportUnmanagedBindingSnapshot(expected)
	if !reflect.DeepEqual(fake.unmanagedReplacement.Expected, want) || fake.unmanagedReplacement.Binding == replacement {
		t.Fatalf("transport replacement = %#v, want snapshot %#v and defensive copy", fake.unmanagedReplacement, want)
	}
	if err := adapter.DeleteUnmanagedBindingIfUnchanged(t.Context(), expected); err != nil || !reflect.DeepEqual(fake.unmanagedDelete, want) {
		t.Fatalf("DeleteUnmanagedBindingIfUnchanged = %v / %#v", err, fake.unmanagedDelete)
	}

	fake.err = ErrTransportManagedBindingConflict
	if _, err := adapter.ReplaceUnmanagedBinding(t.Context(), automation.UnmanagedBindingReplacement{Expected: expected, Binding: replacement}); !errors.Is(err, automation.ErrManagedBinding) || !errors.Is(err, ErrTransportManagedBindingConflict) {
		t.Fatalf("unmanaged ownership conflict = %v", err)
	}
	if err := adapter.DeleteUnmanagedBindingIfUnchanged(t.Context(), expected); !errors.Is(err, automation.ErrManagedBinding) || !errors.Is(err, ErrTransportManagedBindingConflict) {
		t.Fatalf("unmanaged delete ownership conflict = %v", err)
	}
}

func TestAdapterReservationPreservesRawBytesOrderAndImmutableTargets(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage("{ \n  \"action\" : \"opened\" \n}")
	event := &automation.Event{
		WorkspaceKey: "WS", EventID: "event-1", TriggerBindingID: "exact", SourceKind: "github",
		SourceEventID: "delivery-1", EventType: "issue.opened", RouteKey: "github.issue.opened",
		Origin: automation.EventOriginExternal, OccurredAt: now, ReceivedAt: now,
		IdempotencyKey: "github:delivery-1", Payload: append(json.RawMessage(nil), payload...),
		SubjectAttrs: map[string]string{"repo": "loom"}, EpicID: "epic-1",
	}
	accepted := &automation.Delivery{
		WorkspaceKey: "WS", DeliveryID: "delivery-a", TriggerEventID: event.EventID,
		TriggerBindingID: "exact", Status: automation.DeliveryAccepted,
		DriverID: "driver-a", DriverVersionID: "version-a", TargetEntrypoint: "run",
		TargetAgentServiceID: "agent-service-a", SourceKind: "github",
		ConcurrencyPolicy: automation.ConcurrencyQueue, RetryMaxAttempts: 5, RetryBackoffSeconds: 30,
		Attempt: 1, CreatedAt: now, UpdatedAt: now,
	}
	rejected := &automation.Delivery{
		WorkspaceKey: "WS", DeliveryID: "delivery-b", TriggerEventID: event.EventID,
		TriggerBindingID: "pattern", Status: automation.DeliveryRejected,
		RejectionReason: automation.RejectionReasonActorFilter, Attempt: 1, CreatedAt: now, UpdatedAt: now,
	}
	fake := &transportFake{reservation: &TransportReservationResult{
		Event: event, Deliveries: []*automation.Delivery{accepted, rejected},
		EffectiveVersions: []TransportCatalogGuard{{
			BindingID: "exact", DriverID: "driver-a", VersionID: "version-a", DriverRevision: 9,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		}},
		Replayed: true,
	}}
	adapter, _ := New(fake)
	reservation := automation.EventReservation{
		Event: event, Payload: payload, SubjectAttrs: map[string]string{"repo": "loom"}, EpicID: "epic-1",
		ExecutionNodeID: "node-1", ExecutionLeaseID: "lease-1", ExecutionFence: 8,
		BindingSetRevision: 7, MatchedBindingIDs: []string{"exact", "pattern"},
		CatalogGuards: []automation.CatalogGuard{{
			BindingID: "exact", DriverID: "driver-a", VersionID: "version-a", DriverRevision: 9,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		}},
	}
	result, err := adapter.ReserveEvent(t.Context(), reservation)
	if err != nil {
		t.Fatalf("ReserveEvent: %v", err)
	}
	if !result.Replayed || !reflect.DeepEqual(fake.reserveRequest.MatchedBindingIDs, []string{"exact", "pattern"}) ||
		fake.reserveRequest.NodeID != "node-1" || fake.reserveRequest.LeaseID != "lease-1" || fake.reserveRequest.FencingToken != 8 ||
		string(fake.reserveRequest.Payload) != string(payload) || string(result.Payload) != string(payload) {
		t.Fatalf("reservation request/result = %+v / %+v", fake.reserveRequest, result)
	}
	if result.Deliveries[0].Delivery.DeliveryID != "delivery-a" || result.Deliveries[1].Delivery.DeliveryID != "delivery-b" {
		t.Fatalf("delivery order = %+v", result.Deliveries)
	}
	target := result.Deliveries[0].Target
	if target == nil || target.DriverVersionID != "version-a" || target.DriverRevision != 9 ||
		target.TargetAgentServiceID != "agent-service-a" || target.RetryBackoff != 30*time.Second {
		t.Fatalf("accepted target = %+v", target)
	}
	if result.Deliveries[1].Target != nil {
		t.Fatalf("rejected target = %+v", result.Deliveries[1].Target)
	}
}

func TestAdapterAdmissionReplayOnlyMapsTypedMissAndRequiresCommittedGuards(t *testing.T) {
	event := &automation.Event{
		WorkspaceKey: "WS", SourceKind: "github", SourceEventID: "delivery-1", EventType: "push",
		RouteKey: "github.push", Origin: automation.EventOriginExternal, IdempotencyKey: "github:delivery-1",
	}
	fake := &transportFake{err: ErrTransportAdmissionReplayNotFound}
	adapter, _ := New(fake)
	_, err := adapter.ReserveEvent(t.Context(), automation.EventReservation{
		Event: event, ReplayOnly: true, Fingerprint: "immutable",
	})
	if !errors.Is(err, automation.ErrAdmissionReplayNotFound) || !fake.reserveRequest.ReplayOnly ||
		fake.reserveRequest.BindingSetRevision != 0 || len(fake.reserveRequest.CatalogGuards) != 0 {
		t.Fatalf("replay miss/request = %v / %#v", err, fake.reserveRequest)
	}

	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	event.EventID, event.ReceivedAt, event.OccurredAt = "event-1", now, now
	fake.err = nil
	fake.reservation = &TransportReservationResult{
		Event: event, Replayed: true,
		Deliveries: []*automation.Delivery{{
			WorkspaceKey: "WS", DeliveryID: "delivery-1", TriggerEventID: "event-1", TriggerBindingID: "binding-1",
			Status: automation.DeliveryAccepted, DriverID: "driver-1", DriverVersionID: "version-1",
			TargetEntrypoint: "run", SourceKind: "github", ConcurrencyPolicy: automation.ConcurrencyAllow,
			Attempt: 1, CreatedAt: now, UpdatedAt: now,
		}},
	}
	_, err = adapter.ReserveEvent(t.Context(), automation.EventReservation{Event: event, ReplayOnly: true})
	if !errors.Is(err, automation.ErrInvalidPersistedState) {
		t.Fatalf("accepted replay without committed guard error = %v", err)
	}
}

func TestAdapterClaimUsesStableKeyAndRestoresRetryEnvelope(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage("{ \"retry\" : true }")
	event := &automation.Event{WorkspaceKey: "WS", EventID: "event-1", EpicID: "epic-1", Payload: payload, SubjectAttrs: map[string]string{"repo": "loom"}}
	delivery := &automation.Delivery{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", TriggerEventID: "event-1", TriggerBindingID: "binding-a",
		Status: automation.DeliveryFailed, DriverID: "driver-a", DriverVersionID: "version-a",
		TargetEntrypoint: "run", TargetAgentServiceID: "agent-service-a", SourceKind: "github",
		ConcurrencyPolicy: automation.ConcurrencyQueue, RetryMaxAttempts: 5, RetryBackoffSeconds: 30, Attempt: 2,
	}
	fake := &transportFake{claimed: []TransportClaimedDelivery{{Event: event, Delivery: delivery}}}
	adapter, _ := New(fake)
	for range 2 {
		candidates, err := adapter.ClaimDueDeliveries(t.Context(), "WS", now, now.Add(time.Minute), 7)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("ClaimDueDeliveries = %+v, %v", candidates, err)
		}
		candidate := candidates[0]
		if string(candidate.Payload) != string(payload) || candidate.EpicID != "epic-1" ||
			candidate.Target.TargetAgentServiceID != "agent-service-a" || candidate.Target.RetryBackoff != 30*time.Second {
			t.Fatalf("candidate = %+v", candidate)
		}
	}
	if len(fake.claimKeys) != 2 || fake.claimKeys[0] != fake.claimKeys[1] {
		t.Fatalf("claim keys = %v", fake.claimKeys)
	}
	_, _ = adapter.ClaimDueDeliveries(t.Context(), "WS", now.Add(time.Second), now.Add(time.Minute+time.Second), 7)
	if fake.claimKeys[2] == fake.claimKeys[1] {
		t.Fatalf("distinct sweep reused key %q", fake.claimKeys[2])
	}
}

func TestAdapterMapsRetryableFailureToTerminalDuplicateTransition(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	fake := &transportFake{delivery: &automation.Delivery{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", TriggerEventID: "event-1", TriggerBindingID: "prompt-z",
		Status: automation.DeliveryDuplicate, Attempt: 2, CreatedAt: now, UpdatedAt: now,
	}}
	adapter, err := New(fake)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := adapter.TransitionDelivery(t.Context(), automation.DeliveryTransition{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", IdempotencyKey: "delivery-1:failed:2:duplicate:2",
		ExpectedStatus: automation.DeliveryFailed, ExpectedAttempt: 2, Status: automation.DeliveryDuplicate, Attempt: 2,
	})
	if err != nil || result.Status != automation.DeliveryDuplicate {
		t.Fatalf("TransitionDelivery = %+v, %v", result, err)
	}
	if fake.transition.ExpectedStatus != automation.DeliveryFailed || fake.transition.ExpectedAttempt != 2 ||
		fake.transition.Status != automation.DeliveryDuplicate || fake.transition.NextRetryAt != nil {
		t.Fatalf("transport transition = %+v", fake.transition)
	}
}

func TestAdapterCronPreservesClaimCompletionAndFailsClosed(t *testing.T) {
	before := time.Date(2026, 7, 16, 12, 0, 0, 0, time.FixedZone("offset", -7*60*60))
	claimUntil := before.Add(time.Minute)
	claim := automation.CronClaim{
		WorkspaceKey: "WS", Before: before, ClaimUntil: claimUntil,
		IdempotencyKey: "cron-sweep:WS:2026-07-16T12:00:00-07:00", Limit: 7,
	}
	fake := &transportFake{cronOccurrences: []TransportCronOccurrence{{
		WorkspaceKey: "WS", BindingID: "binding-a", RouteKey: "schedule.nightly",
		OccurrenceID: "cron:binding-a:2026-07-16T19:00:00Z", OccurredAt: before,
	}}}
	adapter, err := New(fake)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	occurrences, err := adapter.ClaimDueCron(t.Context(), claim)
	if err != nil {
		t.Fatalf("ClaimDueCron: %v", err)
	}
	wantClaim := TransportCronClaim{
		WorkspaceKey: claim.WorkspaceKey, Before: claim.Before, ClaimUntil: claim.ClaimUntil,
		IdempotencyKey: claim.IdempotencyKey, Limit: claim.Limit,
	}
	if !reflect.DeepEqual(fake.cronClaim, wantClaim) {
		t.Fatalf("transport claim = %+v, want %+v", fake.cronClaim, wantClaim)
	}
	if len(occurrences) != 1 || occurrences[0].OccurrenceID != fake.cronOccurrences[0].OccurrenceID ||
		!occurrences[0].OccurredAt.Equal(before.UTC()) || occurrences[0].OccurredAt.Location() != time.UTC {
		t.Fatalf("occurrences = %+v", occurrences)
	}

	completion := automation.CronCompletion{
		WorkspaceKey: "WS", BindingID: "binding-a",
		OccurrenceID: occurrences[0].OccurrenceID, Status: automation.CronCompletionFailed,
		ErrorClass: "admission_unavailable",
	}
	if err := adapter.CompleteCron(t.Context(), completion); err != nil {
		t.Fatalf("CompleteCron: %v", err)
	}
	if !reflect.DeepEqual(fake.cronCompletion, TransportCronCompletion(completion)) {
		t.Fatalf("transport completion = %+v, want %+v", fake.cronCompletion, completion)
	}

	t.Run("wrong workspace", func(t *testing.T) {
		fake.cronOccurrences = []TransportCronOccurrence{{
			WorkspaceKey: "OTHER", BindingID: "binding-a", RouteKey: "schedule.nightly",
			OccurrenceID: "cron:binding-a:2026-07-16T19:00:00Z", OccurredAt: before,
		}}
		_, err := adapter.ClaimDueCron(t.Context(), claim)
		if !errors.Is(err, automation.ErrWrongWorkspace) || !errors.Is(err, automation.ErrInvalidPersistedState) {
			t.Fatalf("error = %v, want wrong-workspace invalid-persisted-state", err)
		}
	})

	t.Run("malformed occurrence", func(t *testing.T) {
		fake.cronOccurrences = []TransportCronOccurrence{{
			WorkspaceKey: "WS", BindingID: "binding-a", RouteKey: "schedule.nightly",
			OccurrenceID: "not-a-cron-occurrence", OccurredAt: before,
		}}
		_, err := adapter.ClaimDueCron(t.Context(), claim)
		if !errors.Is(err, automation.ErrInvalidPersistedState) {
			t.Fatalf("error = %v, want invalid persisted state", err)
		}
	})

	t.Run("duplicate occurrence", func(t *testing.T) {
		occurrence := TransportCronOccurrence{
			WorkspaceKey: "WS", BindingID: "binding-a", RouteKey: "schedule.nightly",
			OccurrenceID: "cron:binding-a:2026-07-16T19:00:00Z", OccurredAt: before,
		}
		fake.cronOccurrences = []TransportCronOccurrence{occurrence, occurrence}
		_, err := adapter.ClaimDueCron(t.Context(), claim)
		if !errors.Is(err, automation.ErrInvalidPersistedState) {
			t.Fatalf("error = %v, want invalid persisted state", err)
		}
	})
}

func TestAdapterMapsTypedTransportErrors(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{name: "route", in: ErrTransportRouteNotFound, want: automation.ErrNoMatchingBinding},
		{name: "parent", in: ErrTransportParentRunNotFound, want: automation.ErrParentEventNotFound},
		{name: "hop", in: ErrTransportHopDepthExceeded, want: automation.ErrHopDepthExceeded},
		{name: "delivery", in: ErrTransportDeliveryNotFound, want: automation.ErrNotFound},
		{name: "invalid", in: ErrTransportPayloadDigestMismatch, want: automation.ErrInvalid},
		{name: "binding conflict", in: ErrTransportBindingSnapshotConflict, want: automation.ErrConflict},
		{name: "transition conflict", in: ErrTransportDeliveryTransitionConflict, want: automation.ErrConflict},
		{name: "cron occurrence", in: ErrTransportCronOccurrenceNotFound, want: automation.ErrNotFound},
		{name: "cron completion", in: ErrTransportCronCompletionConflict, want: automation.ErrConflict},
		{name: "catalog unavailable", in: ErrTransportCatalogUnavailable, want: automation.ErrUnavailable},
		{name: "managed binding", in: ErrTransportManagedBindingConflict, want: automation.ErrManagedBinding},
		{name: "unknown", in: errors.New("connection reset"), want: automation.ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _ := New(&transportFake{err: test.in})
			_, err := adapter.GetBinding(t.Context(), "WS", "binding-a")
			if !errors.Is(err, test.want) || !errors.Is(err, test.in) {
				t.Fatalf("error = %v, want %v and source", err, test.want)
			}
		})
	}
	if _, err := New(nil); !errors.Is(err, automation.ErrUnavailable) {
		t.Fatalf("New(nil) = %v", err)
	}
}
