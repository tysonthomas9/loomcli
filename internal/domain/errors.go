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

	// ErrGone indicates the entity exists but is no longer available —
	// e.g. a lease that has expired or been released (fleet-db 410
	// lease_expired). Distinct from ErrNotFound (never existed here) and
	// ErrConflict (someone else holds it): re-acquire is safe.
	ErrGone = errors.New("domain: gone")
)
