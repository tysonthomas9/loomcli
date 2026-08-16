package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type systemAuthorityProviderFunc func(context.Context, systemeventing.VerifiedSource) (authority.SystemAuthority, error)

func (function systemAuthorityProviderFunc) AuthorityForVerifiedSource(ctx context.Context, source systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
	return function(ctx, source)
}

type systemAdmissionFunc func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error)

func (function systemAdmissionFunc) AdmitEvent(ctx context.Context, eventAuthority automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	return function(ctx, eventAuthority, command)
}

func TestAutomationIssueJournalEmitterUsesNamedSystemWorkflow(t *testing.T) {
	var gotSource systemeventing.VerifiedSource
	var gotCommand automation.AdmitEventCommand
	workflow, err := systemeventing.New(
		systemAuthorityProviderFunc(func(_ context.Context, source systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
			gotSource = source
			return authority.SystemAuthority{}, nil
		}),
		systemAdmissionFunc(func(_ context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			gotCommand = command
			return &automation.AdmissionResult{
				Event:     &automation.Event{SourceEventID: command.SourceEventID, EventType: "issue.created", ActorRef: "journal-actor"},
				EventType: "issue.created", RouteKey: "internal.issue.created", Origin: automation.EventOriginSystem, HopDepth: 0,
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	journalEvents, err := systemeventing.BindIssueJournalEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	emitter := serveadapter.NewAutomationIssueJournalEmitter(journalEvents, nil)
	result, err := emitter.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "fleet-journal-1", EventType: "issue.create", Origin: automation.EventOriginSystem,
		ActorRef: "journal-actor", SubjectRef: "issue:1", Payload: []byte(`{"status":"open"}`),
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if gotSource.ComponentID != systemeventing.IssueJournalBridgeComponentID || gotSource.WorkspaceKey != "WS" || gotSource.ActorRef != "journal-actor" {
		t.Fatalf("verified source = %+v", gotSource)
	}
	if gotCommand.SourceKind != automation.SourceKindInternal || gotCommand.SourceEventID != "fleet-journal-1" || gotCommand.ActorRef != "" {
		t.Fatalf("admission command = %+v", gotCommand)
	}
	if result == nil || result.EventType != "issue.created" || result.RouteKey != "internal.issue.created" || result.Origin != automation.EventOriginSystem {
		t.Fatalf("mapped result = %+v", result)
	}
}

func TestAutomationIssueJournalEmitterMapsNoListenerAndRejectsForgedOrigin(t *testing.T) {
	workflow, err := systemeventing.New(
		systemAuthorityProviderFunc(func(context.Context, systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, nil
		}),
		systemAdmissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			return nil, automation.ErrNoMatchingBinding
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	journalEvents, err := systemeventing.BindIssueJournalEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	emitter := serveadapter.NewAutomationIssueJournalEmitter(journalEvents, nil)
	if _, err := emitter.Emit(t.Context(), "WS", trigger.InternalEvent{EventID: "event-1", EventType: "issue.create", Origin: automation.EventOriginSystem}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("no listener error = %v, want persistence.ErrNotFound", err)
	}
	if _, err := emitter.Emit(t.Context(), "WS", trigger.InternalEvent{EventID: "event-2", EventType: "issue.create", Origin: automation.EventOriginWorkflow}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("forged origin error = %v, want persistence.ErrInvalid", err)
	}
	if got := serveadapter.NewAutomationIssueJournalEmitter(nil, nil); got != nil {
		t.Fatalf("nil workflow emitter = %#v, want nil", got)
	}
}
