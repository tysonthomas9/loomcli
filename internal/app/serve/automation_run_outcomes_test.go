package serve

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type runOutcomeAuthorityProviderFunc func(context.Context, systemeventing.VerifiedSource) (authority.SystemAuthority, error)

func (fn runOutcomeAuthorityProviderFunc) AuthorityForVerifiedSource(ctx context.Context, source systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
	return fn(ctx, source)
}

type runOutcomeAdmissionFunc func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error)

func (fn runOutcomeAdmissionFunc) AdmitEvent(ctx context.Context, eventAuthority automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	return fn(ctx, eventAuthority, command)
}

func TestAutomationDriverRunOutcomePublisherMapsTrustedEnvelope(t *testing.T) {
	var gotSource systemeventing.VerifiedSource
	var gotCommand automation.AdmitEventCommand
	workflow, err := systemeventing.New(
		runOutcomeAuthorityProviderFunc(func(_ context.Context, source systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
			gotSource = source
			return authority.SystemAuthority{}, nil
		}),
		runOutcomeAdmissionFunc(func(_ context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			gotCommand = command
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runOutcomes, err := systemeventing.BindRunOutcomeEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := newAutomationDriverRunOutcomePublisher(runOutcomes)
	if err != nil {
		t.Fatal(err)
	}
	occurredAt := time.Date(2026, 7, 16, 3, 4, 5, 0, time.UTC)
	payload := json.RawMessage(`{"runId":"run-1","status":"failed"}`)
	outcome := driver.RunOutcome{
		WorkspaceKey: "WS", EventID: driver.RunFinishedEventID("run-1", domain.DriverRunFailed),
		EventType: driver.RunFinishedEventType, RunID: "run-1", Status: domain.DriverRunFailed,
		ActorRef: driver.RunFinishedActor, ParentEventID: "event-parent", EpicID: "EPIC-1",
		OccurredAt: occurredAt, Payload: payload,
	}
	if err := publisher.PublishRunOutcome(t.Context(), outcome); err != nil {
		t.Fatal(err)
	}
	if gotSource != (systemeventing.VerifiedSource{
		ComponentID: systemeventing.DriverRunOutcomeComponentID, WorkspaceKey: "WS", ActorRef: driver.RunFinishedActor,
	}) {
		t.Fatalf("verified source = %+v", gotSource)
	}
	if gotCommand.WorkspaceKey != "WS" || gotCommand.SourceKind != automation.SourceKindInternal ||
		gotCommand.SourceRef != "run-1" || gotCommand.SourceEventID != outcome.EventID ||
		gotCommand.EventType != driver.RunFinishedEventType || gotCommand.SubjectRef != "run-1" ||
		gotCommand.ParentEventID != "event-parent" || gotCommand.EpicID != "EPIC-1" ||
		!gotCommand.OccurredAt.Equal(occurredAt) || string(gotCommand.Payload) != string(payload) {
		t.Fatalf("admission command = %+v", gotCommand)
	}
}

func TestAutomationDriverRunOutcomePublisherRejectsForgedEnvelope(t *testing.T) {
	calls := 0
	workflow, err := systemeventing.New(
		runOutcomeAuthorityProviderFunc(func(context.Context, systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, nil
		}),
		runOutcomeAdmissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			calls++
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runOutcomes, err := systemeventing.BindRunOutcomeEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := newAutomationDriverRunOutcomePublisher(runOutcomes)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []driver.RunOutcome{
		{WorkspaceKey: "WS", EventID: "forged", EventType: driver.RunFinishedEventType, RunID: "run-1", Status: domain.DriverRunCompleted, ActorRef: driver.RunFinishedActor},
		{WorkspaceKey: "WS", EventID: driver.RunFinishedEventID("run-1", domain.DriverRunRunning), EventType: driver.RunFinishedEventType, RunID: "run-1", Status: domain.DriverRunRunning, ActorRef: driver.RunFinishedActor},
		{WorkspaceKey: "WS", EventID: driver.RunFinishedEventID("run-1", domain.DriverRunCompleted), EventType: driver.RunFinishedEventType, RunID: "run-1", Status: domain.DriverRunCompleted, ActorRef: "operator"},
	} {
		if err := publisher.PublishRunOutcome(t.Context(), outcome); !errors.Is(err, systemeventing.ErrInvalidRequest) {
			t.Fatalf("forged outcome %+v error = %v", outcome, err)
		}
	}
	if calls != 0 {
		t.Fatalf("admission calls = %d, want 0", calls)
	}
}

func TestNewAutomationDriverRunOutcomePublisherRequiresWorkflow(t *testing.T) {
	if _, err := newAutomationDriverRunOutcomePublisher(nil); !errors.Is(err, systemeventing.ErrUnavailable) {
		t.Fatalf("nil workflow error = %v", err)
	}
}
