package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/fleethttp"
)

// workspaceContextKey is the unexported key type for storing the workspace ID
// in a request context. Using a struct type avoids collisions with other
// packages that use plain-string context keys.
type workspaceContextKey struct{}
type workspaceRefContextKey struct{}

// WorkspaceRef is the resolved workspace identity for a workspace-scoped
// request. RequestedID is the raw {ws} route value. CanonicalID is the stable
// workspace key that downstream services, SSE clients, and subscribers use.
type WorkspaceRef struct {
	RequestedID string
	CanonicalID string
}

// WorkspaceResolveFn resolves a raw route workspace into its canonical identity.
type WorkspaceResolveFn func(ctx context.Context, requestedID string) (WorkspaceRef, bool)

// WorkspaceFromContext extracts the workspace ID from the context.
// Returns an empty string if no workspace ID is set.
func WorkspaceFromContext(ctx context.Context) string {
	if ref, ok := ctx.Value(workspaceRefContextKey{}).(WorkspaceRef); ok {
		return ref.CanonicalID
	}
	v, _ := ctx.Value(workspaceContextKey{}).(string)
	return v
}

// WorkspaceRefFromContext extracts the resolved workspace ref from context.
// Returns a zero ref when no workspace is set.
func WorkspaceRefFromContext(ctx context.Context) WorkspaceRef {
	if ref, ok := ctx.Value(workspaceRefContextKey{}).(WorkspaceRef); ok {
		return ref
	}
	if wsID := WorkspaceFromContext(ctx); wsID != "" {
		return WorkspaceRef{RequestedID: wsID, CanonicalID: wsID}
	}
	return WorkspaceRef{}
}

// WithWorkspace returns a new context with the workspace ID set.
func WithWorkspace(ctx context.Context, wsID string) context.Context {
	return WithWorkspaceRef(ctx, WorkspaceRef{RequestedID: wsID, CanonicalID: wsID})
}

// WithWorkspaceRef returns a new context with the resolved workspace identity set.
func WithWorkspaceRef(ctx context.Context, ref WorkspaceRef) context.Context {
	if ref.CanonicalID == "" {
		ref.CanonicalID = ref.RequestedID
	}
	ctx = context.WithValue(ctx, workspaceRefContextKey{}, ref)
	return context.WithValue(ctx, workspaceContextKey{}, ref.CanonicalID)
}

// Workspace extracts the {ws} path parameter, validates that the
// workspace exists via wsExists, and injects it into the context. If the path
// param is empty it returns 400; if the workspace is not found it returns 404.
func Workspace(wsExists func(id string) bool) Middleware {
	return WorkspaceResolved(func(_ context.Context, id string) (WorkspaceRef, bool) {
		if wsExists == nil || !wsExists(id) {
			return WorkspaceRef{}, false
		}
		return WorkspaceRef{RequestedID: id, CanonicalID: id}, true
	})
}

// WorkspaceResolved extracts the {ws} path parameter, resolves it to a canonical
// workspace identity, and injects the canonical ID into request context. The
// raw route value remains available as WorkspaceRef.RequestedID.
func WorkspaceResolved(resolve WorkspaceResolveFn) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wsID := strings.TrimSpace(r.PathValue("ws"))
			if wsID == "" {
				writeJSONError(w, http.StatusBadRequest, "workspace ID is required")
				return
			}

			if resolve == nil {
				writeJSONError(w, http.StatusNotFound, "workspace not found: "+wsID)
				return
			}
			ref, ok := resolve(r.Context(), wsID)
			if !ok {
				writeJSONError(w, http.StatusNotFound, "workspace not found: "+wsID)
				return
			}
			if ref.RequestedID == "" {
				ref.RequestedID = wsID
			}
			if ref.CanonicalID == "" {
				ref.CanonicalID = ref.RequestedID
			}

			ctx := WithWorkspaceRef(r.Context(), ref)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WorkspaceMutationActor scopes a caller-supplied actor to downstream
// FleetDB requests for workspace mutations. trustInbound must only be true
// when the server runs without external auth (open/dev mode), where the
// trust model intentionally matches fleet-db --auth-dev-mode: with no
// authentication at all, X-Actor spoofing grants nothing the caller could
// not already do. Under external auth, identity belongs to the auth layer,
// so the header is stripped rather than honored — attribution then falls
// back to the serve process's configured actor.
func WorkspaceMutationActor(trustInbound bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !trustInbound {
				r.Header.Del("X-Actor")
				next.ServeHTTP(w, r)
				return
			}
			if !isMutationMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			actor := strings.TrimSpace(r.Header.Get("X-Actor"))
			if actor == "" {
				next.ServeHTTP(w, r)
				return
			}
			ctx := fleethttp.WithActor(r.Context(), actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
