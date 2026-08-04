package workspace

import (
	"errors"
	"strings"
)

var (
	ErrInvalid               = errors.New("invalid workspace query")
	ErrNotFound              = errors.New("workspace not found")
	ErrConflict              = errors.New("workspace conflict")
	ErrUnavailable           = errors.New("workspace catalog unavailable")
	ErrInvalidPersistedState = errors.New("invalid persisted workspace state")
)

// PublicErrorMessage removes the classification sentinel while preserving a
// capability-owned explanation that is safe for an HTTP response.
func PublicErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, sentinel := range []error{ErrInvalid, ErrNotFound, ErrConflict, ErrUnavailable} {
		if errors.Is(err, sentinel) {
			return strings.TrimSuffix(message, ": "+sentinel.Error())
		}
	}
	return "internal server error"
}
