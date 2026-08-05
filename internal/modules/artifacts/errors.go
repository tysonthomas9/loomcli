package artifacts

import "errors"

var (
	// ErrInvalid means an Artifacts command or execution owner is malformed.
	ErrInvalid = errors.New("artifacts: invalid request")
	// ErrNotFound means no artifact owned by the requesting execution exists.
	ErrNotFound = errors.New("artifacts: not found")
	// ErrAlreadyExists means a create attempted to reuse an occupied identity.
	ErrAlreadyExists = errors.New("artifacts: already exists")
	// ErrNotOwner means the execution lease does not own the requested operation.
	ErrNotOwner = errors.New("artifacts: not owner")
	// ErrInvalidTransition means the requested lifecycle change is not allowed.
	ErrInvalidTransition = errors.New("artifacts: invalid transition")
	// ErrUnavailable means the owner-scoped durable command port is unavailable.
	ErrUnavailable = errors.New("artifacts: unavailable")
	// ErrInvalidPersistedState means durable storage returned data outside the
	// execution-scoped ownership contract.
	ErrInvalidPersistedState = errors.New("artifacts: invalid persisted state")
)
