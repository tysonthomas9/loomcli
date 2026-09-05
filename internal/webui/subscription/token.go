package subscription

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// HandleSSEToken creates an HTTP handler that exchanges a valid JWT for a
// short-lived opaque SSE token. The JWT is validated by ExtAuth middleware
// upstream; this handler extracts UserIdentity from the request context.
//
// When store is nil, SSE token auth is disabled. The route still returns a
// successful disabled response so browser clients do not treat open mode as a
// missing static/API resource.
func HandleSSEToken(store *realtime.TokenStore) http.HandlerFunc {
	return handleSSEToken(store, middleware.WorkspaceFromContext)
}

func handleSSEToken(
	store *realtime.TokenStore,
	workspaceFromCtx func(context.Context) string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		if store == nil {
			handler.WriteJSON(w, http.StatusOK, map[string]bool{"disabled": true})
			return
		}

		identity, ok := middleware.UserIdentityFromContext(r.Context())
		if !ok || identity.UserID == "" {
			handler.RespondError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		workspaceID := sseWorkspaceFromContext(r.Context(), workspaceFromCtx)

		token, err := store.Generate(identity.UserID, workspaceID)
		if err != nil {
			slog.Warn("failed to generate SSE token", "err", err)
			handler.RespondError(w, http.StatusInternalServerError, "failed to generate token")
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

func sseWorkspaceFromContext(ctx context.Context, workspaceFromCtx func(context.Context) string) string {
	if workspaceFromCtx != nil {
		return workspaceFromCtx(ctx)
	}
	return middleware.WorkspaceFromContext(ctx)
}
