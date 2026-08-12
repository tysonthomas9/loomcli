package artifacts

import "errors"

var (
	// ErrInvalid means an Artifacts command or execution owner is malformed.
	ErrInvalid = errors.New("artifacts: invalid request")
	// ErrNotFound means no artifact owned by the requesting execution exists.
	ErrNotFound = errors.New("artifacts: not found")
	// ErrAlreadyExists means a create attempted to reuse an occupied identity.
	ErrAlreadyExists = errors.New("artifacts: already exists")
	// ErrNotOwner means the execution lease does not own the requested operation.
	ErrNotOwner = errors.New("artifacts: not owner")
	// ErrInvalidTransition means the requested lifecycle change is not allowed.
	ErrInvalidTransition = errors.New("artifacts: invalid transition")
	// ErrUnavailable means the owner-scoped durable command port is unavailable.
	ErrUnavailable = errors.New("artifacts: unavailable")
	// ErrContentUnavailable means metadata is durable but the managed content
	// plane cannot currently serve the artifact bytes.
	ErrContentUnavailable = errors.New("artifacts: content unavailable")
	// ErrCaptureFailed means evidence could not be safely prepared or persisted.
	// The owning run/session outcome remains independent from this evidence fact.
	ErrCaptureFailed = errors.New("artifacts: evidence capture failed")
	// ErrEvidenceCorrupt means durable or candidate evidence does not conform to
	// the canonical Artifacts-owned format.
	ErrEvidenceCorrupt = errors.New("artifacts: evidence corrupt")
	// ErrInvalidPersistedState means durable storage returned data outside the
	// execution-scoped ownership contract.
	ErrInvalidPersistedState = errors.New("artifacts: invalid persisted state")
)
