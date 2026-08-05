package execution

import "errors"

var (
	ErrInvalid         = errors.New("execution: invalid command")
	ErrUnavailable     = errors.New("execution: dependency unavailable")
	ErrConflict        = errors.New("execution: command conflict")
	ErrNotFound        = errors.New("execution: record not found")
	ErrFenceConflict   = errors.New("execution: owner fence conflict")
	ErrAlreadyResumed  = errors.New("execution: driver run already resumed for await")
	ErrPreflightFailed = errors.New("execution: preflight failed")
	ErrUnschedulable   = errors.New("execution: task run unschedulable")
	// ErrTaskRunRequestReplayNotFound is an internal read-only replay miss.
	// Request ports return it without writes so the service can run live
	// scheduling only for a genuinely new TaskRun request.
	ErrTaskRunRequestReplayNotFound = errors.New("execution: task run request replay not found")
	ErrLaunchFailed                 = errors.New("execution: launch failed")
	ErrInvalidTransition            = errors.New("execution: invalid transition")
	ErrCompositionDepthExceeded     = errors.New("execution: composition depth exceeded")
)
