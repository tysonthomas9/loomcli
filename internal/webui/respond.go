package webui

import (
	"encoding/json"
	"net/http"
)

// respondJSON writes a JSON response with the given status code.
// It sets Content-Type to application/json, writes the status header,
// and encodes v as JSON. Encoding errors are logged.
func respondJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("failed to encode JSON response", "err", err)
	}
}

// respondError writes a JSON error response: {"error": message}.
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}
