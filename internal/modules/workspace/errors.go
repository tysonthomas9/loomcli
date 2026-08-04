package workspace

import "errors"

var (
	ErrInvalid               = errors.New("invalid workspace query")
	ErrNotFound              = errors.New("workspace not found")
	ErrUnavailable           = errors.New("workspace catalog unavailable")
	ErrInvalidPersistedState = errors.New("invalid persisted workspace state")
)
