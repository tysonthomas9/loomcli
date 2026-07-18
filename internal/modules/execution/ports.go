package execution

import "context"

// PreflightPort validates runner/backend/capability readiness without claiming
// product state.
type PreflightPort interface {
	Preflight(context.Context, PreflightCommand) (PreflightResult, error)
}

// ClaimStartPort owns the atomic Work Item claim plus Execution start record.
// RecordLaunchFailure is owner-fenced and idempotent by the original RequestID.
type ClaimStartPort interface {
	ClaimAndStart(context.Context, ClaimAndLaunchCommand) (ClaimStart, error)
	RecordLaunchFailure(context.Context, ClaimStart, ExitClassification) error
}

// Launcher performs the external side effect only after ClaimAndStart commits.
type Launcher interface {
	Launch(context.Context, ClaimStart, []byte) (LaunchReceipt, error)
}

type HeartbeatPort interface {
	Heartbeat(context.Context, HeartbeatCommand) (HeartbeatResult, error)
}

type LogPort interface {
	AppendLog(context.Context, AppendLogCommand) (LogEntry, error)
}

type Classifier interface {
	Classify(context.Context, ClassifyCommand) (ExitClassification, error)
}

type FinalizePort interface {
	Finalize(context.Context, FinalizeCommand) (FinalizeResult, error)
}

type RecoveryPort interface {
	Recover(context.Context, RecoverCommand) (RecoverResult, error)
}

type AwaitPort interface {
	Register(context.Context, AwaitCommand) (AwaitResult, error)
}
