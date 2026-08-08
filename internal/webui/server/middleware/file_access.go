package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/filecoord"
)

// WorkspaceRoleResolver authorizes a user for one canonical workspace.
type WorkspaceRoleResolver func(ctx context.Context, workspaceID string, identity UserIdentity) (string, error)

// FileAccessConfig configures the workspace file-browser authorization boundary.
type FileAccessConfig struct {
	RemoteAuth      bool
	ResolveRole     WorkspaceRoleResolver
	FrontendOrigins []string
	Logger          *slog.Logger
}

// FileAccess authorizes file-browser requests and installs effective capabilities.
// Remote mode requires a workspace-scoped resolver; open mode is only available to
// a browser request matching an explicitly configured loopback frontend origin.
func FileAccess(cfg FileAccessConfig) Middleware {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	localOrigins := parseLoopbackOrigins(cfg.FrontendOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			capabilities, ok := remoteFileCapabilities(r, cfg, logger)
			if !cfg.RemoteAuth {
				capabilities, ok = localFileCapabilities(r, localOrigins)
			}
			if !ok {
				if cfg.RemoteAuth {
					if identity, present := UserIdentityFromContext(r.Context()); !present || strings.TrimSpace(identity.UserID) == "" {
						writeJSONError(w, http.StatusUnauthorized, "authentication required")
						return
					}
					if cfg.ResolveRole == nil {
						writeJSONError(w, http.StatusForbidden, "file browser RBAC not configured")
						return
					}
				}
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			if fileRequestRequiresWrite(r.Method, r.URL.Path) && !capabilities.Write {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}

			ctx := filecoord.WithFileCapabilities(r.Context(), capabilities)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func remoteFileCapabilities(r *http.Request, cfg FileAccessConfig, logger *slog.Logger) (filecoord.FileCapabilities, bool) {
	if !cfg.RemoteAuth {
		return filecoord.FileCapabilities{}, false
	}
	identity, ok := UserIdentityFromContext(r.Context())
	if !ok || strings.TrimSpace(identity.UserID) == "" || cfg.ResolveRole == nil {
		return filecoord.FileCapabilities{}, false
	}
	workspaceID := WorkspaceFromContext(r.Context())
	if strings.TrimSpace(workspaceID) == "" {
		return filecoord.FileCapabilities{}, false
	}
	role, err := cfg.ResolveRole(r.Context(), workspaceID, identity)
	if err != nil {
		logger.WarnContext(r.Context(), "workspace file role resolution failed",
			"workspace", workspaceID, "user_id", identity.UserID, "err", err)
		return filecoord.FileCapabilities{}, false
	}
	return fileCapabilitiesForRole(role)
}

func fileCapabilitiesForRole(role string) (filecoord.FileCapabilities, bool) {
	switch normalizeFileRole(role) {
	case "admin", "owner", "maintainer":
		return filecoord.FileCapabilities{Read: true, Write: true, Sensitive: true}, true
	case "editor", "developer", "dev":
		return filecoord.FileCapabilities{Read: true, Write: true, Sensitive: true}, true
	case "viewer", "read_only", "readonly", "read-only":
		return filecoord.FileCapabilities{Read: true}, true
	default:
		return filecoord.FileCapabilities{}, false
	}
}

func normalizeFileRole(role string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(role)), " ", "_")
}

func fileRequestRequiresWrite(method, requestPath string) bool {
	if method == http.MethodGet || method == http.MethodHead {
		return false
	}
	if method == http.MethodPost && strings.HasSuffix(requestPath, "/files/search") {
		return false
	}
	return true
}

type localFrontendOrigin struct {
	origin    string
	hostname  string
	authority string
}

func parseLoopbackOrigins(rawOrigins []string) []localFrontendOrigin {
	origins := make([]localFrontendOrigin, 0, len(rawOrigins))
	for _, raw := range rawOrigins {
		u, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(raw), "/"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			continue
		}
		hostname := strings.ToLower(u.Hostname())
		if !isLoopbackHostname(hostname) {
			continue
		}
		origins = append(origins, localFrontendOrigin{
			origin:    strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host),
			hostname:  hostname,
			authority: strings.ToLower(u.Host),
		})
	}
	return origins
}

func localFileCapabilities(r *http.Request, origins []localFrontendOrigin) (filecoord.FileCapabilities, bool) {
	requestHost := strings.ToLower(requestHostname(r.Host))
	if !isLoopbackHostname(requestHost) {
		return filecoord.FileCapabilities{}, false
	}
	rawOrigin := strings.TrimSuffix(strings.TrimSpace(r.Header.Get("Origin")), "/")
	for _, allowed := range origins {
		if requestHost != allowed.hostname {
			continue
		}
		if rawOrigin == "" {
			if strings.EqualFold(r.Host, allowed.authority) {
				return filecoord.FileCapabilities{Read: true, Write: true, Sensitive: true}, true
			}
			continue
		}
		if strings.EqualFold(rawOrigin, allowed.origin) {
			return filecoord.FileCapabilities{Read: true, Write: true, Sensitive: true}, true
		}
	}
	return filecoord.FileCapabilities{}, false
}

func requestHostname(authority string) string {
	if host, _, err := net.SplitHostPort(authority); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(authority, "[]")
}

func isLoopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
