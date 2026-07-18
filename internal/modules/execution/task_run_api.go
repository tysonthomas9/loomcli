package execution

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionRequestTaskRun        authority.Action = "execution.request-task-run"
	ActionClaimTaskRun          authority.Action = "execution.claim-task-run"
	ActionRequeueTaskRun        authority.Action = "execution.requeue-task-run"
	ActionExhaustTaskRunRetries authority.Action = "execution.exhaust-task-run-retries"
	ActionRegisterWorkerNode    authority.Action = "execution.register-worker-node"
	ActionHeartbeatWorkerNode   authority.Action = "execution.heartbeat-worker-node"
	ActionSetWorkerNodeDrain    authority.Action = "execution.set-worker-node-drain"
)

// TaskRunRequestAPI is the parent-run surface used by run-scoped exec-task.
type TaskRunRequestAPI interface {
	RequestTaskRun(context.Context, authority.ExecutionAuthority, RequestTaskRunCommand) (*TaskRun, error)
}

// TaskRunWorkerAPI is the system/owner-scoped surface used by TaskWorker. It
// intentionally exposes no composite Store and no Work Item mutation API.
type TaskRunWorkerAPI interface {
	ClaimTaskRun(context.Context, authority.SystemAuthority, ClaimTaskRunCommand) (ClaimTaskRunResult, error)
	Heartbeat(context.Context, authority.ExecutionAuthority, HeartbeatCommand) (HeartbeatResult, error)
	RequeueTaskRun(context.Context, authority.ExecutionAuthority, RequeueTaskRunCommand) (RequeueTaskRunResult, error)
	ExhaustTaskRunRetries(context.Context, authority.ExecutionAuthority, ExhaustTaskRunRetriesCommand) (ExhaustTaskRunRetriesResult, error)
	Finalize(context.Context, authority.ExecutionAuthority, FinalizeCommand) (FinalizeResult, error)
	RegisterWorkerNode(context.Context, authority.SystemAuthority, RegisterWorkerNodeCommand) (*WorkerNode, error)
	HeartbeatWorkerNode(context.Context, authority.SystemAuthority, HeartbeatWorkerNodeCommand) (*WorkerNode, error)
	SetWorkerNodeDrain(context.Context, authority.SystemAuthority, SetWorkerNodeDrainCommand) (*WorkerNode, error)
}

// TaskRunSchedulingAPI is deliberately read-only and narrow.
type TaskRunSchedulingAPI interface {
	CheckTaskRunScheduling(context.Context, TaskRunSchedulingQuery) (TaskRunSchedulingResult, error)
}
