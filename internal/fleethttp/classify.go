package fleethttp

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The shared status vocabulary.
//
// The two fleet-db clients map errors into different domains
// (backend.BackendError kinds vs domain.Err* sentinels), which is why the
// package header long said the classification "stays local". That freedom is
// how the same server response came to mean two different things: an HTTP 429
// was ErrConflict on the Store path (429 is 4xx, and the catch-all mapped
// every unmatched 4xx to conflict) and ErrUnavailable-with-a-"rate limited:"
// message-prefix on the IssueBackend path. Neither reached a caller as "you
// are being throttled, retry after N", and one of them surfaced in the web UI
// as a 5xx — a server-fault presentation for a routine backpressure signal.
//
// So the CLASS is shared here even though the error TYPES stay local: each
// client still constructs its own error, but from one table. A new status
// therefore cannot be classified two ways by accident, and the parity test
// fails if either client stops consulting it.

// Class is the transport-level meaning of a fleet-db response status,
// independent of either client's error type.
type Class int

const (
	// ClassUnknown means the table has no opinion; the caller keeps its own
	// fallback (deliberately not an error: unmapped statuses stay the
	// caller's business, they just stop being silently reinterpreted).
	ClassUnknown Class = iota
	ClassNotFound
	ClassValidation
	ClassConflict
	ClassForbidden
	ClassUnauthorized
	ClassGone
	// ClassRateLimited is a 429: the request was refused for backpressure,
	// not because anything about it was wrong. Retryable by construction,
	// which is why RetryAfter travels with it.
	ClassRateLimited
	ClassUnavailable
	ClassTimeout
	ClassInternal
)

// String makes Class printable in test failures and logs.
func (c Class) String() string {
	switch c {
	case ClassNotFound:
		return "not_found"
	case ClassValidation:
		return "validation"
	case ClassConflict:
		return "conflict"
	case ClassForbidden:
		return "forbidden"
	case ClassUnauthorized:
		return "unauthorized"
	case ClassGone:
		return "gone"
	case ClassRateLimited:
		return "rate_limited"
	case ClassUnavailable:
		return "unavailable"
	case ClassTimeout:
		return "timeout"
	case ClassInternal:
		return "internal"
	default:
		return "unknown"
	}
}

// statusClass is the single table. Both clients read it; neither keeps a
// private copy of a status it covers.
var statusClass = map[int]Class{
	http.StatusNotFound:            ClassNotFound,
	http.StatusBadRequest:          ClassValidation,
	http.StatusUnprocessableEntity: ClassValidation,
	http.StatusConflict:            ClassConflict,
	http.StatusForbidden:           ClassForbidden,
	http.StatusUnauthorized:        ClassUnauthorized,
	http.StatusGone:                ClassGone,
	http.StatusTooManyRequests:     ClassRateLimited,
	http.StatusServiceUnavailable:  ClassUnavailable,
	http.StatusGatewayTimeout:      ClassTimeout,
	http.StatusInternalServerError: ClassInternal,
	http.StatusBadGateway:          ClassInternal,
}

// ClassifyStatus returns the shared class for an HTTP status.
func ClassifyStatus(status int) Class {
	if c, ok := statusClass[status]; ok {
		return c
	}
	return ClassUnknown
}

// ClassifiedStatuses returns every status the table covers, for parity tests.
func ClassifiedStatuses() map[int]Class {
	out := make(map[int]Class, len(statusClass))
	for k, v := range statusClass {
		out[k] = v
	}
	return out
}

// RetryAfter parses the Retry-After header in both forms RFC 9110 allows:
// delay-seconds ("30") and an HTTP-date. Returns 0 when absent or
// unparseable, and never a negative duration — a date already in the past
// means "retry now", not "retry in the past".
//
// now is injected so the date branch is testable; pass time.Now().
func RetryAfter(h http.Header, now time.Time) time.Duration {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
