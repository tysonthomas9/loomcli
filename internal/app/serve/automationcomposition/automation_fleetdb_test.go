package automationcomposition

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	automationfleetdb "github.com/tysonthomas9/loomcli/internal/modules/automation/fleetdb"
)

type automationInfraTransportStub struct {
	infrafleetdb.AutomationTransport
	reservation    infrafleetdb.AutomationEventReservation
	result         *infrafleetdb.AutomationReservationResult
	cronClaim      infrafleetdb.AutomationCronClaim
	cronItems      []infrafleetdb.AutomationCronOccurrence
	cronCompletion infrafleetdb.AutomationCronCompletion
	err            error
}

func (stub *automationInfraTransportStub) ReserveEvent(_ context.Context, reservation infrafleetdb.AutomationEventReservation) (*infrafleetdb.AutomationReservationResult, error) {
	stub.reservation = reservation
	return stub.result, stub.err
}

func (stub *automationInfraTransportStub) ClaimDueCron(_ context.Context, claim infrafleetdb.AutomationCronClaim) ([]infrafleetdb.AutomationCronOccurrence, error) {
	stub.cronClaim = claim
	return stub.cronItems, stub.err
}

func (stub *automationInfraTransportStub) CompleteCron(_ context.Context, completion infrafleetdb.AutomationCronCompletion) error {
	stub.cronCompletion = completion
	return stub.err
}

func TestAutomationFleetDBTransportPreservesExactReservationBytes(t *testing.T) {
	payload := json.RawMessage("{ \n  \"value\" : 1\n}")
	stub := &automationInfraTransportStub{result: &infrafleetdb.AutomationReservationResult{
		Event:      &automation.Event{WorkspaceKey: "TEST", EventID: "event-1", Payload: append(json.RawMessage(nil), payload...)},
		Deliveries: []*automation.Delivery{{WorkspaceKey: "TEST", DeliveryID: "delivery-1"}},
		EffectiveVersions: []infrafleetdb.AutomationCatalogGuard{{
			BindingID: "binding-1", DriverID: "driver-1", VersionID: "version-1", DriverRevision: 7,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		}},
		Replayed: true,
	}}
	bridge := &automationFleetDBTransport{transport: stub}
	result, err := bridge.ReserveEvent(context.Background(), automationfleetdb.TransportEventReservation{
		WorkspaceKey: "TEST", RouteKey: "github.push", IdempotencyKey: "github:delivery-1",
		Origin: automation.EventOriginExternal, BindingSetRevision: 4,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 17,
		MatchedBindingIDs: []string{"binding-1"},
		CatalogGuards: []automationfleetdb.TransportCatalogGuard{{
			BindingID: "binding-1", DriverID: "driver-1", VersionID: "version-1", DriverRevision: 7,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		}},
		SourceEventID: "delivery-1", EventType: "push", Payload: payload,
		SubjectAttrs: map[string]string{"ref": "main"},
	})
	if err != nil {
		t.Fatalf("ReserveEvent: %v", err)
	}
	if string(stub.reservation.Payload) != string(payload) {
		t.Fatalf("transport payload = %q, want exact %q", stub.reservation.Payload, payload)
	}
	if stub.reservation.NodeID != "node-1" || stub.reservation.LeaseID != "lease-1" || stub.reservation.FencingToken != 17 {
		t.Fatalf("transport owner tuple = %q/%q/%d", stub.reservation.NodeID, stub.reservation.LeaseID, stub.reservation.FencingToken)
	}
	stub.reservation.Payload[0] = '['
	if string(payload) != "{ \n  \"value\" : 1\n}" {
		t.Fatalf("transport retained caller payload storage: %q", payload)
	}
	if result == nil || !result.Replayed || result.Event == nil || string(result.Event.Payload) != string(payload) ||
		len(result.Deliveries) != 1 || len(result.EffectiveVersions) != 1 || result.EffectiveVersions[0].DriverRevision != 7 {
		t.Fatalf("reservation result = %#v", result)
	}
	if got := stub.reservation.SubjectAttrs["ref"]; got != "main" {
		t.Fatalf("subject attrs ref = %q", got)
	}
}

