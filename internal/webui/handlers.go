package webui

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	// MaxListLimit is the maximum number of issues that can be requested in a single call.
	MaxListLimit = 1000

	// maxRequestBody is the maximum request body size (1MB) to prevent DoS attacks.
	maxRequestBody = 1 << 20
)

// writeIssuesError writes a JSON error response for the issues endpoint.
func writeIssuesError(w http.ResponseWriter, status int, message, code string) {
	respondJSON(w, status, IssuesResponse{Success: false, Error: message, Code: code})
}

// serviceErrorStatus returns the HTTP status code for an error.
// ServiceError kinds are mapped to their natural HTTP codes; other errors
// default to 500.
func serviceErrorStatus(err error) int {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		switch svcErr.Kind {
		case service.KindNotFound:
			return http.StatusNotFound
		case service.KindValidation:
			return http.StatusBadRequest
		case service.KindUnavailable:
			return http.StatusServiceUnavailable
		case service.KindTimeout:
			return http.StatusGatewayTimeout
		case service.KindConflict:
			return http.StatusConflict
		case service.KindForbidden:
			return http.StatusForbidden
		}
	}
	return http.StatusInternalServerError
}

// writeServiceError maps a service.ServiceError to an HTTP response.
func writeServiceError(w http.ResponseWriter, err error) {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		status := http.StatusInternalServerError
		switch svcErr.Kind {
		case service.KindNotFound:
			status = http.StatusNotFound
		case service.KindValidation:
			status = http.StatusBadRequest
		case service.KindUnavailable:
			status = http.StatusServiceUnavailable
		case service.KindTimeout:
			status = http.StatusGatewayTimeout
		case service.KindConflict:
			status = http.StatusConflict
		case service.KindForbidden:
			status = http.StatusForbidden
		}
		respondError(w, status, svcErr.Message)
		return
	}
	respondError(w, http.StatusInternalServerError, "internal server error")
}

// splitAndTrim splits a comma-separated string and trims whitespace from each element.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
