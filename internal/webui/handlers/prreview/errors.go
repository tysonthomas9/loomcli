package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

var errEgressUnavailable = errors.New("pull request review connector egress is unavailable")

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{
		Success: true,
		Data:    data,
	})
}

func writePRReviewError(w http.ResponseWriter, err error) {
	var (
		pre   *providers.PreconditionRequired
		stale *providers.StaleSubject
		rl    *providers.RateLimited
		up    *providers.UpstreamError
	)
	switch {
	case errors.Is(err, errEgressUnavailable):
		writePRReviewErrorCode(w, http.StatusServiceUnavailable, "egress_unavailable", err.Error(), false)
	case errors.Is(err, domain.ErrGrantDenied):
		writePRReviewErrorCode(w, http.StatusForbidden, "grant_denied", err.Error(), false)
	case errors.As(err, &pre):
		writePRReviewErrorCode(w, http.StatusPreconditionRequired, "precondition_required", err.Error(), false)
	case errors.As(err, &stale):
		writePRReviewErrorCode(w, http.StatusConflict, "stale_subject", err.Error(), false)
	case errors.As(err, &rl):
		writePRReviewErrorCode(w, http.StatusTooManyRequests, "rate_limited", err.Error(), true)
	case errors.As(err, &up):
		writePRReviewErrorCode(w, http.StatusBadGateway, "upstream_error", err.Error(), providers.Retryable(err))
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
