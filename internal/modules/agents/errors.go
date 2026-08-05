package agents

import "errors"

var (
	ErrInvalid               = errors.New("agents: invalid input")
	ErrNotFound              = errors.New("agents: not found")
	ErrAlreadyExists         = errors.New("agents: already exists")
	ErrConflict              = errors.New("agents: conflict")
	ErrNotOwner              = errors.New("agents: not owner")
	ErrInvalidTransition     = errors.New("agents: invalid transition")
	ErrUnavailable           = errors.New("agents: unavailable")
	ErrInvalidPersistedState = errors.New("agents: invalid persisted state")
)
