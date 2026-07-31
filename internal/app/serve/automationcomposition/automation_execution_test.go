package automationcomposition

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type automationRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn automationRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type automationRunReaderStub struct {
	store.DriverRunStore
	run   *domain.DriverRun
	err   error
	calls int
}

func (stub *automationRunReaderStub) Get(context.Context, string, string) (*domain.DriverRun, error) {
	stub.calls++
	return stub.run, stub.err
}

func issueAutomationExecutionAuthority(t *testing.T, issuer *authority.Issuer, workspace, runID string, action authority.Action, owner authority.ExecutionOwner) authority.ExecutionAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: runID, Class: authority.ClassExecution, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	value, err := issuer.IssueExecutionForOwner(principal, workspace, action, owner)
	if err != nil {
		t.Fatalf("IssueExecution: %v", err)
	}
	return value
}

func TestAutomationExecutionPortReloadsFencedEmissionContext(t *testing.T) {
	runs := &automationRunReaderStub{run: &domain.DriverRun{
		WorkspaceKey: "TEST", RunID: "run-1", Status: domain.DriverRunRunning,
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: 42,
		SourceRef: "event-parent", Payload: json.RawMessage(`{"epicId":"EPIC-9"}`),
	}}
	port := &automationExecutionPort{runs: runs}
	auth := issueAutomationExecutionAuthority(t, authority.NewIssuer(), "TEST", "run-1", automation.ActionAdmitEvent,
		authority.ExecutionOwner{ResourceKind: authority.ExecutionResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 42})
	contextValue, err := port.EmissionContext(context.Background(), auth)
	if err != nil {
		t.Fatalf("EmissionContext: %v", err)
	}
	if contextValue.WorkspaceKey != "TEST" || contextValue.RunID != "run-1" ||
		contextValue.ParentEventID != "event-parent" || contextValue.ActorRef != "driver-run:run-1" ||
		contextValue.EpicID != "EPIC-9" || contextValue.NodeID != "node-1" || contextValue.LeaseID != "lease-1" || contextValue.FencingToken != 42 {
		t.Fatalf("emission context = %#v", contextValue)
	}
	if runs.calls != 1 {
		t.Fatalf("run reads = %d, want 1", runs.calls)
	}
}

func TestAutomationExecutionPortRejectsWrongActionAndStaleRun(t *testing.T) {
	issuer := authority.NewIssuer()
	runs := &automationRunReaderStub{run: &domain.DriverRun{
		WorkspaceKey: "TEST", RunID: "run-1", Status: domain.DriverRunRunning,
		NodeID: "node", LeaseID: "lease", FencingToken: 1,
	}}
	port := &automationExecutionPort{runs: runs}
	owner := authority.ExecutionOwner{ResourceKind: authority.ExecutionResourceDriverRun, ResourceID: "run-1", NodeID: "node", LeaseID: "lease", FencingToken: 1}
	wrong := issueAutomationExecutionAuthority(t, issuer, "TEST", "run-1", automation.ActionSweepCron, owner)
	if _, err := port.EmissionContext(context.Background(), wrong); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action error = %v, want admission denial", err)
	}
	if runs.calls != 0 {
		t.Fatalf("wrong action performed %d run reads", runs.calls)
	}

	valid := issueAutomationExecutionAuthority(t, issuer, "TEST", "run-1", automation.ActionAdmitEvent, owner)
	for _, mutate := range []func(*domain.DriverRun){
		func(run *domain.DriverRun) { run.Status = domain.DriverRunQueued },
		func(run *domain.DriverRun) { run.NodeID = "" },
		func(run *domain.DriverRun) { run.LeaseID = "" },
		func(run *domain.DriverRun) { run.FencingToken = 0 },
		func(run *domain.DriverRun) { run.NodeID = "node-handoff" },
		func(run *domain.DriverRun) { run.LeaseID = "lease-handoff" },
		func(run *domain.DriverRun) { run.FencingToken = 2 },
		func(run *domain.DriverRun) { run.WorkspaceKey = "OTHER" },
		func(run *domain.DriverRun) { run.RunID = "other-run" },
	} {
		candidate := *runs.run
		mutate(&candidate)
		runs.run = &candidate
		if _, err := port.EmissionContext(context.Background(), valid); !errors.Is(err, automation.ErrInvalidPersistedState) {
			t.Fatalf("invalid run %+v error = %v, want invalid persisted state", candidate, err)
		}
		runs.run = &domain.DriverRun{
			WorkspaceKey: "TEST", RunID: "run-1", Status: domain.DriverRunRunning,
			NodeID: "node", LeaseID: "lease", FencingToken: 1,
		}
	}
}

