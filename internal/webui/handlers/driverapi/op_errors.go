package driverapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// opError is the structured v2 error envelope. The shape is FROZEN as the
// SDK v1 contract (sdk/api-surface.v1.json): {code, message, retryable}
// with an OPTIONAL additive details object for machine-readable context.
type opError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOpError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeOpErrorDetails(w, status, code, message, retryable, nil)
}

// writeOpErrorDetails writes the envelope with the optional details object;
// details is additive — clients that predate it ignore the extra key.
func writeOpErrorDetails(w http.ResponseWriter, status int, code, message string, retryable bool, details map[string]any) {
	writeJSON(w, status, map[string]any{"error": opError{Code: code, Message: message, Retryable: retryable, Details: details}})
}

func writeCodedOpError(w http.ResponseWriter, err error) bool {
	var coded *codedOpError
	if !errors.As(err, &coded) {
		return false
	}
	writeOpErrorDetails(w, coded.status, coded.code, coded.Error(), coded.retryable, coded.details)
	return true
}

func writeSpecializedOpError(w http.ResponseWriter, err error) bool {
	return writeConnectorOpError(w, err) || writeAwaitOpError(w, err) || writeCodedOpError(w, err)
}

// writeDomainOpError maps domain sentinel errors onto the structured error
// envelope. Defaults to a non-retryable internal error: only transient
// classes (timeouts, cancellation) advertise retryability.
func writeDomainOpError(w http.ResponseWriter, err error) {
	if writeSpecializedOpError(w, err) || writeBackendDomainOpError(w, err) ||
		writeAutomationDomainOpError(w, err) ||
		writeInteractionDomainOpError(w, err) ||
		writeExecutionDomainOpError(w, err) {
		return
	}
	writeBaseDomainOpError(w, err)
}

func writeAutomationDomainOpError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, workfloweventing.ErrInvalidRequest), errors.Is(err, automation.ErrInvalid), errors.Is(err, automation.ErrWrongWorkspace):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, automation.ErrNotFound), errors.Is(err, automation.ErrNoMatchingBinding), errors.Is(err, automation.ErrParentEventNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, automation.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, workfloweventing.ErrUnavailable), errors.Is(err, automation.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), true)
	default:
		return false
	}
	return true
}

func writeInteractionDomainOpError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, interaction.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, interaction.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, interaction.ErrNotOwner):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, interaction.ErrInvalidTransition):
		writeOpError(
			w,
			http.StatusConflict,
			"invalid_transition",
			err.Error(),
			false,
		)
	case errors.Is(err, interaction.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, interaction.ErrUnavailable):
		writeOpError(
			w,
			http.StatusServiceUnavailable,
			"unavailable",
			err.Error(),
			true,
		)
	case errors.Is(err, interaction.ErrInvalidPersistedState):
		writeOpError(
			w,
			http.StatusInternalServerError,
			"internal",
			err.Error(),
			false,
		)
	default:
		return false
	}
	return true
}

func writeExecutionDomainOpError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, execution.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, execution.ErrFenceConflict), errors.Is(err, authority.ErrInvalidScope):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, execution.ErrUnschedulable):
		writeOpError(w, http.StatusConflict, "unschedulable", err.Error(), true)
	case errors.Is(err, execution.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, execution.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, execution.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, execution.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), true)
	default:
		return false
	}
	return true
}

func writeBaseDomainOpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, domain.ErrNotOwner):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, domain.ErrUnschedulable):
		writeOpError(w, http.StatusConflict, "unschedulable", err.Error(), true)
	case errors.Is(err, domain.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyClaimed):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, domain.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, context.DeadlineExceeded):
		writeOpError(w, http.StatusGatewayTimeout, "timeout", err.Error(), true)
	case errors.Is(err, context.Canceled):
		writeOpError(w, 499, "canceled", err.Error(), true)
	default:
		writeOpError(w, http.StatusInternalServerError, "internal", err.Error(), false)
	}
}
