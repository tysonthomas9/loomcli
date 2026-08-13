package execution

import "context"

type TaskRunDependencies struct {
	Queries         TaskRunQueryPort
	Requests        TaskRunRequestPort
	Claims          TaskRunClaimPort
	WorkItemDesign  TaskRunWorkItemDesignPort
	Requeues        TaskRunRequeuePort
	RetryExhaustion TaskRunRetryExhaustionPort
	Scheduling      TaskRunSchedulingQueryPort
}

// TaskRunQueryPort is the owner-private read seam for one canonical TaskRun
// snapshot. It never returns the raw lease credential.
type TaskRunQueryPort interface {
	GetTaskRun(context.Context, string, string) (*TaskRun, error)
	ListTaskRuns(context.Context, TaskRunArchiveQuery) ([]*TaskRun, error)
	ListActiveTaskRuns(context.Context, ActiveTaskRunQuery) ([]*TaskRun, error)
	ListTaskRunEvents(context.Context, TaskRunEventQuery) ([]*TaskRunEvent, error)
}

// TaskRunRequestPort owns the idempotent queued TaskRun + DriverStep link +
// queued-event application operation. Its implementation may call one atomic
// backend command or a durable process manager; it must not silently degrade
// to an untracked best-effort linkage.
type TaskRunRequestPort interface {
	// ReplayTaskRunRequest is a read-only probe for an already-committed
	// request. It returns ErrTaskRunRequestReplayNotFound without writing when
	// no exact idempotency receipt exists.
	ReplayTaskRunRequest(context.Context, RequestTaskRunCommand) (RequestTaskRunResult, error)
	RequestTaskRun(context.Context, RequestTaskRunCommand) (RequestTaskRunResult, error)
}

// TaskRunClaimPort invokes the authoritative claim-and-start transaction.
type TaskRunClaimPort interface {
	ClaimTaskRun(context.Context, ClaimTaskRunCommand) (ClaimTaskRunResult, error)
}

// TaskRunWorkItemDesignPort owns the atomic, owner-fenced update of the Work
// Item design bound to a running TaskRun. Implementations derive the Work Item
// from the authoritative TaskRun record; callers never select an Issue ID.
type TaskRunWorkItemDesignPort interface {
	UpdateTaskRunWorkItemDesign(context.Context, UpdateTaskRunWorkItemDesignCommand) (UpdateTaskRunWorkItemDesignResult, error)
}

type TaskRunRequeuePort interface {
	RequeueTaskRun(context.Context, RequeueTaskRunCommand) (RequeueTaskRunResult, error)
}

// TaskRunRetryExhaustionPort is one atomic TaskRun + linked Work Item
// transition. A TaskRun-only Finish implementation does not satisfy it.
type TaskRunRetryExhaustionPort interface {
	ExhaustTaskRunRetries(context.Context, ExhaustTaskRunRetriesCommand) (ExhaustTaskRunRetriesResult, error)
}

type TaskRunSchedulingQueryPort interface {
	CheckTaskRunScheduling(context.Context, TaskRunSchedulingQuery) (TaskRunSchedulingResult, error)
}

type WorkerDependencies struct {
	Registration WorkerNodeRegistrationPort
	Heartbeats   WorkerNodeHeartbeatPort
	Drain        WorkerNodeDrainPort
	Profiles     WorkerProfilePort
}

type WorkerProfilePort interface {
	WorkerProfileQueryPort
	WorkerProfileMutationPort
}

type WorkerProfileQueryPort interface {
	GetWorkerProfile(context.Context, string, string) (*WorkerProfile, error)
	ListWorkerProfiles(context.Context, string, WorkerProfileFilter) ([]*WorkerProfile, error)
}

type WorkerNodeRegistrationPort interface {
	RegisterWorkerNode(context.Context, RegisterWorkerNodeCommand) (*WorkerNode, error)
}

type WorkerNodeHeartbeatPort interface {
	HeartbeatWorkerNode(context.Context, HeartbeatWorkerNodeCommand) (*WorkerNode, error)
}

type WorkerNodeDrainPort interface {
	SetWorkerNodeDrain(context.Context, SetWorkerNodeDrainCommand) (*WorkerNode, error)
}
