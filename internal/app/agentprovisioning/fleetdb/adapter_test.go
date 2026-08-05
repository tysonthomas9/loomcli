package fleetdb

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

type transportStub struct {
	beginWorkspace string
	beginInput     infrafleetdb.AgentProvisioningBeginInput
	saveWorkspace  string
	saveID         string
	saveInput      infrafleetdb.AgentProvisioningProgressInput
	record         *infrafleetdb.AgentProvisioningRecord
	pending        []*infrafleetdb.AgentProvisioningRecord
	stepWorkspace  string
	stepID         string
	stepGeneration string
	stepGrantID    string
	role           *infrafleetdb.AgentProvisioningRoleResult
	agent          *infrafleetdb.AgentProvisioningAgentResult
	binding        *infrafleetdb.AgentProvisioningBindingResult
	grant          *infrafleetdb.AgentProvisioningGrantResult
	err            error
}

func (stub *transportStub) BeginAgentProvisioning(
	_ context.Context,
	workspace string,
	input infrafleetdb.AgentProvisioningBeginInput,
) (*infrafleetdb.AgentProvisioningRecord, error) {
	stub.beginWorkspace = workspace
	stub.beginInput = input
	return stub.record, stub.err
}

func (stub *transportStub) GetAgentProvisioning(
	context.Context,
	string,
	string,
) (*infrafleetdb.AgentProvisioningRecord, error) {
	return stub.record, stub.err
}

func (stub *transportStub) ListPendingAgentProvisioning(
	context.Context,
	string,
	int,
) ([]*infrafleetdb.AgentProvisioningRecord, error) {
	return stub.pending, stub.err
}

func (stub *transportStub) SaveAgentProvisioningProgress(
	_ context.Context,
	workspace,
	provisioningID string,
	input infrafleetdb.AgentProvisioningProgressInput,
) (*infrafleetdb.AgentProvisioningRecord, error) {
	stub.saveWorkspace = workspace
	stub.saveID = provisioningID
	stub.saveInput = input
	return stub.record, stub.err
}

func (stub *transportStub) captureStep(workspace, provisioningID, generationID string) {
	stub.stepWorkspace = workspace
	stub.stepID = provisioningID
	stub.stepGeneration = generationID
}

func (stub *transportStub) EnsureAgentProvisioningRole(
	_ context.Context,
	workspace,
	provisioningID,
	generationID string,
) (*infrafleetdb.AgentProvisioningRoleResult, error) {
	stub.captureStep(workspace, provisioningID, generationID)
	return stub.role, stub.err
}

func (stub *transportStub) EnsureAgentProvisioningAgentService(
	_ context.Context,
	workspace,
	provisioningID,
	generationID string,
) (*infrafleetdb.AgentProvisioningAgentResult, error) {
	stub.captureStep(workspace, provisioningID, generationID)
	return stub.agent, stub.err
}

func (stub *transportStub) EnsureAgentProvisioningTriggerBinding(
	_ context.Context,
	workspace,
	provisioningID,
	generationID string,
) (*infrafleetdb.AgentProvisioningBindingResult, error) {
	stub.captureStep(workspace, provisioningID, generationID)
	return stub.binding, stub.err
}

func (stub *transportStub) EnsureAgentProvisioningConnectorGrant(
	_ context.Context,
	workspace,
	provisioningID,
	generationID,
	grantID string,
) (*infrafleetdb.AgentProvisioningGrantResult, error) {
	stub.captureStep(workspace, provisioningID, generationID)
	stub.stepGrantID = grantID
	return stub.grant, stub.err
}

