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
)
