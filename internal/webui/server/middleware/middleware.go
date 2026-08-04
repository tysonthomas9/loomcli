// Package middleware holds the net/http request-path wrappers the web UI server
// composes around its routes: JWT/JWKS auth and user identity, workspace
// resolution into the request context, file-access RBAC, CORS, security headers,
// rate limiting, request logging, and panic recovery. Chained in
// internal/webui/app and internal/cli/serve; handler packages under
// internal/webui that need the caller identity or the resolved workspace read
// them back out via its *FromContext helpers. Seven handler packages do not
// import it at all, for three different reasons: driverapi, taskrunapi,
// webhooks and workflows take the workspace straight from the route value
// r.PathValue("ws"), which is the requested ID rather than the resolved
// WorkspaceRef.CanonicalID that WorkspaceFromContext returns; agentcontrol
// registers {ws}-shaped routes but ignores the segment entirely, forwarding to
// the daemon control socket by agent name; localsettings is not
// workspace-scoped (its routes carry no {ws} segment) and connectors has no
// non-test source at all.
package middleware

import "net/http"

// Middleware wraps an http.Handler with additional behavior.
type Middleware func(http.Handler) http.Handler

// Chain composes middleware in application order. The first middleware in the
// list is the outermost wrapper (runs first on the request path).
// Chain(a, b, c)(h) is equivalent to a(b(c(h))).
func Chain(mws ...Middleware) Middleware {
	return func(final http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			final = mws[i](final)
		}
		return final
	}
}
