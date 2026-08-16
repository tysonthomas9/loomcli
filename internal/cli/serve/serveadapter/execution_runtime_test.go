package serveadapter

import (
	"context"
	"sync"
	"testing"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type taskWorkerCapacityCapture struct {
	execution.TaskRunWorkerAPI
	mu         sync.Mutex
	capacities []int
}

func (capture *taskWorkerCapacityCapture) RegisterWorkerNode(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RegisterWorkerNodeCommand,
) (*execution.WorkerNode, error) {
	capture.mu.Lock()
	capture.capacities = append(capture.capacities, command.Capacity)
	capture.mu.Unlock()
	return &execution.WorkerNode{
		WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID,
		DrainState: execution.WorkerNodeActive, Capacity: command.Capacity,
	}, nil
}

func (*taskWorkerCapacityCapture) HeartbeatWorkerNode(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.HeartbeatWorkerNodeCommand,
) (*execution.WorkerNode, error) {
	return &execution.WorkerNode{
		WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID,
		DrainState: execution.WorkerNodeActive,
	}, nil
}

func (*taskWorkerCapacityCapture) SetWorkerNodeDrain(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.SetWorkerNodeDrainCommand,
) (*execution.WorkerNode, error) {
	return &execution.WorkerNode{
		WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID, DrainState: command.DrainState,
	}, nil
}

func (*taskWorkerCapacityCapture) ClaimTaskRun(
	context.Context,
	authority.SystemAuthority,
	execution.ClaimTaskRunCommand,
) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrNotFound
}

func (capture *taskWorkerCapacityCapture) registeredCapacities() []int {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]int(nil), capture.capacities...)
}

type taskWorkerCapacityAuthorities struct{}

func (taskWorkerCapacityAuthorities) ResolveExecutionSystemAuthority(
	context.Context,
	string,
	authority.Action,
	string,
) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

func (taskWorkerCapacityAuthorities) ResolveTaskRunAuthority(
	context.Context,
	string,
	authority.Action,
	execution.Owner,
) (authority.ExecutionAuthority, error) {
	return authority.ExecutionAuthority{}, nil
}

type taskWorkerCapacityArtifacts struct {
	artifactsmodule.API
}

type taskWorkerCapacityConvergence struct {
	execution.TaskRunConvergenceAPI
}

func TestBuildSharedNodeExecutionRuntimePassesUsesOneCapacity(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "TEST", Name: "Test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	capture := &taskWorkerCapacityCapture{}
	authorities := taskWorkerCapacityAuthorities{}
	template := driverexecutor.TaskWorker{
		Store: st, WorkspaceKey: "TEST", WorkDir: t.TempDir(), NodeID: "shared-task-worker-node",
		HeartbeatInterval: -1, Execution: capture, TaskRunAuthorities: authorities,
		ExecutionAuthorities: authorities, Convergence: &taskWorkerCapacityConvergence{},
		Artifacts: &taskWorkerCapacityArtifacts{},
	}
	executor := &driverexecutor.Executor{}

	driverPass, passes := BuildSharedNodeExecutionRuntimePasses(executor, template, 2)
	if driverPass == nil {
		t.Fatal("driver runtime pass is nil")
	}
	if executor.NodeCapacity != 2 {
		t.Fatalf("executor node capacity = %d, want 2", executor.NodeCapacity)
	}
	if len(passes) != 2 {
		t.Fatalf("runtime passes = %d, want 2", len(passes))
	}
	for index, pass := range passes {
		if err := pass.RunOnce(ctx); err != nil {
			t.Fatalf("runtime pass %d: %v", index+1, err)
		}
	}
	capacities := capture.registeredCapacities()
	if len(capacities) != 1 || capacities[0] != 2 {
		t.Fatalf("shared node registration capacities = %v, want [2]", capacities)
	}
}

func TestBuildSharedNodeExecutionRuntimePassesDefaultsCapacityToOne(t *testing.T) {
	executor := &driverexecutor.Executor{}
	driverPass, taskWorkerPasses := BuildSharedNodeExecutionRuntimePasses(executor, driverexecutor.TaskWorker{}, 0)
	if driverPass == nil {
		t.Fatal("driver runtime pass is nil")
	}
	if executor.NodeCapacity != 1 {
		t.Fatalf("executor node capacity = %d, want safe default 1", executor.NodeCapacity)
	}
	if len(taskWorkerPasses) != 1 {
		t.Fatalf("task worker runtime passes = %d, want safe default 1", len(taskWorkerPasses))
	}
}
