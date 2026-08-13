package leadapi

import (
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type dataErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

func writeDataError(w http.ResponseWriter, status int, code, message string) {
	handler.WriteJSON(w, status, dataErrorResponse{Success: false, Error: message, Code: code})
}

func writeDataStatusError(w http.ResponseWriter, err error) {
	var statusErr *opStatusError
	if errors.As(err, &statusErr) {
		writeDataError(w, statusErr.status, statusErr.code, statusErr.message)
		return
	}
	writeDataError(w, http.StatusInternalServerError, "internal", err.Error())
}
