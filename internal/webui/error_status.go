package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// WriteServiceError extracts a *service.ServiceError from err, maps its Kind
// to an HTTP status code, logs the full error, and writes a JSON error response.
// If err is not a *service.ServiceError, it writes 500 with a generic message.
//
// This delegates to handler.HandleServiceError which owns the canonical
// ErrorKind→HTTP status mapping table.
func WriteServiceError(w http.ResponseWriter, err error) {
	handler.HandleServiceError(w, err)
}
