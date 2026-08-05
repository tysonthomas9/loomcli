package workflowcatalog

import "errors"

var (
	ErrInvalid               = errors.New("workflow catalog: invalid input")
	ErrNotFound              = errors.New("workflow catalog: not found")
	ErrUnavailable           = errors.New("workflow catalog: unavailable")
	ErrWrongWorkspace        = errors.New("workflow catalog: wrong workspace")
	ErrVersionOwnership      = errors.New("workflow catalog: version belongs to another driver")
	ErrVersionNotValidated   = errors.New("workflow catalog: version validation has not passed")
	ErrVersionNotApproved    = errors.New("workflow catalog: version is not approved")
	ErrStaleRevision         = errors.New("workflow catalog: stale driver revision")
	ErrInvalidPersistedState = errors.New("workflow catalog: invalid persisted state")
)
