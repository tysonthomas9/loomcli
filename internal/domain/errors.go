// Package domain holds pure data types shared across loom packages.
//
// This package has no I/O dependencies — no HTTP clients, no database
// drivers, no filesystem. Everything else in loom may import it; it imports
// only the standard library. This rule keeps the domain shape stable and
// independently testable.
package domain

import "errors"

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

	// ErrUnavailable indicates a retryable failure to reach or serve a
	// required durable capability. Infrastructure may wrap this sentinel with
	// transport detail, but shared application and HTTP boundaries must not
	// depend on the persistence package to preserve a 503 classification.
	ErrUnavailable = errors.New("domain: temporarily unavailable")

	// ErrRateLimited indicates a retryable admission failure for which the
	// caller should apply bounded backoff. It is distinct from ErrUnavailable
	// so HTTP and SDK boundaries can preserve a 429 classification.
	ErrRateLimited = errors.New("domain: rate limited")

	// ErrGone indicates the entity exists but is no longer available —
	// e.g. a lease that has expired or been released (fleet-db 410
	// lease_expired). Distinct from ErrNotFound (never existed here) and
	// ErrConflict (someone else holds it): re-acquire is safe.
	ErrGone = errors.New("domain: gone")
)
