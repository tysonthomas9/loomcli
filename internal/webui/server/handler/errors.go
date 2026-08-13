package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

// kindToStatus maps each apperrors.ErrorKind to its HTTP status code.
// This must stay in sync with service/errors.go (task .9).
var kindToStatus = map[apperrors.ErrorKind]int{
	apperrors.KindNotFound:             http.StatusNotFound,
	apperrors.KindValidation:           http.StatusBadRequest,
	apperrors.KindUnavailable:          http.StatusServiceUnavailable,
	apperrors.KindTimeout:              http.StatusGatewayTimeout,
	apperrors.KindConflict:             http.StatusConflict,
	apperrors.KindInternal:             http.StatusInternalServerError,
	apperrors.KindForbidden:            http.StatusForbidden,
	apperrors.KindUnauthorized:         http.StatusUnauthorized,
	apperrors.KindLocked:               http.StatusLocked,
	apperrors.KindPayloadTooLarge:      http.StatusRequestEntityTooLarge,
	apperrors.KindRateLimited:          http.StatusTooManyRequests,
	apperrors.KindBadGateway:           http.StatusBadGateway,
	apperrors.KindNotImplemented:       http.StatusNotImplemented,
	apperrors.KindStarting:             http.StatusServiceUnavailable,
	apperrors.KindPreconditionFailed:   http.StatusPreconditionFailed,
	apperrors.KindPreconditionRequired: http.StatusPreconditionRequired,
}

// HandleServiceError extracts a *apperrors.ServiceError from err, maps its
// Kind to an HTTP status code, logs the full error, and writes a JSON error
// response. If err is not a *apperrors.ServiceError, it writes 500 with a
// generic message.
//
// The response body is always {"error": "<message>"}. The message comes from
// ServiceError.Message (not Error()) to avoid leaking cause chains to clients.
// StatusForKind returns the HTTP status code for the given ErrorKind.
// Unknown kinds return 500 Internal Server Error.
func StatusForKind(kind apperrors.ErrorKind) int {
	if status, ok := kindToStatus[kind]; ok {
		return status
	}
	return http.StatusInternalServerError
}

func HandleServiceError(w http.ResponseWriter, err error) {
	var svcErr *apperrors.ServiceError
	if errors.As(err, &svcErr) {
		status := StatusForKind(svcErr.Kind)
		slog.Error("service error",
			"kind", string(svcErr.Kind),
			"status", status,
			"msg", svcErr.Message,
			"err", err,
		)
		if svcErr.Kind == apperrors.KindStarting {
			w.Header().Set("Retry-After", "5")
		}
		// Include the kind in the body so frontends can branch on a
		// structured signal (e.g., "starting" vs. generic 503) instead of
		// substring-matching the English message.
		WriteJSON(w, status, map[string]string{
			"error": svcErr.Message,
			"kind":  string(svcErr.Kind),
		})
		return
	}
	slog.Error("unexpected error", "err", err)
	WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

// HandleWorkItemsError maps the Work Items capability's public failure
// vocabulary without translating it back through the legacy Web UI service.
func HandleWorkItemsError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := workitems.PublicErrorMessage(err)
	switch {
	case errors.Is(err, workitems.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, workitems.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, workitems.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, workitems.ErrUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, workitems.ErrTimeout):
		status = http.StatusGatewayTimeout
	case errors.Is(err, workitems.ErrNotImplemented):
		status = http.StatusNotImplemented
	}
	slog.Error("work items error", "status", status, "err", err)
	WriteJSON(w, status, map[string]string{"error": message})
}

// HandleWorkspaceError maps the Workspace capability's public failure
// vocabulary without translating it through the legacy Web UI service.
func HandleWorkspaceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := workspace.PublicErrorMessage(err)
	switch {
	case errors.Is(err, workspace.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, workspace.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, workspace.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, workspace.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	slog.Error("workspace error", "status", status, "err", err)
	WriteJSON(w, status, map[string]string{"error": message})
}

// HandleSourceControlError maps Source Control's transport-neutral failure
// vocabulary at the HTTP delivery seam.
func HandleSourceControlError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, sourcecontrol.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, sourcecontrol.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, sourcecontrol.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, sourcecontrol.ErrPayloadTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, sourcecontrol.ErrPreconditionFailed):
		status = http.StatusPreconditionFailed
	case errors.Is(err, sourcecontrol.ErrPreconditionRequired):
		status = http.StatusPreconditionRequired
	case errors.Is(err, sourcecontrol.ErrTimeout):
		status = http.StatusGatewayTimeout
	case errors.Is(err, sourcecontrol.ErrCheckoutConflict),
		errors.Is(err, sourcecontrol.ErrIdempotencyConflict):
		status = http.StatusConflict
	case errors.Is(err, sourcecontrol.ErrUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, sourcecontrol.ErrRemote):
		status = http.StatusBadGateway
	}
	slog.Error("source control error", "status", status, "err", err)
	WriteJSON(w, status, map[string]string{
		"error": sourcecontrol.PublicErrorMessage(err),
	})
}

// IsControlPlaneRateLimited reports whether a compatibility dependency
// preserved the durable control plane's retryable admission classification.
// Keeping this translation in the shared HTTP boundary lets feature handlers
// depend on capability APIs instead of importing persistence contracts.
func IsControlPlaneRateLimited(err error) bool {
	return errors.Is(err, domain.ErrRateLimited)
}

// IsControlPlaneUnavailable reports whether a compatibility dependency
// preserved a retryable control-plane availability failure.
func IsControlPlaneUnavailable(err error) bool {
	return errors.Is(err, domain.ErrUnavailable)
}

// WriteDomainError maps a domain.Err* sentinel to an HTTP status and writes a
// JSON {"error": ...} body. Store-direct handlers (roles, triggerbindings,
// webhooks, workflows) receive domain errors rather than apperrors.ServiceError,
// so they share this mapper instead of each re-deriving the table. fallback is
// the client message for ErrNotFound and unmapped errors.
func WriteDomainError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, domain.ErrInvalid):
		RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists):
		RespondError(w, http.StatusConflict, err.Error())
	default:
		RespondError(w, http.StatusInternalServerError, fallback)
	}
}
