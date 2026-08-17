package systemeventing

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type authorityProviderFunc func(context.Context, VerifiedSource) (authority.SystemAuthority, error)

func (function authorityProviderFunc) AuthorityForVerifiedSource(ctx context.Context, source VerifiedSource) (authority.SystemAuthority, error) {
	return function(ctx, source)
}

type admissionFunc func(context.Context, authority.SystemAuthority, automation.SystemEvent) (*automation.AdmissionResult, error)

func (function admissionFunc) AdmitSystemEvent(ctx context.Context, eventAuthority authority.SystemAuthority, command automation.SystemEvent) (*automation.AdmissionResult, error) {
	return function(ctx, eventAuthority, command)
}

func TestEmitSeparatesVerifiedSourceFromEventContent(t *testing.T) {
	source := VerifiedSource{ComponentID: IssueJournalBridgeComponentID, WorkspaceKey: "WS", ActorRef: "driver-run:parent"}
	request := EmitRequest{WorkspaceKey: "WS", SourceEventID: "journal-1", EventType: "issue.create", SourceRef: "journal:1", SubjectRef: "issue:1", Payload: json.RawMessage(`{"id":"1"}`), SubjectAttrs: map[string]string{"status": "open"}}
	var gotSource VerifiedSource
	var gotCommand automation.SystemEvent
	want := &automation.AdmissionResult{Event: &automation.Event{EventID: "event-1"}}
	workflow, err := New(
		authorityProviderFunc(func(_ context.Context, value VerifiedSource) (authority.SystemAuthority, error) {
			gotSource = value
			return authority.SystemAuthority{}, nil
		}),
		admissionFunc(func(_ context.Context, _ authority.SystemAuthority, command automation.SystemEvent) (*automation.AdmissionResult, error) {
			gotCommand = command
			return want, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := BindIssueJournalEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := emitter.EmitIssueJournal(t.Context(), source.WorkspaceKey, source.ActorRef, request)
	if err != nil || result != want {
		t.Fatalf("Emit = (%+v, %v), want (%+v, nil)", result, err, want)
	}
	if !reflect.DeepEqual(gotSource, source) {
		t.Fatalf("verified source = %+v, want %+v", gotSource, source)
	}
	if gotCommand.WorkspaceKey != "WS" || gotCommand.SourceEventID != "journal-1" || gotCommand.EventType != "issue.create" {
		t.Fatalf("admission command = %+v", gotCommand)
	}
	for _, forbidden := range []string{"SourceKind", "RouteKey", "ActorRef", "Origin", "HopDepth", "SignatureStatus", "IdempotencyKey", "Authority"} {
		if _, exists := reflect.TypeOf(automation.SystemEvent{}).FieldByName(forbidden); exists {
			t.Errorf("SystemEvent exposes forbidden field %q", forbidden)
		}
	}
	for _, forbidden := range []string{"ActorRef", "Origin", "HopDepth", "SignatureStatus", "IdempotencyKey", "Authority"} {
		if _, exists := reflect.TypeOf(EmitRequest{}).FieldByName(forbidden); exists {
			t.Errorf("EmitRequest exposes forbidden field %q", forbidden)
		}
	}
}

func TestEmitFailsClosedBeforeAdmission(t *testing.T) {
	calls := 0
	workflow, err := New(
		authorityProviderFunc(func(context.Context, VerifiedSource) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, authority.ErrAdmissionDenied
		}),
		admissionFunc(func(context.Context, authority.SystemAuthority, automation.SystemEvent) (*automation.AdmissionResult, error) {
			calls++
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := BindIssueJournalEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	request := EmitRequest{WorkspaceKey: "WS", SourceEventID: "event-1", EventType: "issue.create"}
	if _, err := emitter.EmitIssueJournal(t.Context(), "OTHER", "", request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("wrong workspace error = %v", err)
	}
	if _, err := emitter.EmitIssueJournal(t.Context(), "WS", "", request); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("authority error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("admission calls = %d, want 0", calls)
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	provider := authorityProviderFunc(func(context.Context, VerifiedSource) (authority.SystemAuthority, error) {
		return authority.SystemAuthority{}, nil
	})
	admission := admissionFunc(func(context.Context, authority.SystemAuthority, automation.SystemEvent) (*automation.AdmissionResult, error) {
		return nil, nil
	})
	if _, err := New(nil, admission); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil provider error = %v", err)
	}
	if _, err := New(provider, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil admission error = %v", err)
	}
	if _, err := BindIssueJournalEmitter(nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil issue journal workflow error = %v", err)
	}
	if _, err := BindRunOutcomeEmitter(nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil run outcome workflow error = %v", err)
	}
}

func TestEmitRunOutcomeTreatsNoListenerAsSuccess(t *testing.T) {
	var gotSource VerifiedSource
	workflow, err := New(
		authorityProviderFunc(func(_ context.Context, source VerifiedSource) (authority.SystemAuthority, error) {
			gotSource = source
			return authority.SystemAuthority{}, nil
		}),
		admissionFunc(func(context.Context, authority.SystemAuthority, automation.SystemEvent) (*automation.AdmissionResult, error) {
			return nil, errors.Join(automation.ErrNoMatchingBinding, automation.ErrNotFound)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := BindRunOutcomeEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	request := EmitRequest{WorkspaceKey: "WS", SourceEventID: "run-finished:run-1:completed", EventType: "run.finished", SubjectRef: "run-1"}
	result, err := emitter.EmitRunOutcome(t.Context(), "WS", "system", request)
	if err != nil || result != nil {
		t.Fatalf("EmitRunOutcome no listener = (%+v, %v), want (nil, nil)", result, err)
	}
	if gotSource != (VerifiedSource{ComponentID: DriverRunOutcomeComponentID, WorkspaceKey: "WS", ActorRef: "system"}) {
		t.Fatalf("bound run outcome source = %+v", gotSource)
	}
}
