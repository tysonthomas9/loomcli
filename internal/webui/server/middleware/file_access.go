package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// WorkspaceRoleResolver authorizes a user for one canonical workspace.
type WorkspaceRoleResolver func(ctx context.Context, workspaceID string, identity UserIdentity) (string, error)

// FileAccessConfig configures the workspace file-browser authorization boundary.
type FileAccessConfig struct {
	RemoteAuth      bool
	ResolveRole     WorkspaceRoleResolver
	FrontendOrigins []string
	Logger          *slog.Logger
	GrantIssuer     sourcecontrol.AccessGrantIssuer
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
			grant, status, message, ok := authorizeFileRequest(r, cfg, logger, localOrigins)
			if !ok {
				writeJSONError(w, status, message)
				return
			}

			ctx := WithFileAccessGrant(r.Context(), grant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authorizeFileRequest(
	r *http.Request,
	cfg FileAccessConfig,
	logger *slog.Logger,
	localOrigins []localFrontendOrigin,
) (sourcecontrol.AccessGrant, int, string, bool) {
	if !cfg.GrantIssuer.Available() {
		return sourcecontrol.AccessGrant{}, http.StatusForbidden, "file browser grant issuer unavailable", false
	}
	grant, ok := remoteFileAccessGrant(r, cfg, logger)
	if !cfg.RemoteAuth {
		grant, ok = localFileAccessGrant(r, localOrigins, cfg.GrantIssuer)
	}
	if !ok {
		status, message := fileAccessDenial(r, cfg)
		return sourcecontrol.AccessGrant{}, status, message, false
	}
	if fileRequestRequiresWrite(r.Method, r.URL.Path) && !grant.Capabilities().Write {
		return sourcecontrol.AccessGrant{}, http.StatusForbidden, "forbidden", false
	}
	return grant, 0, "", true
}

func fileAccessDenial(r *http.Request, cfg FileAccessConfig) (int, string) {
	if !cfg.RemoteAuth {
		return http.StatusForbidden, "forbidden"
	}
	identity, present := UserIdentityFromContext(r.Context())
	if !present || strings.TrimSpace(identity.UserID) == "" {
		return http.StatusUnauthorized, "authentication required"
	}
	if cfg.ResolveRole == nil {
		return http.StatusForbidden, "file browser RBAC not configured"
	}
	return http.StatusForbidden, "forbidden"
}

func remoteFileAccessGrant(r *http.Request, cfg FileAccessConfig, logger *slog.Logger) (sourcecontrol.AccessGrant, bool) {
	if !cfg.RemoteAuth {
		return sourcecontrol.AccessGrant{}, false
	}
	identity, ok := UserIdentityFromContext(r.Context())
	if !ok || strings.TrimSpace(identity.UserID) == "" || cfg.ResolveRole == nil {
		return sourcecontrol.AccessGrant{}, false
	}
	workspaceID := WorkspaceFromContext(r.Context())
	if strings.TrimSpace(workspaceID) == "" {
		return sourcecontrol.AccessGrant{}, false
	}
	role, err := cfg.ResolveRole(r.Context(), workspaceID, identity)
	if err != nil {
		logger.WarnContext(r.Context(), "workspace file role resolution failed",
			"workspace", workspaceID, "user_id", identity.UserID, "err", err)
		return sourcecontrol.AccessGrant{}, false
	}
	return fileCapabilitiesForRole(cfg.GrantIssuer, role)
}

func fileCapabilitiesForRole(issuer sourcecontrol.AccessGrantIssuer, role string) (sourcecontrol.AccessGrant, bool) {
	switch normalizeFileRole(role) {
	case "admin", "owner", "maintainer":
		return issuer.ReadWrite(true), true
	case "editor", "developer", "dev":
		return issuer.ReadWrite(true), true
	case "viewer", "read_only", "readonly", "read-only":
		return issuer.Read(false), true
	default:
		return sourcecontrol.AccessGrant{}, false
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

func localFileAccessGrant(r *http.Request, origins []localFrontendOrigin, issuer sourcecontrol.AccessGrantIssuer) (sourcecontrol.AccessGrant, bool) {
	requestHost := strings.ToLower(requestHostname(r.Host))
	if !isLoopbackHostname(requestHost) {
		return sourcecontrol.AccessGrant{}, false
	}
	rawOrigin := strings.TrimSuffix(strings.TrimSpace(r.Header.Get("Origin")), "/")
	for _, allowed := range origins {
		if requestHost != allowed.hostname {
			continue
		}
		if rawOrigin == "" {
			if strings.EqualFold(r.Host, allowed.authority) {
				return issuer.ReadWrite(true), true
			}
			continue
		}
		if strings.EqualFold(rawOrigin, allowed.origin) {
			return issuer.ReadWrite(true), true
		}
	}
	return sourcecontrol.AccessGrant{}, false
}

type fileAccessGrantContextKey struct{}

// WithFileAccessGrant carries the delivery boundary's authenticated grant to
// the HTTP handler, which passes it explicitly into Source Control commands.
func WithFileAccessGrant(ctx context.Context, grant sourcecontrol.AccessGrant) context.Context {
	return context.WithValue(ctx, fileAccessGrantContextKey{}, grant)
}

func FileAccessGrantFromContext(ctx context.Context) (sourcecontrol.AccessGrant, bool) {
	grant, ok := ctx.Value(fileAccessGrantContextKey{}).(sourcecontrol.AccessGrant)
	return grant, ok && grant.Capabilities().Read
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
