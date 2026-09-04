package middleware

import (
	"log/slog"
	"net/http"
	"strings"
)

// WorkspacePermission is a coarse permission required by workspace-scoped web routes.
type WorkspacePermission string

const (
	WorkspacePermissionRead  WorkspacePermission = "workspace.read"
	WorkspacePermissionWrite WorkspacePermission = "workspace.write"
	WorkspacePermissionAdmin WorkspacePermission = "workspace.admin"
)

// WorkspaceRBACConfig configures workspace role/permission enforcement.
type WorkspaceRBACConfig struct {
	Enabled     bool
	ResolveRole WorkspaceRoleResolver
	Logger      *slog.Logger
}

// WorkspaceRBAC enforces coarse workspace permissions for authenticated users.
// It is disabled in open mode. In OIDC mode, requests without a UserIdentity
// return 401 and authenticated users lacking the route permission return 403.
func WorkspaceRBAC(cfg WorkspaceRBACConfig) Middleware {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			required, ok := requiredWorkspacePermission(r.Method, r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			identity, ok := UserIdentityFromContext(r.Context())
			if !ok || strings.TrimSpace(identity.UserID) == "" {
				writeJSONError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			workspaceID := WorkspaceFromContext(r.Context())
			if strings.TrimSpace(workspaceID) == "" {
				writeJSONError(w, http.StatusBadRequest, "workspace ID is required")
				return
			}

			role := identity.Role
			if cfg.ResolveRole != nil {
				resolvedRole, err := cfg.ResolveRole(r.Context(), workspaceID, identity)
				if err != nil {
					logger.Warn("workspace role resolution failed",
						"workspace", workspaceID,
						"user_id", identity.UserID,
						"err", err)
					writeJSONError(w, http.StatusForbidden, "forbidden")
					return
				}
				role = resolvedRole
			}

			if !workspaceRoleAllows(role, required) {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func requiredWorkspacePermission(method, path string) (WorkspacePermission, bool) {
	if method == http.MethodOptions || isPublicRoute(method, path) {
		return "", false
	}

	normalizedPath := stripWorkspacePrefix(path)
	if hasOwnAuthPrefix(normalizedPath) {
		return "", false
	}
	if isWorkspaceRootPath(path) && method == http.MethodDelete {
		return WorkspacePermissionAdmin, true
	}
	if isTerminalTokenPath(normalizedPath) {
		return WorkspacePermissionWrite, true
	}

	switch method {
	case http.MethodGet, http.MethodHead:
		return WorkspacePermissionRead, true
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return WorkspacePermissionWrite, true
	default:
		return "", false
	}
}

func workspaceRoleAllows(role string, required WorkspacePermission) bool {
	switch normalizeWorkspaceRole(role) {
	case "admin", "owner", "maintainer":
		return true
	case "developer", "dev", "editor":
		return required == WorkspacePermissionRead || required == WorkspacePermissionWrite
	case "viewer", "read_only", "readonly", "read-only", "user", "":
		return required == WorkspacePermissionRead
	default:
		return false
	}
}

func normalizeWorkspaceRole(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	role = strings.ReplaceAll(role, " ", "_")
	return role
}

func isTerminalTokenPath(normalizedPath string) bool {
	if normalizedPath == "/api/terminal/token" {
		return true
	}
	return strings.HasPrefix(normalizedPath, "/api/agents/") &&
		strings.HasSuffix(normalizedPath, "/terminal/token")
}

func isWorkspaceRootPath(path string) bool {
	const prefix = "/api/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	return rest != "" && !strings.Contains(rest, "/")
}
