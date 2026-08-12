package fleetdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestAutomationTransportUsesSharedClientAndBindingBoundary(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/v1/WS/trigger-bindings" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if _, exists := body["webhook_secret"]; exists {
			t.Errorf("create body leaked webhook_secret: %s", body["webhook_secret"])
		}
		_ = json.NewEncoder(w).Encode(automation.Binding{
			WorkspaceKey: "WS", BindingID: "binding-a", SourceKind: "github",
			DriverID: "driver-a", DriverVersionID: "version-a", ConcurrencyPolicy: automation.ConcurrencyAllow,
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.Automation() == nil || client.Automation() != client.Automation() {
		t.Fatal("Automation did not reuse one shared transport surface")
	}
	binding := &automation.Binding{
		WorkspaceKey: "WS", BindingID: "binding-a", Name: "binding-a", SourceKind: "github",
		RouteKey: "github.push", DriverID: "driver-a", DriverVersionID: "version-a",
		ConcurrencyPolicy: automation.ConcurrencyAllow,
	}
	if got, err := client.Automation().CreateBinding(t.Context(), binding); err != nil || got.BindingID != "binding-a" {
		t.Fatalf("CreateBinding = %+v, %v", got, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("CreateBinding requests = %d, want 1", requests.Load())
	}
}

func TestAutomationTransportPreservesMatchOrderAndRawPayloadBytes(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 123, time.UTC)
	payload := []byte("{ \n  \"action\" : \"opened\" \n}")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/WS/automation/binding-matches/github.issue.opened":
			_ = json.NewEncoder(w).Encode(AutomationBindingMatchSnapshot{
				WorkspaceKey: "WS", RouteKey: "github.issue.opened", BindingSetRevision: 7,
				Bindings: []*automation.Binding{
					{WorkspaceKey: "WS", BindingID: "exact", RouteKey: "github.issue.opened", Enabled: true},
					{WorkspaceKey: "WS", BindingID: "a-pattern", EventTypePatterns: []string{"github.*.*"}, Enabled: true},
					{WorkspaceKey: "WS", BindingID: "z-pattern", EventTypePatterns: []string{"github.*.*"}, Enabled: true},
				},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/WS/automation/admissions/external/github.issue.opened":
			if got := r.Header.Get("Idempotency-Key"); got != "github:delivery-1" {
				t.Errorf("Idempotency-Key = %q", got)
			}
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				PayloadBase64 []byte `json:"payload_base64"`
			}
			if err := json.Unmarshal(raw, &body); err != nil || !bytes.Equal(body.PayloadBase64, payload) {
				t.Errorf("payload_base64 = %q, err=%v, body=%s", body.PayloadBase64, err, raw)
			}
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			for _, forbidden := range []string{"payload", "origin", "hop_depth", "emitting_run_id", "epic_id", "signature_status", "idempotency_key"} {
				if _, exists := fields[forbidden]; exists {
					t.Errorf("request includes forbidden field %q: %s", forbidden, raw)
				}
			}
			_ = json.NewEncoder(w).Encode(automationReservationWire{
				Event: &automationEventWire{
					WorkspaceKey: "WS", EventID: "event-1", TriggerBindingID: "exact", SourceKind: "github",
					SourceEventID: "delivery-1", EventType: "issue.opened", RouteKey: "github.issue.opened",
					Origin: automation.EventOriginExternal, OccurredAt: now, ReceivedAt: now,
					IdempotencyKey: "github:delivery-1", PayloadBase64: payload,
				},
				Deliveries: []*automation.Delivery{{
					WorkspaceKey: "WS", DeliveryID: "delivery-a", TriggerEventID: "event-1", TriggerBindingID: "exact",
					Status: automation.DeliveryAccepted, DriverID: "driver-a", DriverVersionID: "version-a",
					TargetEntrypoint: "run", TargetAgentServiceID: "agent-service-a", SourceKind: "github",
					ConcurrencyPolicy: automation.ConcurrencyQueue, RetryMaxAttempts: 5, RetryBackoffSeconds: 30,
					Attempt: 1, CreatedAt: now, UpdatedAt: now,
				}},
				EffectiveVersions: []AutomationCatalogGuard{{
					BindingID: "exact", DriverID: "driver-a", VersionID: "version-a", DriverRevision: 9,
					SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	transport := client.Automation()
	snapshot, err := transport.MatchBindings(t.Context(), "WS", "github.issue.opened")
	if err != nil {
		t.Fatalf("MatchBindings: %v", err)
	}
	gotOrder := []string{snapshot.Bindings[0].BindingID, snapshot.Bindings[1].BindingID, snapshot.Bindings[2].BindingID}
	if !reflect.DeepEqual(gotOrder, []string{"exact", "a-pattern", "z-pattern"}) {
		t.Fatalf("match order = %v", gotOrder)
	}
	result, err := transport.ReserveEvent(t.Context(), AutomationEventReservation{
		WorkspaceKey: "WS", RouteKey: "github.issue.opened", IdempotencyKey: "github:delivery-1",
		Origin: automation.EventOriginExternal, BindingSetRevision: 7,
		MatchedBindingIDs: []string{"exact", "a-pattern", "z-pattern"},
		CatalogGuards: []AutomationCatalogGuard{{
			BindingID: "exact", DriverID: "driver-a", VersionID: "version-a", DriverRevision: 9,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		}},
		SourceEventID: "delivery-1", EventType: "issue.opened", OccurredAt: now,
		RawPayloadDigest: "sha256:digest", Payload: append(json.RawMessage(nil), payload...),
	})
	if err != nil {
		t.Fatalf("ReserveEvent: %v", err)
	}
	if result.Event == nil || !bytes.Equal(result.Event.Payload, payload) ||
		result.Deliveries[0].TargetAgentServiceID != "agent-service-a" ||
		len(result.EffectiveVersions) != 1 || result.EffectiveVersions[0].DriverRevision != 9 {
		t.Fatalf("reservation result = %+v", result)
	}
}

func TestAutomationTransportAdmissionReplayOnlyOmitsMutableSnapshots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if string(body["replay_only"]) != "true" || string(body["event_type"]) != `"push"` {
			t.Errorf("replay body = %#v", body)
		}
		for _, omitted := range []string{"binding_set_revision", "matched_binding_ids", "effective_versions"} {
			if _, ok := body[omitted]; ok {
				t.Errorf("replay body includes mutable %q: %#v", omitted, body)
			}
		}
		_ = json.NewEncoder(w).Encode(automationReservationWire{Replayed: true})
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	result, err := client.Automation().ReserveEvent(t.Context(), AutomationEventReservation{
		WorkspaceKey: "WS", RouteKey: "github.push", IdempotencyKey: "github:delivery-1",
		ReplayOnly: true, Origin: automation.EventOriginExternal, EventType: "push",
	})
	if err != nil || result == nil || !result.Replayed {
		t.Fatalf("replay result = %#v, %v", result, err)
	}
}

func TestAutomationTransportWorkflowReplayCarriesCurrentOwnerPrecondition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.EscapedPath(), "/api/v1/WS/driver-runs/run-1/automation/admissions/workflow.done"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := r.Header.Get("Idempotency-Key"); got != automation.InternalEventIdempotencyKey("WS", "emission-1") {
			t.Errorf("Idempotency-Key = %q", got)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if string(body["replay_only"]) != "true" || string(body["node_id"]) != `"node-b"` ||
			string(body["lease_id"]) != `"lease-b"` || string(body["fencing_token"]) != "8" {
			t.Errorf("workflow handoff replay body = %#v", body)
		}
		for _, omitted := range []string{"binding_set_revision", "matched_binding_ids", "effective_versions"} {
			if _, ok := body[omitted]; ok {
				t.Errorf("workflow replay body includes mutable %q: %#v", omitted, body)
			}
		}
		_ = json.NewEncoder(w).Encode(automationReservationWire{Replayed: true})
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	result, err := client.Automation().ReserveEvent(t.Context(), AutomationEventReservation{
		WorkspaceKey: "WS", RouteKey: "workflow.done", IdempotencyKey: automation.InternalEventIdempotencyKey("WS", "emission-1"),
		ReplayOnly: true, Origin: automation.EventOriginWorkflow, EmittingRunID: "run-1",
		NodeID: "node-b", LeaseID: "lease-b", FencingToken: 8,
		SourceEventID: "emission-1", EventType: "workflow.done", Payload: []byte(`{"stable":true}`),
	})
	if err != nil || result == nil || !result.Replayed {
		t.Fatalf("workflow replay result = %#v, %v", result, err)
	}
}

func TestAutomationTransportFreshAdmissionIncludesEmptyEffectiveVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if string(body["effective_versions"]) != "[]" {
			t.Errorf("fresh all-filtered body effective_versions = %s; body=%#v", body["effective_versions"], body)
		}
		if _, ok := body["replay_only"]; ok {
			t.Errorf("fresh body includes replay_only: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(automationReservationWire{})
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	_, err := client.Automation().ReserveEvent(t.Context(), AutomationEventReservation{
		WorkspaceKey: "WS", RouteKey: "github.push", IdempotencyKey: "github:delivery-1",
		Origin: automation.EventOriginExternal, BindingSetRevision: 7,
		MatchedBindingIDs: []string{"binding-1"}, EventType: "push",
	})
	if err != nil {
		t.Fatalf("fresh reservation: %v", err)
	}
}

func TestAutomationTransportSelectsTrustedAdmissionRoutes(t *testing.T) {
	tests := []struct {
		name    string
		origin  automation.EventOrigin
		runID   string
		nodeID  string
		leaseID string
		fence   int64
		path    string
	}{
		{name: "external", origin: automation.EventOriginExternal, path: "/api/v1/WS/automation/admissions/external/route.one"},
		{name: "system", origin: automation.EventOriginSystem, path: "/api/v1/WS/automation/admissions/system/route.one"},
		{name: "workflow", origin: automation.EventOriginWorkflow, runID: "run/one", nodeID: "node-1", leaseID: "lease-1", fence: 9,
			path: "/api/v1/WS/driver-runs/run%2Fone/automation/admissions/route.one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.path {
					t.Errorf("path = %q, want %q", r.URL.EscapedPath(), test.path)
				}
				if test.origin == automation.EventOriginWorkflow {
					var body automationAdmissionBody
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode workflow admission body: %v", err)
					} else if body.NodeID != test.nodeID || body.LeaseID != test.leaseID || body.FencingToken != test.fence {
						t.Errorf("workflow owner body = %q/%q/%d", body.NodeID, body.LeaseID, body.FencingToken)
					}
				}
				_ = json.NewEncoder(w).Encode(automationReservationWire{})
			}))
			defer server.Close()
			client, _ := New(Config{BaseURL: server.URL})
			_, err := client.Automation().ReserveEvent(t.Context(), AutomationEventReservation{
				WorkspaceKey: "WS", RouteKey: "route.one", IdempotencyKey: "idem",
				Origin: test.origin, EmittingRunID: test.runID, NodeID: test.nodeID, LeaseID: test.leaseID, FencingToken: test.fence,
			})
			if err != nil {
				t.Fatalf("ReserveEvent: %v", err)
			}
		})
	}
	client, _ := New(Config{BaseURL: "http://unused.invalid"})
	if _, err := client.Automation().ReserveEvent(t.Context(), AutomationEventReservation{Origin: automation.EventOriginWorkflow}); !errors.Is(err, ErrAutomationInvalid) {
		t.Fatalf("missing workflow run = %v", err)
	}
}