func TestAutomationExecutionPortDispatchUsesOnlyAtomicIntent(t *testing.T) {
	var calls int
	port := &automationExecutionPort{dispatch: func(_ context.Context, request automation.ExecutionDispatchRequest) (*automation.ExecutionDispatchResult, error) {
		calls++
		if calls == 1 && request.DeliveryID != "delivery-1" {
			t.Fatalf("delivery id = %q", request.DeliveryID)
		}
		if calls == 2 && request.DeliveryID != "" {
			t.Fatalf("manual delivery id = %q, want empty", request.DeliveryID)
		}
		return &automation.ExecutionDispatchResult{RunID: "run-1"}, nil
	}}
	result, err := port.Dispatch(context.Background(), automation.ExecutionDispatchRequest{DeliveryID: "delivery-1"})
	if err != nil || result == nil || result.RunID != "run-1" || calls != 1 {
		t.Fatalf("atomic dispatch result=%#v calls=%d err=%v", result, calls, err)
	}
	manual, err := port.Dispatch(context.Background(), automation.ExecutionDispatchRequest{})
	if err != nil || manual == nil || manual.RunID != "run-1" {
		t.Fatalf("manual atomic dispatch = %#v, %v", manual, err)
	}
	if calls != 2 {
		t.Fatalf("atomic closure calls=%d, want 2", calls)
	}
	if got := newAutomationExecutionPort(nil, port.dispatch); got != nil {
		t.Fatalf("nil state execution port = %#v, want nil", got)
	}
}