func TestAutomationFleetDBTransportPreservesCronClaimAndCompletion(t *testing.T) {
	before := time.Date(2026, 7, 16, 12, 0, 0, 0, time.FixedZone("offset", -7*60*60))
	claimUntil := before.Add(time.Minute)
	stub := &automationInfraTransportStub{cronItems: []infrafleetdb.AutomationCronOccurrence{{
		WorkspaceKey: "TEST", BindingID: "binding-a", RouteKey: "schedule.nightly",
		OccurrenceID: "cron:binding-a:2026-07-16T19:00:00Z", OccurredAt: before,
	}}}
	bridge := &automationFleetDBTransport{transport: stub}
	claim := automationfleetdb.TransportCronClaim{
		WorkspaceKey: "TEST", IdempotencyKey: "cron-sweep:TEST:one",
		Before: before, ClaimUntil: claimUntil, Limit: 11,
	}

	items, err := bridge.ClaimDueCron(t.Context(), claim)
	if err != nil {
		t.Fatalf("ClaimDueCron: %v", err)
	}
	wantClaim := infrafleetdb.AutomationCronClaim{
		WorkspaceKey: claim.WorkspaceKey, IdempotencyKey: claim.IdempotencyKey,
		Before: claim.Before, ClaimUntil: claim.ClaimUntil, Limit: claim.Limit,
	}
	if !reflect.DeepEqual(stub.cronClaim, wantClaim) {
		t.Fatalf("infra claim = %+v, want %+v", stub.cronClaim, wantClaim)
	}
	if len(items) != 1 || items[0].WorkspaceKey != "TEST" ||
		items[0].OccurrenceID != "cron:binding-a:2026-07-16T19:00:00Z" || !items[0].OccurredAt.Equal(before) {
		t.Fatalf("cron items = %+v", items)
	}

	completion := automationfleetdb.TransportCronCompletion{
		WorkspaceKey: "TEST", BindingID: "binding-a",
		OccurrenceID: items[0].OccurrenceID, Status: automation.CronCompletionFailed,
		ErrorClass: "admission_unavailable",
	}
	if err := bridge.CompleteCron(t.Context(), completion); err != nil {
		t.Fatalf("CompleteCron: %v", err)
	}
	wantCompletion := infrafleetdb.AutomationCronCompletion{
		WorkspaceKey: completion.WorkspaceKey, BindingID: completion.BindingID,
		OccurrenceID: completion.OccurrenceID,
		Status:       infrafleetdb.AutomationCronCompletionFailed,
		ErrorClass:   completion.ErrorClass,
	}
	if !reflect.DeepEqual(stub.cronCompletion, wantCompletion) {
		t.Fatalf("infra completion = %+v, want %+v", stub.cronCompletion, wantCompletion)
	}
}

func TestAutomationFleetDBTransportErrorVocabulary(t *testing.T) {
	if got := newAutomationFleetDBTransport(nil); got != nil {
		t.Fatalf("nil client transport = %#v, want nil", got)
	}
	tests := []struct {
		input error
		want  error
	}{
		{infrafleetdb.ErrAutomationInvalid, automationfleetdb.ErrTransportInvalid},
		{infrafleetdb.ErrAutomationRouteNotFound, automationfleetdb.ErrTransportRouteNotFound},
		{infrafleetdb.ErrAutomationParentRunNotFound, automationfleetdb.ErrTransportParentRunNotFound},
		{infrafleetdb.ErrAutomationIdempotencyConflict, automationfleetdb.ErrTransportIdempotencyConflict},
		{infrafleetdb.ErrAutomationBindingSnapshotConflict, automationfleetdb.ErrTransportBindingSnapshotConflict},
		{infrafleetdb.ErrAutomationCatalogSnapshotConflict, automationfleetdb.ErrTransportCatalogSnapshotConflict},
		{infrafleetdb.ErrAutomationHopDepthExceeded, automationfleetdb.ErrTransportHopDepthExceeded},
		{infrafleetdb.ErrAutomationCatalogUnavailable, automationfleetdb.ErrTransportCatalogUnavailable},
		{infrafleetdb.ErrAutomationFanoutLimitExceeded, automationfleetdb.ErrTransportFanoutLimitExceeded},
		{infrafleetdb.ErrAutomationAdmissionUnavailable, automationfleetdb.ErrTransportAdmissionUnavailable},
		{infrafleetdb.ErrAutomationAdmissionReplayNotFound, automationfleetdb.ErrTransportAdmissionReplayNotFound},
		{infrafleetdb.ErrAutomationDeliveryNotFound, automationfleetdb.ErrTransportDeliveryNotFound},
		{infrafleetdb.ErrAutomationBindingNotFound, automationfleetdb.ErrTransportNotFound},
		{domain.ErrNotFound, automationfleetdb.ErrTransportNotFound},
		{infrafleetdb.ErrAutomationDeliveryTransitionConflict, automationfleetdb.ErrTransportDeliveryTransitionConflict},
		{infrafleetdb.ErrAutomationPayloadDigestMismatch, automationfleetdb.ErrTransportPayloadDigestMismatch},
		{infrafleetdb.ErrAutomationCronOccurrenceNotFound, automationfleetdb.ErrTransportCronOccurrenceNotFound},
		{infrafleetdb.ErrAutomationCronCompletionConflict, automationfleetdb.ErrTransportCronCompletionConflict},
	}
	for _, test := range tests {
		translated := translateAutomationFleetDBError(test.input)
		if !errors.Is(translated, test.input) || !errors.Is(translated, test.want) {
			t.Fatalf("translate %v = %v, want both original and %v", test.input, translated, test.want)
		}
	}
	unknown := errors.New("unknown transport error")
	if translated := translateAutomationFleetDBError(unknown); translated != unknown {
		t.Fatalf("unknown translation = %v, want identity", translated)
	}
}
