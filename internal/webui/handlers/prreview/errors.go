package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

var errEgressUnavailable = errors.New("pull request review connector egress is unavailable")

// writeJSON writes a 200 success envelope; every non-200 goes through the
// error writers below.
func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{
		Success: true,
		Data:    data,
	})
}

func writePRReviewError(w http.ResponseWriter, err error) {
	if errors.Is(err, errEgressUnavailable) {
		writePRReviewErrorCode(w, http.StatusServiceUnavailable, "egress_unavailable", err.Error(), false)
		return
	}
	if failure, ok := connectorsmodule.ClassifyDispatchError(err); ok {
		switch failure.Kind {
		case connectorsmodule.DispatchFailureGrantDenied:
			writePRReviewErrorCode(w, http.StatusForbidden, string(failure.Kind), err.Error(), false)
		case connectorsmodule.DispatchFailurePreconditionRequired:
			writePRReviewErrorCode(w, http.StatusPreconditionRequired, string(failure.Kind), err.Error(), false)
		case connectorsmodule.DispatchFailureStaleSubject:
			writePRReviewErrorCode(w, http.StatusConflict, string(failure.Kind), err.Error(), false)
		case connectorsmodule.DispatchFailureRateLimited:
			writePRReviewErrorCode(w, http.StatusTooManyRequests, string(failure.Kind), err.Error(), true)
		case connectorsmodule.DispatchFailureUpstream:
			writePRReviewErrorCode(w, http.StatusBadGateway, string(failure.Kind), err.Error(), failure.Retryable)
		default:
			writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", err.Error(), false)
		}
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writePRReviewErrorCode(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, domain.ErrInvalid):
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, context.DeadlineExceeded):
		writePRReviewErrorCode(w, http.StatusGatewayTimeout, "timeout", err.Error(), true)
	case errors.Is(err, context.Canceled):
		writePRReviewErrorCode(w, 499, "canceled", err.Error(), true)
	default:
		writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", err.Error(), false)
	}
}

func writePRReviewErrorCode(w http.ResponseWriter, status int, code, message string, retryable bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Success   bool   `json:"success"`
		Data      any    `json:"data"`
		Error     string `json:"error"`
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}{
		Success:   false,
		Data:      nil,
		Error:     message,
		Code:      code,
		Retryable: retryable,
	})
}
