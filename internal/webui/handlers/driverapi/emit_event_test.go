package driverapi

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type driverEventAuthorityProviderFunc func(context.Context, workfloweventing.VerifiedRun) (authority.ExecutionAuthority, error)

func (function driverEventAuthorityProviderFunc) AuthorityForVerifiedRun(ctx context.Context, parent workfloweventing.VerifiedRun) (authority.ExecutionAuthority, error) {
	return function(ctx, parent)
}

type driverEventAdmissionFunc func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error)

func (function driverEventAdmissionFunc) AdmitEvent(ctx context.Context, eventAuthority automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	return function(ctx, eventAuthority, command)
}

type recordingWorkflowEventAwaits struct {
	calls     int
	workspace string
	event     trigger.AwaitDispatchEvent
	err       error
}

func (dispatcher *recordingWorkflowEventAwaits) Dispatch(_ context.Context, workspace string, event trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error) {
	dispatcher.calls++
	dispatcher.workspace = workspace
	dispatcher.event = event
	return &trigger.AwaitDispatchResult{}, dispatcher.err
}

func mustWorkflowEventing(t *testing.T, provider workfloweventing.ExecutionAuthorityProvider, admission automation.EventAdmission) *workfloweventing.Workflow {
	t.Helper()
	workflow, err := workfloweventing.New(provider, admission)
	if err != nil {
		t.Fatalf("new workflow eventing: %v", err)
	}
	return workflow
}

func assertJSONKeys(t *testing.T, object map[string]any, want ...string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v (object=%v)", got, want, object)
	}
}

func assertSameJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("unmarshal got JSON %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal want JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

// seedInternalBinding adds a binding listening on the internal loopback route
// to the harness store (driver-1/version-1 already exist).
func seedInternalBinding(t *testing.T, st store.Store, routeKey string) {
	t.Helper()
	if _, err := st.TriggerBindings().Create(context.Background(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-" + routeKey, Name: "b-" + routeKey,
		SourceKind: "internal", RouteKey: routeKey,
		DriverID: "driver-1", DriverVersionID: "version-1", TargetEntrypoint: "run",
		ConcurrencyPolicy: automation.ConcurrencyAllow, Enabled: true,
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
			"actorRef":   "driver-run:attacker",
			"epicId":     "ATTACKER-EPIC",
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
	if leg["status"] != string(automation.DeliveryDispatched) || leg["driverRunId"] == "" {
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

func TestDriverAPIEmitEventUsesNamedWorkflowAndPreservesCamelCaseWire(t *testing.T) {
	h := newTestHarness(t, "")
	var (
		providerCalls  int
		admissionCalls int
		gotParent      workfloweventing.VerifiedRun
		gotCommand     automation.AdmitEventCommand
	)
	h.module.workflowEventing = mustWorkflowEventing(t,
		driverEventAuthorityProviderFunc(func(_ context.Context, parent workfloweventing.VerifiedRun) (authority.ExecutionAuthority, error) {
			providerCalls++
			gotParent = parent
			return authority.ExecutionAuthority{}, nil
		}),
		driverEventAdmissionFunc(func(_ context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			admissionCalls++
			gotCommand = command
			return &automation.AdmissionResult{
				EventType: "issue.created", RouteKey: "internal.issue.created",
				Origin: automation.EventOriginWorkflow, HopDepth: 7,
				Event: &automation.Event{
					SourceEventID: command.SourceEventID, EventType: "issue.created",
					SubjectRef: command.SubjectRef, ActorRef: driverpkg.DriverRunActor("run-1"),
				},
				Deliveries: []*automation.Delivery{
					{DeliveryID: "delivery-1", TriggerBindingID: "binding-1", DriverRunID: "child-run", Status: automation.DeliveryDispatched},
					{DeliveryID: "delivery-2", TriggerBindingID: "binding-2", Status: automation.DeliveryRejected, RejectionReason: "actor_filter"},
					nil,
				},
			}, nil
		}),
	)
	awaits := &recordingWorkflowEventAwaits{err: errors.New("best-effort await failure")}
	h.module.eventAwaits = awaits

	resp, decoded := h.do(t, opRequest{
		op:      "emit-event",
		headers: h.ownerHeaders(),
		body: map[string]any{
			"eventId": "wf-emit-authority", "eventType": "issue.create",
			"subjectRef": "issue#42", "actorRef": "driver-run:attacker", "epicId": "ATTACKER-EPIC",
			"payload": map[string]any{"issueId": "42"}, "subjectAttrs": map[string]string{"kind": "issue"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	assertJSONKeys(t, decoded, "deliveries", "dropped", "eventType", "hopDepth", "origin", "routeKey")
	if decoded["dropped"] != false || decoded["eventType"] != "issue.created" ||
		decoded["routeKey"] != "internal.issue.created" || decoded["origin"] != "workflow" || decoded["hopDepth"] != float64(7) {
		t.Fatalf("response = %v, want mapped admission identity", decoded)
	}
	deliveries, ok := decoded["deliveries"].([]any)
	if !ok || len(deliveries) != 2 {
		t.Fatalf("deliveries = %v, want two non-nil legs", decoded["deliveries"])
	}
	first, _ := deliveries[0].(map[string]any)
	second, _ := deliveries[1].(map[string]any)
	assertJSONKeys(t, first, "deliveryId", "driverRunId", "status", "triggerBindingId")
	assertJSONKeys(t, second, "deliveryId", "rejectionReason", "status", "triggerBindingId")
	if first["deliveryId"] != "delivery-1" || first["triggerBindingId"] != "binding-1" || first["driverRunId"] != "child-run" || first["status"] != "dispatched" {
		t.Fatalf("first delivery = %v", first)
	}
	if second["deliveryId"] != "delivery-2" || second["triggerBindingId"] != "binding-2" || second["status"] != "rejected" || second["rejectionReason"] != "actor_filter" {
		t.Fatalf("second delivery = %v", second)
	}

	if providerCalls != 1 || admissionCalls != 1 || gotParent.RunID != h.runID || gotParent.Status != "running" {
		t.Fatalf("workflow calls/parent = %d/%d %+v, want one call with verified running parent", providerCalls, admissionCalls, gotParent)
	}
	if gotCommand.WorkspaceKey != "WS" || gotCommand.SourceEventID != "wf-emit-authority" || gotCommand.EventType != "issue.create" ||
		gotCommand.SubjectRef != "issue#42" || !reflect.DeepEqual(gotCommand.SubjectAttrs, map[string]string{"kind": "issue"}) {
		t.Fatalf("admission command content = %+v", gotCommand)
	}
	assertSameJSON(t, gotCommand.Payload, `{"issueId":"42"}`)
	if gotCommand.SourceKind != "" || gotCommand.SourceRef != "" || gotCommand.RouteKey != "" || gotCommand.ActorRef != "" ||
		gotCommand.ParentEventID != "" || gotCommand.EpicID != "" || !gotCommand.OccurredAt.IsZero() ||
		gotCommand.RawPayloadRef != "" || gotCommand.RawPayloadDigest != "" {
		t.Fatalf("caller provenance reached admission: %+v", gotCommand)
	}
	if awaits.calls != 1 || awaits.workspace != "WS" || awaits.event.EventID != "wf-emit-authority" ||
		awaits.event.EventType != "issue.created" || awaits.event.SubjectRef != "issue#42" ||
		awaits.event.ActorRef != driverpkg.DriverRunActor("run-1") {
		t.Fatalf("await dispatch = calls=%d workspace=%q event=%+v", awaits.calls, awaits.workspace, awaits.event)
	}
	assertSameJSON(t, awaits.event.Payload, `{"issueId":"42"}`)
}

func TestDriverAPIEmitEventDropHasNoDeliveryOrAwait(t *testing.T) {
	h := newTestHarness(t, "")
	h.module.workflowEventing = mustWorkflowEventing(t,
		driverEventAuthorityProviderFunc(func(context.Context, workfloweventing.VerifiedRun) (authority.ExecutionAuthority, error) {
			return authority.ExecutionAuthority{}, nil
		}),
		driverEventAdmissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			return &automation.AdmissionResult{
				Dropped: true, DropReason: "hop_limit", EventType: "issue.created",
				RouteKey: "internal.issue.created", Origin: automation.EventOriginWorkflow, HopDepth: 9,
				Deliveries: []*automation.Delivery{{DeliveryID: "must-not-leak", Status: automation.DeliveryDispatched}},
			}, nil
		}),
	)
	awaits := &recordingWorkflowEventAwaits{}
	h.module.eventAwaits = awaits

	resp, decoded := h.do(t, opRequest{
		op: "emit-event", headers: h.ownerHeaders(),
		body: map[string]any{"eventId": "wf-drop", "eventType": "issue.create"},
	})
	if resp.StatusCode != http.StatusOK || decoded["dropped"] != true || decoded["dropReason"] != "hop_limit" {
		t.Fatalf("response = status %d %v, want structural drop", resp.StatusCode, decoded)
	}
	assertJSONKeys(t, decoded, "deliveries", "dropReason", "dropped", "eventType", "hopDepth", "origin", "routeKey")
	if deliveries, _ := decoded["deliveries"].([]any); len(deliveries) != 0 {
		t.Fatalf("dropped deliveries = %v, want []", decoded["deliveries"])
	}
	if awaits.calls != 0 {
		t.Fatalf("await calls = %d, want 0 for drop", awaits.calls)
	}
}

func TestDriverAPIEmitEventWithoutWorkflowIsInert(t *testing.T) {
	h := newTestHarness(t, "")
	h.module.workflowEventing = nil

	resp, decoded := h.do(t, opRequest{
		op: "emit-event", headers: h.ownerHeaders(),
		body: map[string]any{"eventId": "wf-no-fallback", "eventType": "issue.create"},
	})
	if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, decoded) != "unavailable" {
		t.Fatalf("response = status %d %v, want 503 unavailable", resp.StatusCode, decoded)
	}
	events, err := h.store.TriggerEvents().List(context.Background(), "WS", store.TriggerEventFilter{SourceKind: "internal"})
	if err != nil || len(events) != 0 {
		t.Fatalf("events after inert emit = %v, %v; want none", events, err)
	}
}

func TestDriverAPIEmitEventProductionFilesHaveNoLegacyFallback(t *testing.T) {
	forbidden := map[string]bool{
		"InternalSource": true, "InternalEvent": true, "TriggerRoutes": true,
		"DispatchTriggerRoute": true, "DispatchTriggerRouteV2": true,
	}
	for _, filename := range []string{"emit_event.go", "module.go"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Clean(filename), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.Ident:
				if forbidden[value.Name] {
					t.Errorf("production %s contains legacy fallback identifier %q", filename, value.Name)
				}
			case *ast.SelectorExpr:
				if forbidden[value.Sel.Name] {
					t.Errorf("production %s contains legacy fallback selector %q", filename, value.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestDriverAPIEmitEventRequiresRunOwnership(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "foreign node", mutate: func(headers map[string]string) { headers[HeaderDriverNodeID] = "node-attacker" }},
		{name: "foreign lease", mutate: func(headers map[string]string) { headers[HeaderDriverLeaseID] = "lease-attacker" }},
		{name: "stale fence", mutate: func(headers map[string]string) { headers[HeaderDriverFencingToken] = "999999" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t, "")
			providerCalls := 0
			h.module.workflowEventing = mustWorkflowEventing(t,
				driverEventAuthorityProviderFunc(func(context.Context, workfloweventing.VerifiedRun) (authority.ExecutionAuthority, error) {
					providerCalls++
					return authority.ExecutionAuthority{}, nil
				}),
				driverEventAdmissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
					t.Fatal("admission called for unverified owner")
					return nil, nil
				}),
			)
			headers := h.ownerHeaders()
			test.mutate(headers)
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
			if providerCalls != 0 {
				t.Fatalf("authority provider calls = %d, want 0 before verified ownership", providerCalls)
			}
		})
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
