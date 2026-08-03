package agents

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type reconciliationBindings struct {
	states []bool
	err    error
}

func (source *reconciliationBindings) ListAgentBindingStates(context.Context, string, string) ([]bool, error) {
	return append([]bool(nil), source.states...), source.err
}

type reconciliationLifecycle struct {
	mutation *ApplyLifecycleMutation
	result   *LifecycleResult
	err      error
}

func (store *reconciliationLifecycle) ApplyLifecycle(_ context.Context, mutation ApplyLifecycleMutation) (*LifecycleResult, error) {
	copy := mutation
	store.mutation = &copy
	return store.result, store.err
}

func TestReconcileDesiredStateNoOpsWhenBindingsMatch(t *testing.T) {
	service, issuer, lifecycle := newReconciliationService(t, []bool{true, true}, DesiredRunning)
	auth := issueSystem(t, issuer, "WS", string(DesiredStateReconciliationComponentID), ActionReconcileDesiredState)
	agent, err := service.GetAgent(t.Context(), "WS", "agt-docs")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileDesiredState(t.Context(), auth, ReconcileDesiredStateCommand{
		WorkspaceKey: "WS", AgentID: agent.AgentID, ExpectedUpdatedAt: agent.UpdatedAt, GenerationID: agent.GenerationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Converged || result.Repaired || lifecycle.mutation != nil {
		t.Fatalf("result/mutation = %+v/%+v", result, lifecycle.mutation)
	}
}

func TestReconcileDesiredStateRepairsDriftWithoutChangingIntent(t *testing.T) {
	service, issuer, lifecycle := newReconciliationService(t, []bool{false, true}, DesiredRunning)
	auth := issueSystem(t, issuer, "WS", string(DesiredStateReconciliationComponentID), ActionReconcileDesiredState)
	agent, err := service.GetAgent(t.Context(), "WS", "agt-docs")
	if err != nil {
		t.Fatal(err)
	}
	committed := agent.UpdatedAt.Add(time.Microsecond)
	lifecycle.result = &LifecycleResult{
		WorkspaceKey: "WS", AgentID: agent.AgentID, Action: LifecycleReconcile,
		Agent:       func() *Agent { copy := *agent; copy.UpdatedAt = committed; return &copy }(),
		CommittedAt: committed,
	}
	result, err := service.ReconcileDesiredState(t.Context(), auth, ReconcileDesiredStateCommand{
		WorkspaceKey: "WS", AgentID: agent.AgentID, ExpectedUpdatedAt: agent.UpdatedAt, GenerationID: agent.GenerationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Converged || !result.Repaired || lifecycle.mutation == nil ||
		lifecycle.mutation.Action != LifecycleReconcile || lifecycle.mutation.ChangedBy != string(DesiredStateReconciliationComponentID) {
		t.Fatalf("result/mutation = %+v/%+v", result, lifecycle.mutation)
	}
	if lifecycle.result.Agent.DesiredState != DesiredRunning {
		t.Fatalf("reconciliation changed desired state to %q", lifecycle.result.Agent.DesiredState)
	}
}

func newReconciliationService(t *testing.T, states []bool, desired DesiredState) (*Service, *authority.Issuer, *reconciliationLifecycle) {
	t.Helper()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	agent := testAgent(now, desired)
	ports.getAgent = func(context.Context, string, string) (*Agent, error) { return cloneAgent(agent), nil }
	ports.listAgents = func(context.Context, string, AgentFilter) ([]*Agent, error) { return []*Agent{cloneAgent(agent)}, nil }
	lifecycle := &reconciliationLifecycle{}
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewWithLifecycle(ports, ports, ports, ports, ports, ports, lifecycle, &reconciliationBindings{states: states}, admission)
	if err != nil {
		t.Fatal(err)
	}
	return service, issuer, lifecycle
}
