package webui

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// NewFleetDBProxy returns a reverse proxy that forwards fleet-db API requests
// (mounted at /api/v1/) to an upstream fleet-db, passing the caller's own
// credentials (X-API-Key / X-Actor / Authorization) through unchanged.
//
// This is the "config proxy" (2C): a sandboxed agent points LOOM_FLEET_DB_URL
// at loom serve instead of fleet-db directly, so the sandbox opens a single
// network egress to serve (its OPA policy need only allow serve, not fleet-db).
// Serve is a *scoped passthrough* — it deliberately does NOT substitute its own
// identity, so fleet-db's RBAC still authorizes the caller's workspace-scoped
// developer key. (httputil.ReverseProxy copies all non-hop-by-hop request
// headers to the upstream by default, so the auth headers are forwarded without
// any explicit handling here.)
//
// Returns nil if fleetDBURL is empty or invalid (proxy disabled).
func NewFleetDBProxy(fleetDBURL string, logger *slog.Logger) http.Handler {
	if fleetDBURL == "" {
		return nil
	}
	target, err := url.Parse(fleetDBURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		if logger != nil {
			logger.Warn("fleet-db proxy disabled: invalid url", "url", fleetDBURL, "error", err)
		}
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	// No ResponseHeaderTimeout: fleet-db's WaitForMutations long-poll holds the
	// response open for up to ~30s, and a header timeout would sever it.
	proxy.Transport = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req) // sets scheme/host/path to the upstream; preserves /api/v1/...
		req.Host = target.Host
		// Caller auth headers (X-API-Key, X-Actor, Authorization) are copied
		// verbatim by ReverseProxy. We intentionally inject NO serve credential
		// so fleet-db authorizes the caller's own scoped key.
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if logger != nil {
			logger.Error("fleet-db proxy error", "method", r.Method, "path", r.URL.Path, "error", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"fleet-db unavailable"}`))
	}

	if logger != nil {
		logger.Info("fleet-db proxy enabled", "target", fleetDBURL)
	}
	return proxy
}
