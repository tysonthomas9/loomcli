package driverapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
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
	if writeSpecializedOpError(w, err) || writeWorkItemsDomainOpError(w, err) ||
		writeAgentsDomainOpError(w, err) ||
		writeWorkspaceDomainOpError(w, err) ||
		writeAutomationDomainOpError(w, err) ||
		writeInteractionDomainOpError(w, err) ||
		writeExecutionDomainOpError(w, err) {
		return
	}
	writeBaseDomainOpError(w, err)
}

func writeWorkspaceDomainOpError(w http.ResponseWriter, err error) bool {
	message := workspace.PublicErrorMessage(err)
	switch {
	case errors.Is(err, workspace.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", message, false)
	case errors.Is(err, workspace.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", message, false)
	case errors.Is(err, workspace.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", message, false)
	case errors.Is(err, workspace.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", message, true)
	case errors.Is(err, workspace.ErrInvalidPersistedState):
		writeOpError(w, http.StatusInternalServerError, "internal", "internal server error", false)
	default:
		return false
	}
	return true
}

func writeAgentsDomainOpError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, agents.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, agents.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, agents.ErrAlreadyExists), errors.Is(err, agents.ErrConflict), errors.Is(err, agents.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, agents.ErrNotOwner):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, agents.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), true)
	case errors.Is(err, agents.ErrInvalidPersistedState):
		writeOpError(w, http.StatusInternalServerError, "internal", "internal server error", false)
	default:
		return false
	}
	return true
}

func writeWorkItemsDomainOpError(w http.ResponseWriter, err error) bool {
	message := workitems.PublicErrorMessage(err)
	switch {
	case errors.Is(err, workitems.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", message, false)
	case errors.Is(err, workitems.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", message, false)
	case errors.Is(err, workitems.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", message, false)
	case errors.Is(err, workitems.ErrNotImplemented):
		writeOpError(w, http.StatusNotImplemented, "not_implemented", message, false)
	case errors.Is(err, workitems.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", message, true)
	case errors.Is(err, workitems.ErrTimeout):
		writeOpError(w, http.StatusGatewayTimeout, "timeout", message, true)
	case errors.Is(err, workitems.ErrInvalidPersistedState):
		writeOpError(w, http.StatusInternalServerError, "internal", "internal server error", false)
	default:
		return false
	}
	return true
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
	case errors.Is(err, persistence.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, persistence.ErrNotOwner):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, persistence.ErrUnschedulable):
		writeOpError(w, http.StatusConflict, "unschedulable", err.Error(), true)
	case errors.Is(err, persistence.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrAlreadyExists), errors.Is(err, persistence.ErrAlreadyClaimed):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, persistence.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, context.DeadlineExceeded):
		writeOpError(w, http.StatusGatewayTimeout, "timeout", err.Error(), true)
	case errors.Is(err, context.Canceled):
		writeOpError(w, 499, "canceled", err.Error(), true)
	default:
		writeOpError(w, http.StatusInternalServerError, "internal", err.Error(), false)
	}
}
