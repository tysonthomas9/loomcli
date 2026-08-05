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
)
