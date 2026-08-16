// Package persistence defines the backend-format error classes shared by
// Loom's local test adapters and FleetDB transport adapters. Capability
// modules map these mechanical classes to their own operation errors at their
// owning seams.
package persistence

import "errors"

var (
	ErrNotFound          = errors.New("persistence: not found")
	ErrAlreadyExists     = errors.New("persistence: already exists")
	ErrInvalid           = errors.New("persistence: invalid value")
	ErrConflict          = errors.New("persistence: conflict")
	ErrAlreadyClaimed    = errors.New("persistence: already claimed")
	ErrNotOwner          = errors.New("persistence: not owner")
	ErrInvalidTransition = errors.New("persistence: invalid status transition")
	ErrUnschedulable     = errors.New("persistence: unschedulable")
	ErrUnavailable       = errors.New("persistence: temporarily unavailable")
	ErrRateLimited       = errors.New("persistence: rate limited")
	ErrGone              = errors.New("persistence: gone")
)
