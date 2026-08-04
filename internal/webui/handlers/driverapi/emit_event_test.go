package driverapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// seedInternalBinding adds a binding listening on the internal loopback route
// to the harness store (driver-1/version-1 already exist).
func seedInternalBinding(t *testing.T, st store.Store, routeKey string) {
	t.Helper()
	if _, err := st.TriggerBindings().Create(context.Background(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-" + routeKey, Name: "b-" + routeKey,
		SourceKind: "internal", RouteKey: routeKey,
		DriverID: "driver-1", DriverVersionID: "version-1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
}

func TestDriverAPIEmitEventDispatchesLoopback(t *testing.T) {
	h := newTestHarness(t, "")
	seedInternalBinding(t, h.store, "internal.issue.created")

	resp, decoded := h.do(t, opRequest{
		op:      "emit-event",
		headers: h.ownerHeaders(),
		body: map[string]any{
			"eventId":    "wf-emit-1",
			"eventType":  "issue.create",
			"subjectRef": "issue#42",
			"payload":    map[string]any{"issueId": "42"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if dropped, _ := decoded["dropped"].(bool); dropped {
		t.Fatalf("response = %v, want not dropped", decoded)
	}
	if decoded["routeKey"] != "internal.issue.created" || decoded["eventType"] != "issue.created" ||
		decoded["origin"] != "workflow" || decoded["hopDepth"] != float64(1) {
		t.Fatalf("response = %v, want workflow loopback at depth 1 on internal.issue.created", decoded)
	}
	deliveries, _ := decoded["deliveries"].([]any)
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %v, want one leg", decoded["deliveries"])
	}
	leg, _ := deliveries[0].(map[string]any)
	if leg["status"] != string(domain.TriggerDeliveryDispatched) || leg["driverRunId"] == "" {
		t.Fatalf("delivery leg = %v, want dispatched with run", leg)
	}

	// The persisted event carries the loopback identity (idempotency key
	// derived from the workflow's eventId, signature_status internal).
	events, err := h.store.TriggerEvents().List(context.Background(), "WS", store.TriggerEventFilter{SourceKind: "internal"})
	if err != nil || len(events) != 1 {
		t.Fatalf("List internal events = %v, %v; want exactly one", events, err)
	}
	if events[0].IdempotencyKey != "internal:WS:wf-emit-1" || events[0].SignatureStatus != "internal" {
		t.Fatalf("persisted event = %+v, want loopback identity fields", events[0])
	}
}

func TestDriverAPIEmitEventRequiresRunOwnership(t *testing.T) {
	h := newTestHarness(t, "")
	seedInternalBinding(t, h.store, "internal.issue.created")

	headers := h.ownerHeaders()
	headers[HeaderDriverFencingToken] = "999999"
	resp, decoded := h.do(t, opRequest{
		op:      "emit-event",
		headers: headers,
		body:    map[string]any{"eventId": "wf-emit-2", "eventType": "issue.created"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}
}

func TestDriverAPIEmitEventValidatesParams(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op:      "emit-event",
		headers: h.ownerHeaders(),
		body:    map[string]any{"eventType": "issue.created"}, // no eventId
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d (%v), want 400", resp.StatusCode, decoded)
	}
}
