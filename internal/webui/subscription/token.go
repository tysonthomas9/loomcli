package subscription

import (
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// HandleSSEToken creates an HTTP handler that exchanges a valid JWT for a
// short-lived opaque SSE token. The JWT is validated by ExtAuth middleware
// upstream; this handler extracts UserIdentity from the request context.
func HandleSSEToken(store *realtime.TokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := middleware.UserIdentityFromContext(r.Context())
		if !ok || identity.UserID == "" {
			handler.RespondError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		workspaceID := middleware.WorkspaceFromContext(r.Context())

		token, err := store.Generate(identity.UserID, workspaceID)
		if err != nil {
			slog.Warn("failed to generate SSE token", "err", err)
			handler.RespondError(w, http.StatusInternalServerError, "failed to generate token")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		handler.WriteJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}
