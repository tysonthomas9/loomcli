package webui

import (
	"context"
	"net/http"
	"strings"
)

// workspaceContextKey is the unexported key type for storing the workspace ID
// in a request context. Using a struct type avoids collisions with other
// packages that use plain-string context keys.
type workspaceContextKey struct{}

// WorkspaceFromContext extracts the workspace ID from the context.
// Returns an empty string if no workspace ID is set.
func WorkspaceFromContext(ctx context.Context) string {
	v, _ := ctx.Value(workspaceContextKey{}).(string)
	return v
}

// WithWorkspace returns a new context with the workspace ID set.
func WithWorkspace(ctx context.Context, wsID string) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, wsID)
}

// WorkspaceMiddleware extracts the {ws} path parameter, validates that the
// workspace exists via wsExists, and injects it into the context. If the path
// param is empty it returns 400; if the workspace is not found it returns 404.
func WorkspaceMiddleware(wsExists func(id string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		if wsID == "" {
			respondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}

		if !wsExists(wsID) {
			respondError(w, http.StatusNotFound, "workspace not found: "+wsID)
			return
		}

		ctx := WithWorkspace(r.Context(), wsID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveWorkDirFromContext returns the on-disk path for the workspace stored
// in ctx. Returns "" when the context has no workspace ID, the resolver is
// nil, the lookup errors, or the resolved WorkspaceData has no path. This is
// the fallback semantics used by terminal spawn handlers: an empty workDir
// means "inherit the loom service's cwd", matching legacy behavior.
func resolveWorkDirFromContext(ctx context.Context, fn func(string) (*WorkspaceData, error)) string {
	wsID := WorkspaceFromContext(ctx)
	if wsID == "" || fn == nil {
		return ""
	}
	ws, err := fn(wsID)
	if err != nil || ws == nil {
		return ""
	}
	return ws.Path
}