func TestAdapterMapsServerCanonicalRecordWithoutMintingRequesterOrFingerprint(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	wire := canonicalWireRecord(now)
	stub := &transportStub{record: wire, pending: []*infrafleetdb.AgentProvisioningRecord{wire}}
	adapter, err := New(stub)
	if err != nil {
		t.Fatal(err)
	}
	spec := canonicalSpec()
	record, err := adapter.Begin(t.Context(), spec, "loom-operator")
	if err != nil {
		t.Fatal(err)
	}
	if stub.beginWorkspace != "TEST" ||
		stub.beginInput.ProvisioningID != "provision-1" ||
		stub.beginInput.Role.Name != "docs-review" ||
		stub.beginInput.Role.TaskFilter != "status=review" ||
		!reflect.DeepEqual(stub.beginInput.Role.AllowedTools, []string{"Read", "Edit"}) ||
		stub.beginInput.DelegatedActor != "loom-operator" ||
		stub.beginInput.Agent.Metadata["backend"] != "codex" ||
		record.RequestedBy != "loom-service" ||
		record.ProvisioningGenerationID != wire.ProvisioningGenerationID ||
		record.SpecFingerprint != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("begin input=%+v record=%+v", stub.beginInput, record)
	}
	wire.Spec.Agent.Metadata["backend"] = "mutated"
	wire.Spec.Binding.EventPatterns[0] = "mutated"
	wire.Spec.Role.AllowedTools[0] = "mutated"
	if record.Spec.Agent.Metadata["backend"] != "codex" ||
		record.Spec.Binding.EventPatterns[0] != "internal.task.review" ||
		record.Spec.Role.AllowedTools[0] != "Read" {
		t.Fatal("record aliased FleetDB wire values")
	}
	wantCanonical := canonicalOwnerSpec()
	if !reflect.DeepEqual(record.Spec, wantCanonical) {
		t.Fatalf("record spec = %+v, want server canonical %+v", record.Spec, wantCanonical)
	}

	record.State = agentprovisioning.StateRunning
	record.CompletedSteps = []agentprovisioning.Step{agentprovisioning.StepRole}
	record.LastErrorClass = ""
	if _, err := adapter.Save(t.Context(), record, 1); err != nil {
		t.Fatal(err)
	}
	if stub.saveWorkspace != "TEST" || stub.saveID != "provision-1" ||
		stub.saveInput.ExpectedProvisioningGenerationID != wire.ProvisioningGenerationID ||
		stub.saveInput.ExpectedVersion != 1 ||
		!reflect.DeepEqual(stub.saveInput.CompletedSteps, []string{"role"}) {
		t.Fatalf("save input = %+v", stub.saveInput)
	}
}

func TestAdapterMapsTypedTransportFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "not found", err: infrafleetdb.ErrAgentProvisioningNotFound, want: agentprovisioning.ErrNotFound},
		{name: "invalid", err: infrafleetdb.ErrAgentProvisioningInvalid, want: agentprovisioning.ErrInvalid},
		{name: "conflict", err: infrafleetdb.ErrAgentProvisioningConflict, want: agentprovisioning.ErrConflict},
		{name: "concurrent", err: infrafleetdb.ErrAgentProvisioningConcurrentWrite, want: agentprovisioning.ErrConcurrentWrite},
		{name: "transition", err: infrafleetdb.ErrAgentProvisioningInvalidTransition, want: agentprovisioning.ErrInvalidTransition},
		{name: "unavailable", err: errors.New("network down"), want: agentprovisioning.ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := New(&transportStub{err: test.err})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Get(t.Context(), "TEST", "provision-1")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.want)
			}
		})
	}
}

