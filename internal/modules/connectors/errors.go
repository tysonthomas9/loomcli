package connectors

import "errors"

var (
	// ErrInvalid means the repository- and operation-scoped request is
	// malformed or attempts to carry authority in its remote URL.
	ErrInvalid = errors.New("connectors: invalid request")
	// ErrUnavailable means the credential broker or its bounded Git executor
	// was not composed.
	ErrUnavailable = errors.New("connectors: unavailable")
	// ErrUnsupportedOperation means the request is not one of the explicitly
	// read-only Git operations admitted by this minimal broker.
	ErrUnsupportedOperation = errors.New("connectors: unsupported operation")
	// ErrIdempotencyConflict means an operation ID was already bound to a
	// different immutable repository/checkout coordinate tuple.
	ErrIdempotencyConflict = errors.New("connectors: idempotency conflict")
	// ErrGrantConflict means a durable GrantID already exists but is not the
	// exact immutable grant requested, or a create race could not be resolved
	// to that exact active grant.
	ErrGrantConflict = errors.New("connectors: grant conflict")
	// ErrNotFound means a Connector or binding referenced by a grant does not
	// exist in the workspace.
	ErrNotFound = errors.New("connectors: not found")
	// ErrConflict means a requested definition or grant identity already
	// exists with incompatible state.
	ErrConflict = errors.New("connectors: conflict")
	// ErrGrantRevoked means the requested grant was already revoked.
	ErrGrantRevoked = errors.New("connectors: grant revoked")
	// ErrInvalidPersistedState means Fleet returned a malformed, cross-scope,
	// revoked, duplicate, or otherwise contradictory grant projection.
	ErrInvalidPersistedState = errors.New("connectors: invalid persisted state")
)
