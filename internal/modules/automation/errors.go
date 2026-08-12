package automation

import "errors"

var (
	ErrInvalid               = errors.New("automation: invalid input")
	ErrNotFound              = errors.New("automation: not found")
	ErrUnavailable           = errors.New("automation: unavailable")
	ErrConflict              = errors.New("automation: conflict")
	ErrWrongWorkspace        = errors.New("automation: wrong workspace")
	ErrInvalidPersistedState = errors.New("automation: invalid persisted state")
	ErrBindingEnabled        = errors.New("automation: binding must be disabled")
	ErrManagedBinding        = errors.New("automation: binding is managed by an agent service")
	ErrNoMatchingBinding     = errors.New("automation: no matching binding")
	ErrParentEventNotFound   = errors.New("automation: parent event not found")
	ErrHopDepthExceeded      = errors.New("automation: event hop depth exceeded")
	ErrExecutionBusy         = errors.New("automation: execution is busy")
	// ErrDispatchReplayNotFound is an internal control-flow miss from the
	// replay-only Execution probe; HTTP callers never receive it directly.
	ErrDispatchReplayNotFound = errors.New("automation: dispatch replay not found")
	// ErrAdmissionReplayNotFound is internal control flow from the replay-only
	// durable admission probe; it is never returned from the public command.
	ErrAdmissionReplayNotFound = errors.New("automation: admission replay not found")
)
