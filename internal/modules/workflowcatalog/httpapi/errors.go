package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	errorCodeInvalidRequest        = "invalid_request"
	errorCodeUnauthenticated       = "unauthenticated"
	errorCodeForbidden             = "forbidden"
	errorCodeNotFound              = "not_found"
	errorCodeVersionOwnership      = "version_ownership_conflict"
	errorCodeStaleRevision         = "stale_revision"
	errorCodeVersionNotValidated   = "version_not_validated"
	errorCodeVersionNotApproved    = "version_not_approved"
	errorCodeUnavailable           = "unavailable"
	errorCodeInvalidPersistedState = "invalid_persisted_state"
	errorCodeInternal              = "internal_error"
)

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeMappedError(w http.ResponseWriter, err error) {
	status, code, message := classifyError(err)
	writeError(w, status, code, message)
}

func classifyError(err error) (int, string, string) {
	var admissionErr *authority.AdmissionError
	if errors.As(err, &admissionErr) {
		switch admissionErr.Reason {
		case authority.DenialInvalidAuthority, authority.DenialExpired:
			return http.StatusUnauthorized, errorCodeUnauthenticated, "authentication required"
		default:
			return http.StatusForbidden, errorCodeForbidden, "forbidden"
		}
	}
	switch {
	case errors.Is(err, ErrUnauthenticated),
		errors.Is(err, authority.ErrInvalidPrincipal),
		errors.Is(err, authority.ErrInvalidOperatorToken),
		errors.Is(err, authority.ErrPrincipalExpired),
		errors.Is(err, authority.ErrOpaqueAuthority):
		return http.StatusUnauthorized, errorCodeUnauthenticated, "authentication required"
	case errors.Is(err, authority.ErrAdmissionDenied),
		errors.Is(err, authority.ErrWorkspaceMismatch),
		errors.Is(err, authority.ErrPrincipalClass),
		errors.Is(err, authority.ErrActionNotAllowed),
		errors.Is(err, workflowcatalog.ErrWrongWorkspace):
		return http.StatusForbidden, errorCodeForbidden, "forbidden"
	case errors.Is(err, workflowcatalog.ErrInvalid):
		return http.StatusBadRequest, errorCodeInvalidRequest, "invalid workflow catalog request"
	case errors.Is(err, workflowcatalog.ErrNotFound):
		return http.StatusNotFound, errorCodeNotFound, "workflow catalog resource not found"
	case errors.Is(err, workflowcatalog.ErrVersionOwnership):
		return http.StatusConflict, errorCodeVersionOwnership, "workflow version belongs to another driver"
	case errors.Is(err, workflowcatalog.ErrStaleRevision):
		return http.StatusConflict, errorCodeStaleRevision, "workflow driver revision is stale"
	case errors.Is(err, workflowcatalog.ErrVersionNotValidated):
		return http.StatusPreconditionFailed, errorCodeVersionNotValidated, "workflow version has not passed validation"
	case errors.Is(err, workflowcatalog.ErrVersionNotApproved):
		return http.StatusPreconditionFailed, errorCodeVersionNotApproved, "workflow version is not approved"
	case errors.Is(err, workflowcatalog.ErrUnavailable):
		return http.StatusServiceUnavailable, errorCodeUnavailable, "workflow catalog is unavailable"
	case errors.Is(err, workflowcatalog.ErrInvalidPersistedState):
		return http.StatusBadGateway, errorCodeInvalidPersistedState, "workflow catalog returned invalid state"
	default:
		return http.StatusInternalServerError, errorCodeInternal, "internal server error"
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
