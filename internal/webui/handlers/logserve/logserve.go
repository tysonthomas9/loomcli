// Package logserve owns the shared wire contract and error mapping for
// persisted task-run and driver-run logs.
package logserve

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/taskrunlogs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type persistedLogDTO struct {
	Content    string    `json:"content"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Truncated  bool      `json:"truncated"`
}

type persistedLogResponse struct {
	Success bool            `json:"success"`
	Data    persistedLogDTO `json:"data"`
}

// ValidRecordID preserves the traversal rejection that existed when record
// IDs were also used as filesystem path components.
func ValidRecordID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

// ServeLog resolves one persisted artifact ref and writes the shared log wire
// response. Noun keeps the endpoint-specific error message meaningful.
func ServeLog(w http.ResponseWriter, r *http.Request, st store.Store, workspaceKey, ref, noun string) {
	log, err := taskrunlogs.Get(r.Context(), st, workspaceKey, ref)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			handler.RespondError(w, http.StatusNotFound, noun+" is not available yet")
			return
		}
		handler.RespondError(w, http.StatusInternalServerError, "read "+noun+" failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, persistedLogResponse{
		Success: true,
		Data: persistedLogDTO{
			Content:    log.Content,
			ModifiedAt: log.ModifiedAt,
			Truncated:  log.Truncated,
		},
	})
}
