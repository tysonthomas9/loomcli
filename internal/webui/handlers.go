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
// ServiceError kinds are mapped to their natural HTTP codes via kindStatus;
// other errors default to 500.
func serviceErrorStatus(err error) int {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		return statusForKind(svcErr.Kind)
	}
	return http.StatusInternalServerError
}

// serviceErrorMessage returns the user-facing message for an error.
// For ServiceErrors it returns the Message field (without the Kind prefix);
// for other errors it returns a generic message to avoid leaking internals.
func serviceErrorMessage(err error) string {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		return svcErr.Message
	}
	return "internal server error"
}

// writeServiceError maps a service.ServiceError to an HTTP response.
// Delegates to WriteServiceError which uses the canonical kindStatus table.
func writeServiceError(w http.ResponseWriter, err error) {
	WriteServiceError(w, err)
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
