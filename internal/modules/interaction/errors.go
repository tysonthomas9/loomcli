package interaction

import "errors"

var (
	ErrInvalid               = errors.New("interaction: invalid")
	ErrUnavailable           = errors.New("interaction: unavailable")
	ErrNotFound              = errors.New("interaction: not found")
	ErrConflict              = errors.New("interaction: conflict")
	ErrNotOwner              = errors.New("interaction: not owner")
	ErrInvalidTransition     = errors.New("interaction: invalid transition")
	ErrInvalidPersistedState = errors.New("interaction: invalid persisted state")
	ErrTerminalCapacity      = errors.New("interaction: terminal capacity reached")
	ErrTerminalClosed        = errors.New("interaction: terminal runtime closed")
	ErrTerminalPlacement     = errors.New("interaction: terminal workspace unavailable")
	ErrAgentTerminalStopped  = errors.New("interaction: agent terminal stopped")
	ErrAgentTerminalWorker   = errors.New("interaction: background worker terminal")
	ErrTerminalLaunchMissing = errors.New("interaction: terminal launch metadata missing")
)
