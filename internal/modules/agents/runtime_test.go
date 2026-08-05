package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

type agentsRuntimeQueries struct {
	values map[string][]*Agent
	errs   map[string]error
	calls  []string
}

func (*agentsRuntimeQueries) GetAgent(context.Context, string, string) (*Agent, error) {
	return nil, ErrNotFound
}

func (stub *agentsRuntimeQueries) ListAgents(_ context.Context, workspace string, _ AgentFilter) ([]*Agent, error) {
	stub.calls = append(stub.calls, workspace)
	return stub.values[workspace], stub.errs[workspace]
}

type agentsRuntimeCommandCall struct {
	workspace  string
	agentID    string
	revision   time.Time
	generation string
}

type agentsRuntimeCommands struct {
	calls []agentsRuntimeCommandCall
	errs  map[string]error
}

func (stub *agentsRuntimeCommands) ReconcileDesiredState(
	_ context.Context,
	_ authority.SystemAuthority,
	command ReconcileDesiredStateCommand,
) (ReconcileDesiredStateResult, error) {
	stub.calls = append(stub.calls, agentsRuntimeCommandCall{
		workspace:  command.WorkspaceKey,
		agentID:    command.AgentID,
		revision:   command.ExpectedUpdatedAt,
		generation: command.GenerationID,
	})
	return ReconcileDesiredStateResult{}, stub.errs[command.WorkspaceKey+"/"+command.AgentID]
}

type agentsRuntimeAuthorityCall struct {
	component platformruntime.ComponentID
	workspace string
	action    authority.Action
}

type agentsRuntimeAuthority struct {
	calls []agentsRuntimeAuthorityCall
	errs  map[string]error
}

func (stub *agentsRuntimeAuthority) AuthorityForAgentsRuntime(
	_ context.Context,
	component platformruntime.ComponentID,
	workspace string,
	action authority.Action,
) (authority.SystemAuthority, error) {
	stub.calls = append(stub.calls, agentsRuntimeAuthorityCall{component: component, workspace: workspace, action: action})
	return authority.SystemAuthority{}, stub.errs[workspace]
}

type agentsRuntimeWorkspaces struct {
	values []string
	err    error
}

func (stub agentsRuntimeWorkspaces) ListWorkspaceKeys(context.Context) ([]string, error) {
	return stub.values, stub.err
}

