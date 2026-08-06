package workspacecoord

import "github.com/tysonthomas9/loomcli/internal/webui/apperrors"

// ServiceError is the shared transport-neutral error envelope understood by
// the Web UI HTTP boundary. Workspace coordination owns no HTTP policy.
type ServiceError = apperrors.ServiceError

const (
	KindUnavailable = apperrors.KindUnavailable
	KindConflict    = apperrors.KindConflict
	KindValidation  = apperrors.KindValidation
	KindNotFound    = apperrors.KindNotFound
)

var (
	ErrNotFound    = apperrors.ErrNotFound
	ErrValidation  = apperrors.ErrValidation
	ErrUnavailable = apperrors.ErrUnavailable
	ErrTimeout     = apperrors.ErrTimeout
	ErrConflict    = apperrors.ErrConflict
	ErrInternal    = apperrors.ErrInternal
	ErrForbidden   = apperrors.ErrForbidden
)
