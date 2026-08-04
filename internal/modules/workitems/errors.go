package workitems

import (
	"errors"
	"strings"
)

var (
	ErrInvalid               = errors.New("invalid work item command")
	ErrNotFound              = errors.New("work item not found")
	ErrConflict              = errors.New("work item conflict")
	ErrUnavailable           = errors.New("work items unavailable")
	ErrTimeout               = errors.New("work item operation timed out")
	ErrInvalidPersistedState = errors.New("invalid persisted work item state")
)

// PublicErrorMessage removes the classification sentinel from a public error
// while preserving its safe, capability-owned explanation.
func PublicErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, sentinel := range []error{ErrInvalid, ErrNotFound, ErrConflict, ErrUnavailable, ErrTimeout} {
		if errors.Is(err, sentinel) {
			return strings.TrimSuffix(message, ": "+sentinel.Error())
		}
	}
	return "internal server error"
}
