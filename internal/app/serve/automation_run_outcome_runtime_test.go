package serve

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type runOutcomeRuntimeTestExecution struct {
	execution.DriverRunAPI
}

type runOutcomeRuntimeAuthorityProviderFunc func(
	context.Context,
	systemeventing.VerifiedSource,
) (authority.SystemAuthority, error)

func (function runOutcomeRuntimeAuthorityProviderFunc) AuthorityForVerifiedSource(
	ctx context.Context,
	source systemeventing.VerifiedSource,
) (authority.SystemAuthority, error) {
	return function(ctx, source)
}

type runOutcomeRuntimeAdmissionFunc func(
	context.Context,
	authority.SystemAuthority,
	automation.SystemEvent,
) (*automation.AdmissionResult, error)

func (function runOutcomeRuntimeAdmissionFunc) AdmitSystemEvent(
	ctx context.Context,
	eventAuthority authority.SystemAuthority,
	command automation.SystemEvent,
) (*automation.AdmissionResult, error) {
	return function(ctx, eventAuthority, command)
}

func (runOutcomeRuntimeTestExecution) RecoverTerminalDriverRunWork(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverTerminalDriverRunWorkCommand,
) (execution.RecoverTerminalDriverRunWorkResult, error) {
	return execution.RecoverTerminalDriverRunWorkResult{
		ActionID: command.RequestID,
		Committed: &execution.RecoverTerminalDriverRunWorkCommit{
			WorkspaceKey: command.WorkspaceKey, DriverRunID: command.DriverRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason,
			ErrorClass: command.ErrorClass, RecoveredAt: command.RecoveredAt,
		},
	}, nil
}

func (runOutcomeRuntimeTestExecution) RecoverChildDriverRunCascade(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverChildDriverRunCascadeCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{
		ActionID: command.RequestID,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey, ParentRunID: command.ParentRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason,
			ErrorClass: command.ErrorClass, CascadedAt: command.CascadedAt,
			MaxDepth: command.MaxDepth,
		},
	}, nil
}

func TestNewRunOutcomeRuntimeRegistrationDoesNotRequireAutomationPublisher(t *testing.T) {
	st := memstore.New()
	capability, err := NewExecutionCapability(executionTestDependencies(t, st))
	if err != nil {
		t.Fatal(err)
	}
	registration, err := NewRunOutcomeRuntimeRegistrationWithExecution(
		st.Awaits(), st.TriggerEvents(), st.Workspaces(), nil, "WS",
		runOutcomeRuntimeTestExecution{DriverRunAPI: capability.DriverRunAPI()},
		capability.DriverRunOutcomeAPI(), capability.TerminalDriverRunWorkRecoveryQueueAPI(), capability.SystemAuthorityResolver(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if registration.Component == nil || string(registration.Component.ID()) != systemeventing.DriverRunOutcomeComponentID {
		t.Fatalf("registration component = %#v", registration.Component)
	}
	if !registration.Policy.Immediate ||
		registration.Policy.Cadence != RunOutcomeReconcileCadence {
		t.Fatalf("registration policy = %+v", registration.Policy)
	}
}

func TestRunOutcomeRuntimePublishesOpaqueRunIDThroughAutomationAdmission(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()
	const workspace = "WS"
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: workspace, Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: workspace, DriverID: "driver", Name: "driver",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey: workspace, DriverID: "driver", VersionID: "v1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatal(err)
	}
	runID := " run/1 " + strings.Repeat("x", 300)
	created, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey: workspace, RunID: runID, DriverID: "driver", DriverVersionID: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, workspace, created.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	final, err := st.DriverRuns().Finish(ctx, workspace, created.RunID, execution.DriverRunFinish{
		NodeID: "node", LeaseID: "lease", FencingToken: claimed.FencingToken,
		Status: execution.DriverRunFailed, Summary: "failed", ErrorClass: "runtime",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got automation.SystemEvent
	workflow, err := systemeventing.New(
		runOutcomeRuntimeAuthorityProviderFunc(func(context.Context, systemeventing.VerifiedSource) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, nil
		}),
		runOutcomeRuntimeAdmissionFunc(func(_ context.Context, _ authority.SystemAuthority, command automation.SystemEvent) (*automation.AdmissionResult, error) {
			got = command
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := systemeventing.BindRunOutcomeEmitter(workflow)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := NewDriverRunOutcomePublisher(emitter)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := NewExecutionCapability(executionTestDependencies(t, st))
	if err != nil {
		t.Fatal(err)
	}
	registration, err := NewRunOutcomeRuntimeRegistrationWithExecution(
		st.Awaits(), st.TriggerEvents(), st.Workspaces(), publisher, workspace,
		runOutcomeRuntimeTestExecution{DriverRunAPI: capability.DriverRunAPI()},
		capability.DriverRunOutcomeAPI(), capability.TerminalDriverRunWorkRecoveryQueueAPI(), capability.SystemAuthorityResolver(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Component.RunOnce(ctx, final.FinishedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got.SourceRef != runID || got.SubjectRef != runID {
		t.Fatalf("opaque source/subject = %q / %q", got.SourceRef, got.SubjectRef)
	}
	if got.SourceEventID == "" || len(got.SourceEventID) > 86 || !safePrintableASCII(got.SourceEventID) {
		t.Fatalf("bounded event id = %q (%d bytes)", got.SourceEventID, len(got.SourceEventID))
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["runId"] != runID || payload["status"] != string(execution.DriverRunFailed) {
		t.Fatalf("admission payload = %s", got.Payload)
	}
}

func safePrintableASCII(value string) bool {
	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
