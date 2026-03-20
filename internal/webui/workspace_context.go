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

// WorkspaceMiddleware extracts a workspace ID from the request and injects it
// into the context. It checks (in order):
//
//  1. The "Workspace" request header.
//  2. The {ws} path parameter (Go 1.22+ ServeMux pattern matching).
//
// If a workspace ID is found, it is injected via WithWorkspace and the next
// handler is called. If not found, the middleware returns 400 Bad Request.
func WorkspaceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsID := r.Header.Get("Workspace")
		if wsID == "" {
			wsID = r.PathValue("ws")
		}

		wsID = strings.TrimSpace(wsID)
		if wsID == "" {
			respondError(w, http.StatusBadRequest, "workspace ID is required: set the Workspace header or use a workspace-scoped URL")
			return
		}

		ctx := WithWorkspace(r.Context(), wsID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalWorkspaceMiddleware reads the Workspace header and injects it into
// the context. If the header is missing, it injects the defaultWS value instead.
// This enables non-scoped routes (e.g., /api/issues) to be workspace-aware
// when used with a MultiPool that reads workspace ID from the context.
func OptionalWorkspaceMiddleware(defaultWS string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.Header.Get("Workspace"))
		if wsID == "" {
			wsID = defaultWS
		}
		if wsID != "" {
			ctx := WithWorkspace(r.Context(), wsID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		next.ServeHTTP(w, r)
	})
}
