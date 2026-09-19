// Package domain holds pure data types shared across loom packages.
//
// This package has no I/O dependencies — no HTTP clients, no database
// drivers, no filesystem. Everything else in loom may import it; it imports
// only the standard library. This rule keeps the domain shape stable and
// independently testable.
package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by Store implementations and consumed by callers
// via errors.Is. Implementations wrap these with context-specific detail
// using fmt.Errorf("...: %w", ErrXxx).
var (
	// ErrNotFound indicates the requested entity does not exist.
	ErrNotFound = errors.New("domain: not found")

	// ErrAlreadyExists indicates a Create call collided with an existing
	// entity of the same identity.
	ErrAlreadyExists = errors.New("domain: already exists")

	// ErrInvalid indicates the provided value violated a domain invariant
	// (e.g., empty required field, malformed ID, out-of-range enum).
	ErrInvalid = errors.New("domain: invalid value")

	// ErrConflict indicates an optimistic-lock or precondition failure
	// (e.g., concurrent update, dependency violation).
	ErrConflict = errors.New("domain: conflict")

	// ErrAlreadyClaimed indicates a queued unit of work has already been
	// admitted by another owner.
	ErrAlreadyClaimed = errors.New("domain: already claimed")

	// ErrNotOwner indicates an owner-scoped operation was attempted by a
	// different node or worker.
	ErrNotOwner = errors.New("domain: not owner")

	// ErrInvalidTransition indicates a status transition is not allowed from
	// the current lifecycle state.
	ErrInvalidTransition = errors.New("domain: invalid status transition")

	// ErrUnschedulable indicates a valid unit of work cannot currently be
	// placed on any live node with the required provider/capabilities.
	ErrUnschedulable = errors.New("domain: unschedulable")

	// ErrGone indicates the entity exists but is no longer available —
	// e.g. a lease that has expired or been released (fleet-db 410
	// lease_expired). Distinct from ErrNotFound (never existed here) and
	// ErrConflict (someone else holds it): re-acquire is safe.
	ErrGone = errors.New("domain: gone")

	// ErrRateLimited indicates the request was refused for backpressure
	// (fleet-db 429), not because anything about it was wrong. It is
	// retryable by construction and deliberately NOT ErrConflict: a
	// conflict says "someone else won", a rate limit says "ask again
	// shortly". The Store client's catch-all mapped every unmatched 4xx to
	// ErrConflict, which made throttling indistinguishable from losing a
	// race. Callers that want the server's pacing hint read it with
	// RateLimitRetryAfter.
	ErrRateLimited = errors.New("domain: rate limited")
)

// RateLimitError carries the server's pacing hint alongside ErrRateLimited.
// It exists because a bare sentinel cannot answer "how long?" — and a caller
// that guesses a backoff for a server that already told it the answer will
// guess wrong in one of the two costly directions.
type RateLimitError struct {
	// RetryAfter is the upstream Retry-After header verbatim — delta-seconds
	// ("30") or an HTTP-date, exactly as RFC 9110 allows both. Empty means
	// the server did not say, which is different from "retry immediately":
	// the caller's own backoff policy owns that case.
	//
	// Kept verbatim rather than parsed so it can be propagated unchanged to
	// an HTTP client, which is the only consumer that needs it and the one
	// place where re-rendering a parsed duration would lose the date form.
	RetryAfter string
	// Detail is the transport-level description (method, path, status, body
	// message) for logs.
	Detail string
}

func (e *RateLimitError) Error() string {
	switch {
	case e.Detail != "" && e.RetryAfter != "":
		return fmt.Sprintf("%s: %s (retry after %s)", ErrRateLimited, e.Detail, e.RetryAfter)
	case e.Detail != "":
		return fmt.Sprintf("%s: %s", ErrRateLimited, e.Detail)
	default:
		return ErrRateLimited.Error()
	}
}

// Unwrap makes errors.Is(err, ErrRateLimited) true for a *RateLimitError, so
// callers that only care about the class need no type assertion.
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// RateLimitRetryAfter returns the server's pacing hint carried by err, and
// whether err is a rate-limit error at all. A rate limit with no hint
// returns ("", true) — the class is known, the interval is not.
func RateLimitRetryAfter(err error) (string, bool) {
	if !errors.Is(err, ErrRateLimited) {
		return "", false
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return rl.RetryAfter, true
	}
	return "", true
}