func TestAutomationFleetManualBindingDispatchRetriesLostResponseWithoutGenericCreate(t *testing.T) {
	var calls int
	var bodies [][]byte
	client, err := infrafleetdb.New(infrafleetdb.Config{
		BaseURL: "http://fleet.test",
		HTTPClient: &http.Client{Transport: automationRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatalf("read request body: %v", readErr)
			}
			bodies = append(bodies, body)
			if request.URL.Path != "/api/v1/WS/automation/bindings/binding-1/dispatch" ||
				request.Header.Get("Idempotency-Key") != "manual-key" {
				t.Fatalf("manual request = %s key=%q", request.URL.Path, request.Header.Get("Idempotency-Key"))
			}
			if bytes.Contains(body, []byte(`"concurrency_policy"`)) || bytes.Contains(body, []byte(`"target_entrypoint"`)) {
				t.Fatalf("manual request trusted caller policy/target: %s", body)
			}
			if calls == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(bytes.NewBufferString(`{
					"driver_run":{"workspace_key":"WS","run_id":"run-manual-1","driver_id":"driver-1","driver_version_id":"version-1","entrypoint":"run","source_kind":"binding-run","source_ref":"route.manual","status":"queued","trigger_binding_id":"binding-1","payload":{"manual":true}},
					"outcome":"run","run_reused":false,"replayed":true
				}`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("new fleet client: %v", err)
	}
	dispatch := newAutomationFleetExecutionDispatch(client)
	result, err := dispatch(context.Background(), automation.ExecutionDispatchRequest{
		WorkspaceKey: "WS", IdempotencyKey: "manual-key", TriggerBindingID: "binding-1",
		DriverID: "driver-1", DriverVersionID: "version-1", DriverRevision: 7,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		SubjectRef: "repo:main", SourceRef: "route.manual", ActorRef: "operator:alice",
		Payload: json.RawMessage(`{"manual":true}`), SubjectAttrs: map[string]string{"branch": "main"},
	})
	if err != nil {
		t.Fatalf("manual dispatch: %v", err)
	}
	if calls != 2 || len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("manual attempts=%d bodies=%q; want two identical atomic requests", calls, bodies)
	}
	if result == nil || result.RunID != "run-manual-1" || !result.Replayed || result.Busy || result.Delivery != nil ||
		!bytes.Contains(result.RunSnapshot, []byte(`"payload":{"manual":true}`)) {
		t.Fatalf("mapped manual dispatch = %#v", result)
	}
}

func TestAutomationFleetManualReplayMissIsStableControlFlow(t *testing.T) {
	var calls int
	client, err := infrafleetdb.New(infrafleetdb.Config{
		BaseURL: "http://fleet.test",
		HTTPClient: &http.Client{Transport: automationRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Contains(body, []byte(`"replay_only":true`)) || bytes.Contains(body, []byte(`"effective_version"`)) {
				t.Fatalf("replay probe body = %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusNotFound, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"error":{"code":"automation_binding_dispatch_replay_not_found","message":"missing"}}`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = newAutomationFleetExecutionDispatch(client)(context.Background(), automation.ExecutionDispatchRequest{
		WorkspaceKey: "WS", TriggerBindingID: "binding-1", IdempotencyKey: "manual-key", ReplayOnly: true,
	})
	if !errors.Is(err, automation.ErrDispatchReplayNotFound) ||
		!errors.Is(err, infrafleetdb.ErrAutomationBindingDispatchReplayNotFound) || calls != 1 {
		t.Fatalf("replay miss err=%v calls=%d", err, calls)
	}
}

func TestAutomationFleetExecutionDispatchRetriesLostResponseWithSameIntent(t *testing.T) {
	var calls int
	var bodies [][]byte
	client, err := infrafleetdb.New(infrafleetdb.Config{
		BaseURL: "http://fleet.test",
		HTTPClient: &http.Client{Transport: automationRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatalf("read request body: %v", readErr)
			}
			bodies = append(bodies, body)
			if request.URL.Path != "/api/v1/WS/automation/deliveries/delivery-1/dispatch" ||
				request.Header.Get("Idempotency-Key") != "event-key#binding-1" {
				t.Fatalf("dispatch request = %s idempotency=%q", request.URL.Path, request.Header.Get("Idempotency-Key"))
			}
			if calls == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(bytes.NewBufferString(`{
					"event":{"workspace_key":"WS","event_id":"event-1","source_kind":"github","event_type":"issue.opened","occurred_at":"2026-07-16T12:00:00Z","received_at":"2026-07-16T12:00:00Z","payload_base64":"eyJ4IjoxfQ=="},
					"delivery":{"workspace_key":"WS","delivery_id":"delivery-1","trigger_event_id":"event-1","trigger_binding_id":"binding-1","status":"dispatched","attempt":1,"driver_run_id":"run-1"},
					"driver_run":{"workspace_key":"WS","run_id":"run-1"},"outcome":"run","run_reused":false,"replayed":true
				}`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("new fleet client: %v", err)
	}
	dispatch := newAutomationFleetExecutionDispatch(client)
	result, err := dispatch(context.Background(), automation.ExecutionDispatchRequest{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", IdempotencyKey: "event-key#binding-1",
		ExpectedDeliveryStatus: automation.DeliveryAccepted, ExpectedDeliveryAttempt: 1,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if calls != 2 || len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("dispatch attempts=%d bodies=%q; want two identical requests", calls, bodies)
	}
	if result == nil || result.RunID != "run-1" || !result.Replayed || result.Busy ||
		result.Delivery == nil || result.Delivery.DeliveryID != "delivery-1" {
		t.Fatalf("mapped dispatch = %#v", result)
	}
}

func TestAutomationFleetExecutionDispatchDoesNotRetryStableConflict(t *testing.T) {
	var calls int
	client, err := infrafleetdb.New(infrafleetdb.Config{
		BaseURL: "http://fleet.test",
		HTTPClient: &http.Client{Transport: automationRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusConflict,
				Header:     make(http.Header),
				Body: io.NopCloser(bytes.NewBufferString(
					`{"error":{"code":"automation_delivery_not_dispatchable","message":"stale delivery"}}`,
				)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("new fleet client: %v", err)
	}
	_, err = newAutomationFleetExecutionDispatch(client)(context.Background(), automation.ExecutionDispatchRequest{
		WorkspaceKey: "WS", DeliveryID: "delivery-1", IdempotencyKey: "key",
		ExpectedDeliveryStatus: automation.DeliveryAccepted, ExpectedDeliveryAttempt: 1,
	})
	if !errors.Is(err, automation.ErrConflict) || !errors.Is(err, infrafleetdb.ErrAutomationDeliveryNotDispatchable) {
		t.Fatalf("dispatch error = %v, want stable Automation conflict", err)
	}
	if calls != 1 {
		t.Fatalf("stable conflict attempts = %d, want 1", calls)
	}
}
