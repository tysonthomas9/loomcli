package workflowbinding

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type targetPreparerStub struct {
	target WorkflowTarget
	err    error
	calls  int
	ws     string
	name   string
}

func (stub *targetPreparerStub) PrepareWorkflowTarget(_ context.Context, workspace, name string) (WorkflowTarget, error) {
	stub.calls++
	stub.ws = workspace
	stub.name = name
	return stub.target, stub.err
}

type bindingCreatorStub struct {
	result  *automation.Binding
	err     error
	calls   int
	command automation.CreateBindingCommand
}

func (stub *bindingCreatorStub) CreateBinding(
	_ context.Context,
	_ authority.OperatorAuthority,
	command automation.CreateBindingCommand,
) (*automation.Binding, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

func TestCreatePreparesWorkflowTargetBeforeDelegatingToAutomation(t *testing.T) {
	preparer := &targetPreparerStub{target: WorkflowTarget{DriverID: "driver-1", DriverVersionID: "version-7"}}
	creator := &bindingCreatorStub{result: &automation.Binding{BindingID: "binding-1"}}
	workflow, err := New(preparer, creator)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := workflow.Create(t.Context(), authority.OperatorAuthority{}, CreateRequest{
		WorkspaceKey: " WS ", Workflow: " github-review-agent ",
		Definition: automation.BindingDefinition{BindingID: "binding-1", RouteKey: "github.pull_request"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result != creator.result || preparer.calls != 1 || preparer.ws != "WS" || preparer.name != "github-review-agent" {
		t.Fatalf("result=%#v preparer=%+v", result, preparer)
	}
	if creator.calls != 1 || creator.command.WorkspaceKey != "WS" ||
		creator.command.Definition.DriverID != "driver-1" || creator.command.Definition.DriverVersionID != "version-7" ||
		creator.command.Definition.BindingID != "binding-1" {
		t.Fatalf("Automation command = %+v", creator.command)
	}
}

func TestCreateExplicitDriverBypassesWorkflowPreparation(t *testing.T) {
	preparer := &targetPreparerStub{err: errors.New("must not run")}
	creator := &bindingCreatorStub{result: &automation.Binding{BindingID: "binding-1"}}
	workflow, err := New(preparer, creator)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = workflow.Create(t.Context(), authority.OperatorAuthority{}, CreateRequest{
		WorkspaceKey: "WS", Workflow: "ignored-workflow",
		Definition: automation.BindingDefinition{
			BindingID: "binding-1", DriverID: " explicit-driver ", DriverVersionID: " explicit-version ",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if preparer.calls != 0 {
		t.Fatalf("preparer calls = %d, want 0", preparer.calls)
	}
	if creator.command.Definition.DriverID != "explicit-driver" || creator.command.Definition.DriverVersionID != "explicit-version" {
		t.Fatalf("explicit target = %q/%q", creator.command.Definition.DriverID, creator.command.Definition.DriverVersionID)
	}
}

func TestCreateWorkflowPreservesRequestedVersionForAutomationValidation(t *testing.T) {
	preparer := &targetPreparerStub{target: WorkflowTarget{DriverID: "driver-1", DriverVersionID: "active-version"}}
	creator := &bindingCreatorStub{}
	workflow, err := New(preparer, creator)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = workflow.Create(t.Context(), authority.OperatorAuthority{}, CreateRequest{
		WorkspaceKey: "WS", Workflow: "github-review-agent",
		Definition: automation.BindingDefinition{BindingID: "binding-1", DriverVersionID: "requested-version"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if creator.command.Definition.DriverID != "driver-1" || creator.command.Definition.DriverVersionID != "requested-version" {
		t.Fatalf("delegated target = %q/%q", creator.command.Definition.DriverID, creator.command.Definition.DriverVersionID)
	}
}

func TestResolveTargetPreparesWithoutCreatingBinding(t *testing.T) {
	preparer := &targetPreparerStub{target: WorkflowTarget{
		DriverID: " driver-1 ", DriverVersionID: " version-7 ",
	}}
	creator := &bindingCreatorStub{}
	workflow, err := New(preparer, creator)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	target, err := workflow.ResolveTarget(t.Context(), " WS ", " github-review-agent ")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.DriverID != "driver-1" || target.DriverVersionID != "version-7" {
		t.Fatalf("target = %+v", target)
	}
	if preparer.calls != 1 || preparer.ws != "WS" || preparer.name != "github-review-agent" {
		t.Fatalf("preparer = %+v", preparer)
	}
	if creator.calls != 0 {
		t.Fatalf("creator calls = %d, want 0", creator.calls)
	}
}

func TestCreatePreparationFailureStopsBeforeAutomation(t *testing.T) {
	prepErr := errors.New("builtin build unavailable")
	preparer := &targetPreparerStub{err: prepErr}
	creator := &bindingCreatorStub{}
	workflow, err := New(preparer, creator)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := workflow.Create(t.Context(), authority.OperatorAuthority{}, CreateRequest{
		WorkspaceKey: "WS", Workflow: "github-review-agent",
		Definition: automation.BindingDefinition{BindingID: "binding-1"},
	})
	if result != nil || !errors.Is(err, prepErr) {
		t.Fatalf("result=%#v error=%v, want preparation error", result, err)
	}
	if preparer.calls != 1 || creator.calls != 0 {
		t.Fatalf("preparer calls=%d creator calls=%d", preparer.calls, creator.calls)
	}
}

func TestCreateIncompletePreparedTargetIsInvalidServerState(t *testing.T) {
	preparer := &targetPreparerStub{target: WorkflowTarget{DriverID: "driver-1"}}
	creator := &bindingCreatorStub{}
	workflow, err := New(preparer, creator)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := workflow.Create(t.Context(), authority.OperatorAuthority{}, CreateRequest{
		WorkspaceKey: "WS", Workflow: "github-review-agent",
		Definition: automation.BindingDefinition{BindingID: "binding-1"},
	})
	if result != nil || !errors.Is(err, automation.ErrInvalidPersistedState) {
		t.Fatalf("result=%#v error=%v, want invalid persisted state", result, err)
	}
	if preparer.calls != 1 || creator.calls != 0 {
		t.Fatalf("preparer calls=%d creator calls=%d", preparer.calls, creator.calls)
	}
}

func TestNewAndNilWorkflowFailClosed(t *testing.T) {
	if _, err := New(nil, &bindingCreatorStub{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil preparer error = %v", err)
	}
	if _, err := New(&targetPreparerStub{}, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil creator error = %v", err)
	}
	if _, err := (*Workflow)(nil).Create(t.Context(), authority.OperatorAuthority{}, CreateRequest{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil workflow error = %v", err)
	}
}
