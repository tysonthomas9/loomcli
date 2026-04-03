package webui

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// kindStatus maps each service.ErrorKind to its HTTP status code.
// Every ErrorKind defined in service/errors.go must have an entry here.
var kindStatus = map[service.ErrorKind]int{
	service.KindNotFound:        http.StatusNotFound,
	service.KindValidation:      http.StatusBadRequest,
	service.KindUnavailable:     http.StatusServiceUnavailable,
	service.KindTimeout:         http.StatusGatewayTimeout,
	service.KindConflict:        http.StatusConflict,
	service.KindInternal:        http.StatusInternalServerError,
	service.KindForbidden:       http.StatusForbidden,
	service.KindUnauthorized:    http.StatusUnauthorized,
	service.KindLocked:          http.StatusLocked,
	service.KindPayloadTooLarge: http.StatusRequestEntityTooLarge,
	service.KindRateLimited:     http.StatusTooManyRequests,
	service.KindBadGateway:      http.StatusBadGateway,
	service.KindNotImplemented:  http.StatusNotImplemented,
}

// statusForKind returns the HTTP status code for the given ErrorKind.
// Unknown kinds return 500 Internal Server Error.
func statusForKind(kind service.ErrorKind) int {
	if status, ok := kindStatus[kind]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// WriteServiceError extracts a *service.ServiceError from err, maps its Kind
// to an HTTP status code, logs the full error, and writes a JSON error response.
// If err is not a *service.ServiceError, it writes 500 with a generic message.
func WriteServiceError(w http.ResponseWriter, err error) {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		status := statusForKind(svcErr.Kind)
		slog.Error("service error",
			"kind", string(svcErr.Kind),
			"status", status,
			"msg", svcErr.Message,
			"err", err,
		)
		respondError(w, status, svcErr.Message)
		return
	}
	slog.Error("unexpected error", "err", err)
	respondError(w, http.StatusInternalServerError, "internal server error")
}