func TestDesiredStateRuntimeUsesExactAuthorityAndAgentRevision(t *testing.T) {
	revision := time.Date(2026, 8, 2, 14, 0, 0, 0, time.UTC)
	deletedAt := revision.Add(-time.Hour)
	queries := &agentsRuntimeQueries{values: map[string][]*Agent{
		"ALPHA": {
			{AgentID: "agent-1", UpdatedAt: revision, GenerationID: "00112233445566778899aabbccddeeff"},
			nil,
			{AgentID: "deleted", UpdatedAt: revision, GenerationID: "ffeeddccbbaa99887766554433221100", DeletedAt: &deletedAt},
		},
		"ZETA": {{AgentID: "agent-2", UpdatedAt: revision.Add(time.Second), GenerationID: "0123456789abcdef0123456789abcdef"}},
	}}
	commands := &agentsRuntimeCommands{}
	provider := &agentsRuntimeAuthority{}
	registration, err := RuntimeRegistration(queries, commands, provider, RuntimeConfig{
		WorkspaceLister: agentsRuntimeWorkspaces{values: []string{"ZETA", "ALPHA"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Component.RunOnce(t.Context(), revision); err != nil {
		t.Fatal(err)
	}
	if registration.Component.ID() != DesiredStateReconciliationComponentID ||
		!registration.Policy.Immediate ||
		registration.Policy.Cadence != DesiredStateReconciliationCadence ||
		registration.Policy.Timeout != DesiredStateReconciliationTimeout ||
		registration.Policy.FailureBackoff.Initial != time.Second ||
		registration.Policy.FailureBackoff.Max != time.Minute ||
		registration.Policy.FailureBackoff.Multiplier != 2 {
		t.Fatalf("registration = %+v", registration)
	}
	wantAuthority := []agentsRuntimeAuthorityCall{
		{component: DesiredStateReconciliationComponentID, workspace: "ALPHA", action: ActionReconcileDesiredState},
		{component: DesiredStateReconciliationComponentID, workspace: "ZETA", action: ActionReconcileDesiredState},
	}
	if !reflect.DeepEqual(provider.calls, wantAuthority) {
		t.Fatalf("authority calls = %#v, want %#v", provider.calls, wantAuthority)
	}
	wantCommands := []agentsRuntimeCommandCall{
		{workspace: "ALPHA", agentID: "agent-1", revision: revision, generation: "00112233445566778899aabbccddeeff"},
		{workspace: "ZETA", agentID: "agent-2", revision: revision.Add(time.Second), generation: "0123456789abcdef0123456789abcdef"},
	}
	if !reflect.DeepEqual(commands.calls, wantCommands) {
		t.Fatalf("command calls = %#v, want %#v", commands.calls, wantCommands)
	}
}

func TestDesiredStateRuntimeContinuesAfterWorkspaceAndAgentFailures(t *testing.T) {
	listErr := errors.New("list")
	reconcileErr := errors.New("reconcile")
	authorityErr := errors.New("authority")
	revision := time.Now().UTC()
	queries := &agentsRuntimeQueries{
		values: map[string][]*Agent{
			"BETA":  {{AgentID: "agent-1", UpdatedAt: revision, GenerationID: "00112233445566778899aabbccddeeff"}},
			"GAMMA": {{AgentID: "agent-2", UpdatedAt: revision, GenerationID: "0123456789abcdef0123456789abcdef"}},
		},
		errs: map[string]error{"DELTA": listErr},
	}
	commands := &agentsRuntimeCommands{errs: map[string]error{"BETA/agent-1": reconcileErr}}
	provider := &agentsRuntimeAuthority{errs: map[string]error{"ALPHA": authorityErr}}
	registration, err := RuntimeRegistration(queries, commands, provider, RuntimeConfig{
		WorkspaceLister: agentsRuntimeWorkspaces{values: []string{"ALPHA", "BETA", "DELTA", "GAMMA"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = registration.Component.RunOnce(t.Context(), revision)
	if !errors.Is(err, authorityErr) || !errors.Is(err, listErr) || !errors.Is(err, reconcileErr) {
		t.Fatalf("RunOnce error = %v", err)
	}
	if len(provider.calls) != 4 || len(commands.calls) != 2 {
		t.Fatalf("authority calls=%d command calls=%d", len(provider.calls), len(commands.calls))
	}
}

func TestDesiredStateRuntimeRejectsInvalidCompositionAndWorkspaceState(t *testing.T) {
	queries := &agentsRuntimeQueries{}
	commands := &agentsRuntimeCommands{}
	provider := &agentsRuntimeAuthority{}
	if _, err := RuntimeRegistration(nil, commands, provider, RuntimeConfig{WorkspaceKey: "WS"}); err == nil {
		t.Fatal("expected nil queries error")
	}
	if _, err := RuntimeRegistration(queries, nil, provider, RuntimeConfig{WorkspaceKey: "WS"}); err == nil {
		t.Fatal("expected nil commands error")
	}
	if _, err := RuntimeRegistration(queries, commands, nil, RuntimeConfig{WorkspaceKey: "WS"}); err == nil {
		t.Fatal("expected nil authority error")
	}
	if _, err := RuntimeRegistration(queries, commands, provider, RuntimeConfig{}); err == nil {
		t.Fatal("expected missing workspace scope error")
	}
	for name, values := range map[string][]string{
		"blank": {"WS", ""}, "unclean": {" WS"}, "duplicate": {"WS", "WS"},
	} {
		t.Run(name, func(t *testing.T) {
			registration, err := RuntimeRegistration(queries, commands, provider, RuntimeConfig{
				WorkspaceLister: agentsRuntimeWorkspaces{values: values},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := registration.Component.RunOnce(t.Context(), time.Now()); err == nil {
				t.Fatal("expected invalid workspace list error")
			}
		})
	}
}
