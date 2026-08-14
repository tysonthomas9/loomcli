package sessionarchive

import (
	"errors"
	"fmt"
)

var (
	ErrInvalid               = errors.New("session archive: invalid query")
	ErrNotFound              = errors.New("session archive: not found")
	ErrUnavailable           = errors.New("session archive: unavailable")
	ErrInvalidPersistedState = errors.New("session archive: invalid persisted state")
)

// failure keeps the query projection's transport-neutral classification,
// client-safe message, and diagnostic cause separate. Delivery adapters map
// the sentinel to their protocol and must never serialize Error directly.
type failure struct {
	kind    error
	message string
	cause   error
}

func (failure *failure) Error() string {
	if failure.cause != nil {
		return fmt.Sprintf("%s: %v", failure.message, failure.cause)
	}
	return failure.message
}

func (failure *failure) Unwrap() []error {
	if failure.cause == nil {
		return []error{failure.kind}
	}
	return []error{failure.kind, failure.cause}
}

func queryError(kind error, message string, cause error) error {
	return &failure{kind: kind, message: message, cause: cause}
}

// PublicErrorMessage returns the projection's client-safe description without
// exposing owner, persistence, or evidence-adapter diagnostics.
func PublicErrorMessage(err error) string {
	var queryFailure *failure
	if errors.As(err, &queryFailure) {
		return queryFailure.message
	}
	return "session archive operation failed"
}
