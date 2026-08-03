package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// FleetDB command identities are capped at 128 bytes.
const requestTaskRunRequestIDMaxLength = 128

// RequestedTaskRunID derives the stable child-run identity used when a
// workflow omits taskRunId. Replaying the same parent/work-item request after
// a lost response therefore reaches the same queued TaskRun.
func RequestedTaskRunID(parentRunID, workItemID string) string {
	digest := sha256.Sum256([]byte("loom-task-run-request\x00" + parentRunID + "\x00" + workItemID))
	return "task-run-" + hex.EncodeToString(digest[:16])
}

// RequestedDriverStepID binds the structured DriverStep link to the exact
// parent and TaskRun identities. Caller-supplied TaskRun IDs remain supported
// while the step identity stays deterministic across retries.
func RequestedDriverStepID(parentRunID, taskRunID string) string {
	digest := sha256.Sum256([]byte("loom-driver-step-request\x00" + parentRunID + "\x00" + taskRunID))
	return "step-" + hex.EncodeToString(digest[:16])
}

func RequestTaskRunRequestID(parentRunID, taskRunID string) string {
	digest := sha256.Sum256([]byte("loom-exec-task-request\x00" + parentRunID + "\x00" + taskRunID))
	return "exec-task:sha256:" + hex.EncodeToString(digest[:])
}

const (
	ActionRequestTaskRun              authority.Action = "execution.request-task-run"
	ActionClaimTaskRun                authority.Action = "execution.claim-task-run"
	ActionUpdateTaskRunWorkItemDesign authority.Action = "execution.update-task-run-work-item-design"
	ActionRequeueTaskRun              authority.Action = "execution.requeue-task-run"
	ActionExhaustTaskRunRetries       authority.Action = "execution.exhaust-task-run-retries"
	ActionRegisterWorkerNode          authority.Action = "execution.register-worker-node"
	ActionHeartbeatWorkerNode         authority.Action = "execution.heartbeat-worker-node"
	ActionSetWorkerNodeDrain          authority.Action = "execution.set-worker-node-drain"
)

const taskRunWorkItemDesignActionPrefix = "task-run-work-item-design-update:"

// TaskRunWorkItemDesignActionID is the durable receipt identity shared by the
// Execution application boundary and its FleetDB adapter.
func TaskRunWorkItemDesignActionID(requestID string) string {
	return taskRunWorkItemDesignActionPrefix + requestID
}

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
