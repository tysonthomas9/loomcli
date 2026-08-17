package workfloweventing

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type authorityProviderFunc func(context.Context, VerifiedRun) (authority.ExecutionAuthority, error)

func (function authorityProviderFunc) AuthorityForVerifiedRun(ctx context.Context, parent VerifiedRun) (authority.ExecutionAuthority, error) {
	return function(ctx, parent)
}

type admissionFunc func(context.Context, authority.ExecutionAuthority, automation.WorkflowEvent) (*automation.AdmissionResult, error)

func (function admissionFunc) AdmitWorkflowEvent(ctx context.Context, auth authority.ExecutionAuthority, command automation.WorkflowEvent) (*automation.AdmissionResult, error) {
	return function(ctx, auth, command)
}

func validParent() VerifiedRun {
	return VerifiedRun{WorkspaceKey: "WS", RunID: "run-1", Status: "running", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1}
}

func validRequest() EmitRequest {
	return EmitRequest{
		WorkspaceKey: "WS", EventID: "emission-1", EventType: "issue.create",
		SubjectRef: "issue#42", Payload: json.RawMessage(`{"issueId":"42"}`),
		SubjectAttrs: map[string]string{"issue_id": "42"},
	}
}

func TestEmitDerivesAuthorityThenAdmitsOnlyEventContent(t *testing.T) {
	parent := validParent()
	request := validRequest()
	order := make([]string, 0, 2)
	var gotParent VerifiedRun
	var gotCommand automation.WorkflowEvent
	wantResult := &automation.AdmissionResult{Event: &automation.Event{EventID: "event-1"}}
	workflow, err := New(
		authorityProviderFunc(func(_ context.Context, verified VerifiedRun) (authority.ExecutionAuthority, error) {
			order = append(order, "authority")
			gotParent = verified
			return authority.ExecutionAuthority{}, nil
		}),
		admissionFunc(func(_ context.Context, _ authority.ExecutionAuthority, command automation.WorkflowEvent) (*automation.AdmissionResult, error) {
			order = append(order, "admission")
			gotCommand = command
			return wantResult, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Emit(t.Context(), parent, request)
	if err != nil || result != wantResult {
		t.Fatalf("Emit = (%+v, %v), want (%+v, nil)", result, err, wantResult)
	}
	if !reflect.DeepEqual(order, []string{"authority", "admission"}) || !reflect.DeepEqual(gotParent, parent) {
		t.Fatalf("order/parent = %v/%+v", order, gotParent)
	}
	wantCommand := automation.WorkflowEvent{
		WorkspaceKey: "WS", SourceEventID: "emission-1", EventType: "issue.create",
		SubjectRef: "issue#42", ExecutionNodeID: parent.NodeID, ExecutionLeaseID: parent.LeaseID,
		ExecutionFencingToken: parent.FencingToken, Payload: request.Payload, SubjectAttrs: request.SubjectAttrs,
	}
	if !reflect.DeepEqual(gotCommand, wantCommand) {
		t.Fatalf("admission command = %#v, want %#v", gotCommand, wantCommand)
	}
	for _, field := range []string{"SourceKind", "SourceRef", "RouteKey", "ActorRef", "ParentEventID", "EpicID", "OccurredAt", "RawPayloadRef", "RawPayloadDigest"} {
		if _, exists := reflect.TypeOf(gotCommand).FieldByName(field); exists {
			t.Errorf("WorkflowEvent exposes caller-controlled provenance field %s", field)
		}
	}
	for _, forbidden := range []string{"Origin", "HopDepth", "SignatureStatus", "IdempotencyKey", "ParentEventID", "EpicID", "RunID", "ActorRef", "Authority"} {
		if _, exists := reflect.TypeOf(EmitRequest{}).FieldByName(forbidden); exists {
			t.Errorf("EmitRequest exposes forbidden field %q", forbidden)
		}
	}
}

func TestEmitAuthorityFailureStopsAdmission(t *testing.T) {
	denied := authority.ErrAdmissionDenied
	calls := 0
	workflow, err := New(
		authorityProviderFunc(func(context.Context, VerifiedRun) (authority.ExecutionAuthority, error) {
			return authority.ExecutionAuthority{}, denied
		}),
		admissionFunc(func(context.Context, authority.ExecutionAuthority, automation.WorkflowEvent) (*automation.AdmissionResult, error) {
			calls++
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Emit(t.Context(), validParent(), validRequest()); !errors.Is(err, denied) {
		t.Fatalf("Emit error = %v, want %v", err, denied)
	}
	if calls != 0 {
		t.Fatalf("admission calls = %d, want 0", calls)
	}
}

func TestEmitPreservesAdmissionResultWithError(t *testing.T) {
	wantResult := &automation.AdmissionResult{Event: &automation.Event{EventID: "event-1"}}
	wantErr := errors.New("partial dispatch failure")
	workflow, err := New(
		authorityProviderFunc(func(context.Context, VerifiedRun) (authority.ExecutionAuthority, error) {
			return authority.ExecutionAuthority{}, nil
		}),
		admissionFunc(func(context.Context, authority.ExecutionAuthority, automation.WorkflowEvent) (*automation.AdmissionResult, error) {
			return wantResult, wantErr
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Emit(t.Context(), validParent(), validRequest())
	if result != wantResult || !errors.Is(err, wantErr) {
		t.Fatalf("Emit = (%+v, %v), want preserved result/error", result, err)
	}
}

func TestEmitFailsClosedOnInvalidCompositionAndRequest(t *testing.T) {
	noopAuthority := authorityProviderFunc(func(context.Context, VerifiedRun) (authority.ExecutionAuthority, error) {
		return authority.ExecutionAuthority{}, nil
	})
	noopAdmission := admissionFunc(func(context.Context, authority.ExecutionAuthority, automation.WorkflowEvent) (*automation.AdmissionResult, error) {
		return &automation.AdmissionResult{}, nil
	})
	if _, err := New(nil, noopAdmission); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil authority New error = %v", err)
	}
	if _, err := New(noopAuthority, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil admission New error = %v", err)
	}
	workflow, err := New(noopAuthority, noopAdmission)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		parent  VerifiedRun
		request EmitRequest
	}{
		{name: "foreign workspace", parent: validParent(), request: func() EmitRequest { value := validRequest(); value.WorkspaceKey = "OTHER"; return value }()},
		{name: "unverified parent", parent: VerifiedRun{}, request: validRequest()},
		{name: "terminal parent", parent: func() VerifiedRun {
			value := validParent()
			value.Status = "completed"
			return value
		}(), request: validRequest()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := workflow.Emit(t.Context(), test.parent, test.request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Emit error = %v, want %v", err, ErrInvalidRequest)
			}
		})
	}
}

func TestEmitMapsPayloadAndAttributesWithoutAddingProvenance(t *testing.T) {
	request := validRequest()
	wantPayload := append(json.RawMessage(nil), request.Payload...)
	wantAttrs := map[string]string{"issue_id": "42"}
	workflow, err := New(
		authorityProviderFunc(func(context.Context, VerifiedRun) (authority.ExecutionAuthority, error) {
			return authority.ExecutionAuthority{}, nil
		}),
		admissionFunc(func(_ context.Context, _ authority.ExecutionAuthority, command automation.WorkflowEvent) (*automation.AdmissionResult, error) {
			if !reflect.DeepEqual(command.Payload, wantPayload) || !reflect.DeepEqual(command.SubjectAttrs, wantAttrs) {
				t.Fatalf("mapped payload/attrs = %s/%v", command.Payload, command.SubjectAttrs)
			}
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Emit(t.Context(), validParent(), request); err != nil {
		t.Fatal(err)
	}
}