func TestAutomationTransportClaimDispatchTransitionAndOriginFilter(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	payload := []byte("{ \"retry\" : true }")
	delivery := &automation.Delivery{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", TriggerEventID: "event-system", TriggerBindingID: "binding-a",
		Status: automation.DeliveryFailed, DriverID: "driver-a", DriverVersionID: "version-a",
		TargetEntrypoint: "run", TargetAgentServiceID: "agent-service-a", SourceKind: "internal",
		ConcurrencyPolicy: automation.ConcurrencyQueue, RetryMaxAttempts: 5, RetryBackoffSeconds: 30,
		Attempt: 2, NextRetryAt: ptrTime(now), CreatedAt: now, UpdatedAt: now,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-events":
			if r.URL.Query().Has("limit") {
				t.Errorf("origin-filtered list sent premature limit: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger_events": []automationEventWire{
				{WorkspaceKey: "WS", EventID: "event-external", Origin: automation.EventOriginExternal},
				{WorkspaceKey: "WS", EventID: "event-system", Origin: automation.EventOriginSystem, PayloadBase64: payload},
			}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/claim-due"):
			if r.Header.Get("Idempotency-Key") != "claim-key" {
				t.Errorf("claim Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"deliveries": []automationClaimedDeliveryWire{{
					Event:    automationEventWire{WorkspaceKey: "WS", EventID: "event-system", Origin: automation.EventOriginSystem, PayloadBase64: payload},
					Delivery: delivery,
				}}, "count": 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dispatch"):
			if r.Header.Get("Idempotency-Key") != "dispatch-key" {
				t.Errorf("dispatch Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
			}
			var body map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 2 || string(body["expected_status"]) != `"failed"` || string(body["expected_attempt"]) != "2" {
				t.Errorf("dispatch body = %s", body)
			}
			dispatched := *delivery
			dispatched.Status, dispatched.DriverRunID = automation.DeliveryDispatched, "run-1"
			_ = json.NewEncoder(w).Encode(automationDeliveryDispatchWire{
				Event: &automationEventWire{
					WorkspaceKey: "WS", EventID: "event-system", Origin: automation.EventOriginSystem,
					PayloadBase64: payload,
				},
				Delivery: &dispatched,
				DriverRun: &domain.DriverRun{
					WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver-a", DriverVersionID: "version-a",
				},
				Outcome: AutomationDeliveryDispatchRun, SupersededRunIDs: []string{"run-old"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transition"):
			if r.Header.Get("Idempotency-Key") != "transition-key" {
				t.Errorf("transition Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
			}
			var body map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, exists := body["attempt"]; exists {
				t.Errorf("transition body included caller attempt: %+v", body)
			}
			out := *delivery
			out.Status, out.DriverRunID = automation.DeliveryDispatched, "run-1"
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	transport := client.Automation()
	events, err := transport.ListEvents(t.Context(), "WS", AutomationEventFilter{Origin: automation.EventOriginSystem, Limit: 1})
	if err != nil || len(events) != 1 || events[0].EventID != "event-system" || !bytes.Equal(events[0].Payload, payload) {
		t.Fatalf("ListEvents = %+v, %v", events, err)
	}
	claimed, err := transport.ClaimDueDeliveries(t.Context(), "WS", "claim-key", now, now.Add(time.Minute), 5)
	if err != nil || len(claimed) != 1 || !bytes.Equal(claimed[0].Event.Payload, payload) {
		t.Fatalf("ClaimDueDeliveries = %+v, %v", claimed, err)
	}
	dispatched, err := transport.DispatchAutomationDelivery(t.Context(), AutomationDeliveryDispatch{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", IdempotencyKey: "dispatch-key",
		ExpectedStatus: automation.DeliveryFailed, ExpectedAttempt: 2,
	})
	if err != nil || dispatched.Outcome != AutomationDeliveryDispatchRun ||
		dispatched.Delivery.DriverRunID != "run-1" || dispatched.DriverRun.RunID != "run-1" ||
		!bytes.Equal(dispatched.Event.Payload, payload) || !reflect.DeepEqual(dispatched.SupersededRunIDs, []string{"run-old"}) {
		t.Fatalf("DispatchAutomationDelivery = %+v, %v", dispatched, err)
	}
	transitioned, err := transport.TransitionDelivery(t.Context(), AutomationDeliveryTransition{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", IdempotencyKey: "transition-key",
		ExpectedStatus: automation.DeliveryFailed, ExpectedAttempt: 2,
		Status: automation.DeliveryDispatched, DriverRunID: "run-1",
	})
	if err != nil || transitioned.Status != automation.DeliveryDispatched || transitioned.DriverRunID != "run-1" {
		t.Fatalf("TransitionDelivery = %+v, %v", transitioned, err)
	}
}

func TestAutomationTransportCronClaimAndCompletionWireContract(t *testing.T) {
	location := time.FixedZone("UTC+2", 2*60*60)
	before := time.Date(2026, 7, 16, 14, 0, 0, 123, location)
	claimUntil := before.Add(time.Minute)
	occurredAt := before.Add(-time.Minute).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v1/WS/automation/cron/claim-due":
			if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != "automation-cron:key" {
				t.Errorf("claim request = %s key=%q", r.Method, r.Header.Get("Idempotency-Key"))
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode claim body: %v", err)
			}
			if len(body) != 3 || string(body["before"]) != `"2026-07-16T12:00:00.000000123Z"` ||
				string(body["claim_until"]) != `"2026-07-16T12:01:00.000000123Z"` || string(body["limit"]) != "7" {
				t.Errorf("claim body = %s", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"occurrences": []AutomationCronOccurrence{{
					WorkspaceKey: "WS", BindingID: "cron-nightly", RouteKey: "cron.nightly",
					OccurrenceID: "cron:cron-nightly:1784203140", OccurredAt: occurredAt,
				}},
				"count": 1,
			})
		case "/api/v1/WS/automation/cron/cron:cron-nightly:1784203140/complete":
			if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != "" {
				t.Errorf("completion request = %s key=%q", r.Method, r.Header.Get("Idempotency-Key"))
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode completion body: %v", err)
			}
			if len(body) != 3 || string(body["binding_id"]) != `"cron-nightly"` ||
				string(body["status"]) != `"dropped"` || string(body["error_class"]) != `"policy_drop"` {
				t.Errorf("completion body = %s", body)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	occurrences, err := client.Automation().ClaimDueCron(t.Context(), AutomationCronClaim{
		WorkspaceKey: "WS", IdempotencyKey: "automation-cron:key",
		Before: before, ClaimUntil: claimUntil, Limit: 7,
	})
	if err != nil || len(occurrences) != 1 || occurrences[0].WorkspaceKey != "WS" ||
		occurrences[0].OccurrenceID != "cron:cron-nightly:1784203140" || !occurrences[0].OccurredAt.Equal(occurredAt) {
		t.Fatalf("ClaimDueCron = %+v, %v", occurrences, err)
	}
	if err := client.Automation().CompleteCron(t.Context(), AutomationCronCompletion{
		WorkspaceKey: "WS", BindingID: "cron-nightly", OccurrenceID: occurrences[0].OccurrenceID,
		Status: AutomationCronCompletionDropped, ErrorClass: "policy_drop",
	}); err != nil {
		t.Fatalf("CompleteCron: %v", err)
	}
}

func TestAutomationTransportCronFailsClosedOnMalformedIdentityAndResponse(t *testing.T) {
	before := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	validClaim := AutomationCronClaim{
		WorkspaceKey: "WS", IdempotencyKey: "cron-key", Before: before, ClaimUntil: before.Add(time.Minute), Limit: 2,
	}
	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "count mismatch", body: map[string]any{"occurrences": []AutomationCronOccurrence{}, "count": 1}},
		{name: "wrong workspace", body: map[string]any{"occurrences": []AutomationCronOccurrence{{
			WorkspaceKey: "OTHER", BindingID: "cron-a", OccurrenceID: "cron:cron-a:1", OccurredAt: before,
		}}, "count": 1}},
		{name: "malformed occurrence", body: map[string]any{"occurrences": []AutomationCronOccurrence{{
			WorkspaceKey: "WS", BindingID: "cron-a", OccurrenceID: "external:a:1", OccurredAt: before,
		}}, "count": 1}},
		{name: "duplicate occurrence", body: map[string]any{"occurrences": []AutomationCronOccurrence{
			{WorkspaceKey: "WS", BindingID: "cron-a", OccurrenceID: "cron:cron-a:1", OccurredAt: before},
			{WorkspaceKey: "WS", BindingID: "cron-a", OccurrenceID: "cron:cron-a:1", OccurredAt: before},
		}, "count": 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(test.body)
			}))
			defer server.Close()
			client, _ := New(Config{BaseURL: server.URL})
			if _, err := client.Automation().ClaimDueCron(t.Context(), validClaim); !errors.Is(err, ErrAutomationInvalid) {
				t.Fatalf("ClaimDueCron error = %v, want invalid", err)
			}
		})
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	invalidClaims := []AutomationCronClaim{
		{},
		{WorkspaceKey: " WS ", IdempotencyKey: "cron-key", Before: before, ClaimUntil: before.Add(time.Minute), Limit: 1},
		{WorkspaceKey: "ws", IdempotencyKey: "cron-key", Before: before, ClaimUntil: before.Add(time.Minute), Limit: 1},
		{WorkspaceKey: "WS-", IdempotencyKey: "cron-key", Before: before, ClaimUntil: before.Add(time.Minute), Limit: 1},
		{WorkspaceKey: "WS", IdempotencyKey: "bad key", Before: before, ClaimUntil: before.Add(time.Minute), Limit: 1},
		{WorkspaceKey: "WS", IdempotencyKey: "cron-key", Before: before, ClaimUntil: before, Limit: 1},
	}
	for _, claim := range invalidClaims {
		if _, err := client.Automation().ClaimDueCron(t.Context(), claim); !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("invalid claim error = %v", err)
		}
	}
	if err := client.Automation().CompleteCron(t.Context(), AutomationCronCompletion{
		WorkspaceKey: "WS", BindingID: "cron-a", OccurrenceID: "external:a:1",
		Status: AutomationCronCompletionAdmitted,
	}); !errors.Is(err, ErrAutomationInvalid) {
		t.Fatalf("invalid completion error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("malformed cron commands reached HTTP: %d", requests.Load())
	}
}

func TestAutomationTransportBindingDispatchWireContractAndFailClosed(t *testing.T) {
	payload := json.RawMessage("{ \n  \"z\": 2, \"a\": 1\n}")
	dispatch := AutomationBindingDispatch{
		WorkspaceKey: "WS", BindingID: "binding-1", IdempotencyKey: "manual-dispatch-1",
		EffectiveVersion: AutomationCatalogGuard{
			BindingID: "binding-1", DriverID: "driver-1", VersionID: "version-1", DriverRevision: 7,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		},
		SubjectRef: "repo:main", EpicID: "EPIC-1", ActorRef: "operator:alice", RawPayloadRef: "request:1",
		Payload: payload, SubjectAttrs: map[string]string{"branch": "main"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/v1/WS/automation/bindings/binding-1/dispatch" ||
			r.Header.Get("Idempotency-Key") != "manual-dispatch-1" {
			t.Errorf("request = %s %s key=%q", r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"))
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if len(body) != 7 || string(body["payload_base64"]) != `"eyAKICAieiI6IDIsICJhIjogMQp9"` ||
			string(body["subject_ref"]) != `"repo:main"` || string(body["epic_id"]) != `"EPIC-1"` ||
			string(body["actor_ref"]) != `"operator:alice"` || string(body["raw_payload_ref"]) != `"request:1"` {
			t.Errorf("body = %s", body)
		}
		var guard AutomationCatalogGuard
		if err := json.Unmarshal(body["effective_version"], &guard); err != nil || guard != dispatch.EffectiveVersion {
			t.Errorf("effective version = %+v, %v", guard, err)
		}
		_ = json.NewEncoder(w).Encode(AutomationBindingDispatchResult{
			DriverRun: &domain.DriverRun{
				WorkspaceKey: "WS", RunID: "run-manual-1", DriverID: "driver-1", DriverVersionID: "version-1",
				Entrypoint: "run", SourceKind: "binding-run", SourceRef: "route.manual", TriggerBindingID: "binding-1",
				AgentServiceID: "agent-1", SubjectKey: "repo/main", Status: domain.DriverRunQueued, Payload: payload,
			},
			Outcome: AutomationDeliveryDispatchRun,
		})
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	result, err := client.Automation().DispatchAutomationBinding(t.Context(), dispatch)
	var wantPayload, gotPayload bytes.Buffer
	if compactErr := json.Compact(&wantPayload, payload); compactErr != nil {
		t.Fatal(compactErr)
	}
	if result != nil && result.DriverRun != nil {
		if compactErr := json.Compact(&gotPayload, result.DriverRun.Payload); compactErr != nil {
			t.Fatal(compactErr)
		}
	}
	if err != nil || result == nil || result.DriverRun == nil || result.DriverRun.RunID != "run-manual-1" ||
		result.DriverRun.SubjectKey != "repo/main" || !bytes.Equal(gotPayload.Bytes(), wantPayload.Bytes()) ||
		result.Outcome != AutomationDeliveryDispatchRun || !bytes.Contains(result.DriverRunSnapshot, []byte(`"subject_key":"repo/main"`)) {
		t.Fatalf("DispatchAutomationBinding = %+v, %v", result, err)
	}

	for _, test := range []struct {
		name   string
		result AutomationBindingDispatchResult
	}{
		{name: "wrong workspace", result: AutomationBindingDispatchResult{
			DriverRun: &domain.DriverRun{WorkspaceKey: "OTHER", RunID: "run-1", DriverID: "driver-1", DriverVersionID: "version-1", TriggerBindingID: "binding-1"},
			Outcome:   AutomationDeliveryDispatchRun,
		}},
		{name: "wrong binding", result: AutomationBindingDispatchResult{
			DriverRun: &domain.DriverRun{WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver-1", DriverVersionID: "version-1", TriggerBindingID: "binding-2"},
			Outcome:   AutomationDeliveryDispatchRun,
		}},
		{name: "malformed busy", result: AutomationBindingDispatchResult{
			DriverRun: &domain.DriverRun{WorkspaceKey: "WS", RunID: "run-1"}, Outcome: AutomationDeliveryDispatchBusy, BusyRunID: "busy-1",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(test.result)
			}))
			defer server.Close()
			client, _ := New(Config{BaseURL: server.URL})
			if _, err := client.Automation().DispatchAutomationBinding(t.Context(), dispatch); !errors.Is(err, ErrAutomationInvalid) {
				t.Fatalf("error = %v, want invalid", err)
			}
		})
	}

	var requests atomic.Int32
	invalidServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer invalidServer.Close()
	invalidClient, _ := New(Config{BaseURL: invalidServer.URL})
	invalid := dispatch
	invalid.EffectiveVersion.BindingID = "other-binding"
	if _, err := invalidClient.Automation().DispatchAutomationBinding(t.Context(), invalid); !errors.Is(err, ErrAutomationInvalid) {
		t.Fatalf("invalid guard error = %v", err)
	}
	invalid = dispatch
	invalid.Payload = json.RawMessage(`{"unterminated"`)
	if _, err := invalidClient.Automation().DispatchAutomationBinding(t.Context(), invalid); !errors.Is(err, ErrAutomationInvalid) {
		t.Fatalf("invalid payload error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid binding commands reached HTTP: %d", requests.Load())
	}
}

func TestAutomationTransportBindingReplayOnlyOmitsCatalogAndPreservesUnknownRunFields(t *testing.T) {
	payload := json.RawMessage(`{"manual":true}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["replay_only"]) != "true" || body["effective_version"] != nil ||
			string(body["payload_base64"]) != `"eyJtYW51YWwiOnRydWV9"` || r.Header.Get("Idempotency-Key") != "manual-replay-1" {
			t.Fatalf("replay request body=%s key=%q", body, r.Header.Get("Idempotency-Key"))
		}
		_, _ = w.Write([]byte(`{
			"driver_run":{"workspace_key":"WS","run_id":"run-replayed","driver_id":"driver-1","driver_version_id":"version-1","status":"queued","trigger_binding_id":"binding-1","future_field":"preserved"},
			"outcome":"run","run_reused":false,"replayed":true
		}`))
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	result, err := client.Automation().DispatchAutomationBinding(t.Context(), AutomationBindingDispatch{
		WorkspaceKey: "WS", BindingID: "binding-1", IdempotencyKey: "manual-replay-1", ReplayOnly: true,
		SubjectRef: "issue-1", ActorRef: "operator:alice", Payload: payload,
	})
	if err != nil || result == nil || !result.Replayed || result.DriverRun == nil || result.DriverRun.RunID != "run-replayed" ||
		!bytes.Contains(result.DriverRunSnapshot, []byte(`"future_field":"preserved"`)) {
		t.Fatalf("replay result = %#v, %v", result, err)
	}
}

func TestAutomationTransportUsesExactManagedAndUnmanagedBindingRoutes(t *testing.T) {
	createdAt := time.Date(2026, 7, 16, 12, 0, 0, 123000000, time.UTC)
	updatedAt := createdAt.Add(time.Second)
	nextUpdatedAt := updatedAt.Add(time.Microsecond)
	managed := &automation.Binding{
		WorkspaceKey: "WS", BindingID: "managed-a", RouteKey: "github.push",
		TargetAgentServiceID: "agent-service-a", CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	unmanaged := &automation.Binding{
		WorkspaceKey: "WS", BindingID: "ordinary-a", RouteKey: "github.issue",
		CreatedAt: createdAt, UpdatedAt: nextUpdatedAt,
	}
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.EscapedPath())
		switch r.URL.EscapedPath() {
		case "/api/v1/WS/automation/managed-bindings":
			var body automation.Binding
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TargetAgentServiceID != "agent-service-a" {
				t.Errorf("managed create body = %+v, %v", body, err)
			}
			_ = json.NewEncoder(w).Encode(managed)
		case "/api/v1/WS/automation/managed-bindings/managed-a/replace":
			var body struct {
				ExpectedTargetAgentServiceID string              `json:"expected_target_agent_service_id"`
				ExpectedRouteKey             string              `json:"expected_route_key"`
				ExpectedCreatedAt            time.Time           `json:"expected_created_at"`
				ExpectedUpdatedAt            time.Time           `json:"expected_updated_at"`
				Binding                      *automation.Binding `json:"binding"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
				body.ExpectedTargetAgentServiceID != "agent-service-a" || body.ExpectedRouteKey != "github.push" ||
				!body.ExpectedCreatedAt.Equal(createdAt) || !body.ExpectedUpdatedAt.Equal(updatedAt) ||
				body.Binding == nil || !body.Binding.UpdatedAt.Equal(nextUpdatedAt) {
				t.Errorf("managed replace body = %+v, %v", body, err)
			}
			_ = json.NewEncoder(w).Encode(body.Binding)
		case "/api/v1/WS/automation/managed-bindings/managed-a/delete":
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 4 ||
				string(body["expected_target_agent_service_id"]) != `"agent-service-a"` ||
				string(body["expected_route_key"]) != `"github.push"` {
				t.Errorf("managed delete body = %s, %v", body, err)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/WS/automation/unmanaged-bindings/ordinary-a/replace":
			var body struct {
				ExpectedRouteKey  string              `json:"expected_route_key"`
				ExpectedCreatedAt time.Time           `json:"expected_created_at"`
				ExpectedUpdatedAt time.Time           `json:"expected_updated_at"`
				Binding           *automation.Binding `json:"binding"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedRouteKey != "github.issue" ||
				!body.ExpectedCreatedAt.Equal(createdAt) || !body.ExpectedUpdatedAt.Equal(updatedAt) ||
				body.Binding == nil || body.Binding.TargetAgentServiceID != "" || !body.Binding.UpdatedAt.Equal(nextUpdatedAt) {
				t.Errorf("unmanaged replace body = %+v, %v", body, err)
			}
			_ = json.NewEncoder(w).Encode(body.Binding)
		case "/api/v1/WS/automation/unmanaged-bindings/ordinary-a/delete":
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 3 ||
				string(body["expected_route_key"]) != `"github.issue"` {
				t.Errorf("unmanaged delete body = %s, %v", body, err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Automation()
	if _, err := transport.CreateManagedBinding(t.Context(), managed); err != nil {
		t.Fatalf("CreateManagedBinding: %v", err)
	}
	managedReplacement := *managed
	managedReplacement.UpdatedAt = nextUpdatedAt
	if _, err := transport.ReplaceManagedBinding(t.Context(), AutomationManagedBindingReplacement{
		Expected: AutomationManagedBindingSnapshot{
			WorkspaceKey: "WS", BindingID: "managed-a", ExpectedTargetAgentServiceID: "agent-service-a",
			ExpectedRouteKey: "github.push", ExpectedCreatedAt: createdAt, ExpectedUpdatedAt: updatedAt,
		},
		Binding: &managedReplacement,
	}); err != nil {
		t.Fatalf("ReplaceManagedBinding: %v", err)
	}
	if err := transport.DeleteManagedBindingIfUnchanged(t.Context(), AutomationManagedBindingSnapshot{
		WorkspaceKey: "WS", BindingID: "managed-a", ExpectedTargetAgentServiceID: "agent-service-a",
		ExpectedRouteKey: "github.push", ExpectedCreatedAt: createdAt, ExpectedUpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("DeleteManagedBindingIfUnchanged: %v", err)
	}
	if _, err := transport.ReplaceUnmanagedBinding(t.Context(), AutomationUnmanagedBindingReplacement{
		Expected: AutomationUnmanagedBindingSnapshot{
			WorkspaceKey: "WS", BindingID: "ordinary-a", ExpectedRouteKey: "github.issue",
			ExpectedCreatedAt: createdAt, ExpectedUpdatedAt: updatedAt,
		},
		Binding: unmanaged,
	}); err != nil {
		t.Fatalf("ReplaceUnmanagedBinding: %v", err)
	}
	if err := transport.DeleteUnmanagedBindingIfUnchanged(t.Context(), AutomationUnmanagedBindingSnapshot{
		WorkspaceKey: "WS", BindingID: "ordinary-a", ExpectedRouteKey: "github.issue",
		ExpectedCreatedAt: createdAt, ExpectedUpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("DeleteUnmanagedBindingIfUnchanged: %v", err)
	}
	want := []string{
		"POST /api/v1/WS/automation/managed-bindings",
		"POST /api/v1/WS/automation/managed-bindings/managed-a/replace",
		"POST /api/v1/WS/automation/managed-bindings/managed-a/delete",
		"POST /api/v1/WS/automation/unmanaged-bindings/ordinary-a/replace",
		"POST /api/v1/WS/automation/unmanaged-bindings/ordinary-a/delete",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}

func TestAutomationTransportMapsMachineReadableErrors(t *testing.T) {
	tests := []struct {
		code   string
		status int
		want   error
	}{
		{code: "automation_route_not_found", status: http.StatusNotFound, want: ErrAutomationRouteNotFound},
		{code: "automation_parent_run_not_found", status: http.StatusNotFound, want: ErrAutomationParentRunNotFound},
		{code: "automation_idempotency_conflict", status: http.StatusConflict, want: ErrAutomationIdempotencyConflict},
		{code: "automation_binding_snapshot_conflict", status: http.StatusConflict, want: ErrAutomationBindingSnapshotConflict},
		{code: "automation_catalog_snapshot_conflict", status: http.StatusConflict, want: ErrAutomationCatalogSnapshotConflict},
		{code: "automation_delivery_transition_conflict", status: http.StatusConflict, want: ErrAutomationDeliveryTransitionConflict},
		{code: "automation_delivery_not_dispatchable", status: http.StatusConflict, want: ErrAutomationDeliveryNotDispatchable},
		{code: "automation_hop_depth_exceeded", status: http.StatusUnprocessableEntity, want: ErrAutomationHopDepthExceeded},
		{code: "automation_catalog_version_unavailable", status: http.StatusUnprocessableEntity, want: ErrAutomationCatalogUnavailable},
		{code: "automation_fanout_limit_exceeded", status: http.StatusUnprocessableEntity, want: ErrAutomationFanoutLimitExceeded},
		{code: "automation_payload_digest_mismatch", status: http.StatusBadRequest, want: ErrAutomationPayloadDigestMismatch},
		{code: "automation_binding_not_found", status: http.StatusNotFound, want: ErrAutomationBindingNotFound},
		{code: "automation_binding_dispatch_replay_not_found", status: http.StatusNotFound, want: ErrAutomationBindingDispatchReplayNotFound},
		{code: "automation_admission_unavailable", status: http.StatusServiceUnavailable, want: ErrAutomationAdmissionUnavailable},
		{code: "automation_admission_replay_not_found", status: http.StatusNotFound, want: ErrAutomationAdmissionReplayNotFound},
		{code: "automation_cron_occurrence_not_found", status: http.StatusNotFound, want: ErrAutomationCronOccurrenceNotFound},
		{code: "automation_cron_completion_conflict", status: http.StatusConflict, want: ErrAutomationCronCompletionConflict},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": test.code, "message": test.code}})
			}))
			defer server.Close()
			client, _ := New(Config{BaseURL: server.URL})
			_, err := client.Automation().ReserveEvent(context.Background(), AutomationEventReservation{
				WorkspaceKey: "WS", RouteKey: "route", IdempotencyKey: "idem", Origin: automation.EventOriginExternal,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