func TestAdapterUsesProvisioningGuardForEveryExactOwnerStep(t *testing.T) {
	spec := canonicalOwnerSpec()
	stub := &transportStub{
		role: &infrafleetdb.AgentProvisioningRoleResult{
			WorkspaceKey: "TEST", Name: spec.Role.Name,
			Kind: spec.Role.Kind, Description: spec.Role.Description,
			Prompt: spec.Role.Prompt, PromptFile: spec.Role.PromptFile,
			Model: spec.Role.Model, TaskFilter: spec.Role.TaskFilter,
			Backend: spec.Role.Backend, Effort: spec.Role.Effort,
			PathPatterns:   append([]string(nil), spec.Role.PathPatterns...),
			Skills:         append([]string(nil), spec.Role.Skills...),
			MaxPriority:    cloneInt(spec.Role.MaxPriority),
			MaxConcurrency: cloneInt(spec.Role.MaxConcurrency),
			ReadOnly:       spec.Role.ReadOnly,
			AllowedTools:   append([]string(nil), spec.Role.AllowedTools...),
			DeniedTools:    append([]string(nil), spec.Role.DeniedTools...),
			MaxBudgetUSD:   cloneFloat64(spec.Role.MaxBudgetUSD),
		},
		agent: &infrafleetdb.AgentProvisioningAgentResult{
			WorkspaceKey: "TEST", ServiceID: spec.Agent.AgentID,
			Name: spec.Agent.Name, Kind: spec.Agent.Kind,
			DesiredState: spec.Agent.DesiredState,
			RoleName:     spec.Agent.RoleName, BudgetPolicy: spec.Agent.BudgetPolicy,
			Metadata: map[string]string{"backend": "codex"},
		},
		binding: &infrafleetdb.AgentProvisioningBindingResult{
			WorkspaceKey: "TEST", BindingID: spec.Binding.BindingID,
			Name: spec.Binding.Name, SourceKind: spec.Binding.SourceKind,
			SourceConfigRef: spec.Binding.SourceConfigRef, RouteKey: spec.Binding.RouteKey,
			EventTypePatterns: append([]string(nil), spec.Binding.EventPatterns...),
			DriverID:          spec.Binding.DriverID, DriverVersionID: spec.Binding.DriverVersionID,
			TargetEntrypoint:     spec.Binding.Entrypoint,
			TargetAgentServiceID: spec.Agent.AgentID,
			ConcurrencyPolicy:    spec.Binding.ConcurrencyPolicy,
			Schedule:             spec.Binding.Schedule, ScheduleTimezone: spec.Binding.ScheduleZone,
			Enabled: spec.Binding.Enabled,
		},
		grant: &infrafleetdb.AgentProvisioningGrantResult{
			WorkspaceKey: "TEST", GrantID: spec.Grants[0].GrantID,
			ConnectorID: spec.Grants[0].ConnectorID,
			BindingID:   spec.Binding.BindingID, Action: spec.Grants[0].Action,
			ResourcePattern: spec.Grants[0].ResourcePattern,
		},
	}
	adapter, err := New(stub)
	if err != nil {
		t.Fatal(err)
	}
	const generationID = "0123456789abcdef0123456789abcdef"
	if err := adapter.EnsureRole(t.Context(), agentprovisioning.EnsureRoleCommand{
		CommandID: "provision-1:role", WorkspaceKey: "TEST",
		ProvisioningID: "provision-1", ProvisioningGenerationID: generationID,
		Role: spec.Role,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.EnsureAgent(t.Context(), agentprovisioning.EnsureAgentCommand{
		CommandID: "provision-1:agent", WorkspaceKey: "TEST",
		ProvisioningID: "provision-1", ProvisioningGenerationID: generationID,
		Agent: spec.Agent,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.EnsureBinding(t.Context(), agentprovisioning.EnsureBindingCommand{
		CommandID: "provision-1:binding", WorkspaceKey: "TEST",
		ProvisioningID: "provision-1", ProvisioningGenerationID: generationID,
		AgentID: spec.Agent.AgentID, Binding: spec.Binding,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.EnsureGrant(t.Context(), agentprovisioning.EnsureGrantCommand{
		CommandID: "provision-1:grant:grant-read", WorkspaceKey: "TEST",
		ProvisioningID: "provision-1", ProvisioningGenerationID: generationID,
		BindingID: spec.Binding.BindingID, Grant: spec.Grants[0],
	}); err != nil {
		t.Fatal(err)
	}
	if stub.stepWorkspace != "TEST" || stub.stepID != "provision-1" ||
		stub.stepGeneration != generationID || stub.stepGrantID != "grant-read" {
		t.Fatalf(
			"last guard = workspace:%q provisioning:%q generation:%q grant:%q",
			stub.stepWorkspace,
			stub.stepID,
			stub.stepGeneration,
			stub.stepGrantID,
		)
	}
}

func TestAdapterRejectsInvalidOwnerStepGuardBeforeTransport(t *testing.T) {
	stub := &transportStub{}
	adapter, err := New(stub)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.EnsureRole(t.Context(), agentprovisioning.EnsureRoleCommand{
		WorkspaceKey: "TEST", ProvisioningID: "provision-1",
		ProvisioningGenerationID: "caller-selected",
	})
	if !errors.Is(err, agentprovisioning.ErrInvalid) {
		t.Fatalf("EnsureRole invalid generation = %v", err)
	}
	if stub.stepWorkspace != "" {
		t.Fatal("invalid guard reached FleetDB transport")
	}
}

func TestAdapterRejectsNilCompositionAndNilSave(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("New(nil) = %v", err)
	}
	adapter, err := New(&transportStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Save(t.Context(), nil, 1); !errors.Is(err, agentprovisioning.ErrInvalid) {
		t.Fatalf("Save(nil) = %v", err)
	}
}

func TestAdapterRejectsDivergentServerOwnedIdentity(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*infrafleetdb.AgentProvisioningRecord)
	}{
		{
			name: "provisioning id",
			mutate: func(record *infrafleetdb.AgentProvisioningRecord) {
				record.Spec.ProvisioningID = "other-provisioning"
			},
		},
		{
			name: "workspace",
			mutate: func(record *infrafleetdb.AgentProvisioningRecord) {
				record.Spec.WorkspaceKey = "OTHER"
			},
		},
		{
			name: "authenticated requester",
			mutate: func(record *infrafleetdb.AgentProvisioningRecord) {
				record.Spec.RequestedBy = "forged-requester"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := canonicalWireRecord(now)
			test.mutate(wire)
			adapter, err := New(&transportStub{record: wire})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Get(t.Context(), "TEST", "provision-1"); !errors.Is(err, agentprovisioning.ErrConflict) {
				t.Fatalf("Get divergent record = %v, want ErrConflict", err)
			}
		})
	}
}

func canonicalSpec() agentprovisioning.Spec {
	maxPriority := 5
	maxConcurrency := 2
	maxBudgetUSD := 12.5
	return agentprovisioning.Spec{
		ProvisioningID: "provision-1", WorkspaceKey: "TEST",
		Role: agentprovisioning.RoleSpec{
			Name: "docs-review", Kind: "worker", Prompt: "review docs",
			TaskFilter: "status=review", PathPatterns: []string{"docs/**", "README.md"},
			Skills: []string{"documentation"}, MaxPriority: &maxPriority,
			MaxConcurrency: &maxConcurrency, AllowedTools: []string{"Read", "Edit"},
			DeniedTools: []string{"Shell"}, MaxBudgetUSD: &maxBudgetUSD,
		},
		Agent: agentprovisioning.AgentSpec{
			AgentID: "agent-1", Name: "Docs Review", Kind: "event",
			DesiredState: "running", RoleName: "docs-review",
			Metadata: map[string]string{"backend": "codex"},
		},
		Binding: agentprovisioning.BindingSpec{
			BindingID: "binding-1", SourceKind: "internal",
			SourceConfigRef: "role://docs-review?backend=codex",
			EventPatterns:   []string{"internal.task.review"},
			DriverID:        "prompt-agent", DriverVersionID: "prompt-agent-v1",
			Entrypoint: "run", ConcurrencyPolicy: "one_active_per_epic", Enabled: true,
		},
		Grants: []agentprovisioning.GrantSpec{
			{
				GrantID: "grant-read", ConnectorID: "github",
				Action: "pull_request.read", ResourcePattern: "repo:acme/docs",
			},
		},
	}
}

func canonicalOwnerSpec() agentprovisioning.Spec {
	spec := canonicalSpec()
	spec.Binding.RouteKey = "internal:" + spec.Binding.BindingID
	spec.Binding.Name = spec.Binding.RouteKey
	return spec
}

func canonicalWireRecord(now time.Time) *infrafleetdb.AgentProvisioningRecord {
	spec := canonicalOwnerSpec()
	return &infrafleetdb.AgentProvisioningRecord{
		ProvisioningID:           "provision-1",
		ProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
		WorkspaceKey:             "TEST", RequestedBy: "loom-service",
		SpecFingerprint: "sha256:" + strings.Repeat("a", 64),
		Spec: infrafleetdb.AgentProvisioningSpec{
			ProvisioningID: "provision-1", WorkspaceKey: "TEST", RequestedBy: "loom-service",
			Role:    roleToWire(spec.Role),
			Agent:   agentToWire(spec.Agent),
			Binding: bindingToWire(spec.Binding),
			Grants:  grantsToWire(spec.Grants),
		},
		State: "pending", UnusedRolePolicy: "retain", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
}
