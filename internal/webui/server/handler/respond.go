package handler

import "net/http"

// RespondError writes a JSON error response: {"error": message}.
func RespondError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
