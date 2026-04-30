package misc

import (
	"context"
	"math"
	"net/http"
	"os"

	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/authmode"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// authConfigResponse is the JSON response for GET /api/config.
// It tells clients which authentication mode the server is running, plus
// the active issue-tracking backend so the frontend Settings view (and the
// parity harness) can distinguish a beads instance from a fleet instance
// without poking at LOOM_ISSUE_BACKEND on the host.
type authConfigResponse struct {
	Mode         string `json:"mode"`                    // "open" or "oidc"
	AuthURL      string `json:"auth_url,omitempty"`      // Better Auth service base URL for OAuth redirects (only when mode is "oidc")
	IssueBackend string `json:"issue_backend,omitempty"` // "beads" | "fleet" | "fleetdb" | "api" | "agent-ipc" (active IssueBackend.BackendName(), normalized)
}

// AuthConfigLimiter is a per-IP token bucket rate limiter for GET /api/config.
// Follows the same pattern as ClientErrorLimiter and CSPReportLimiter.
type AuthConfigLimiter struct {
	clients         sync.Map // map[string]*authConfigLimiterEntry
	ratePerSec      rate.Limit
	burst           int
	stopCleanup     chan struct{}
	stopOnce        sync.Once
	cleanupInterval time.Duration
	ttl             time.Duration
}

type authConfigLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64
}

func NewAuthConfigLimiter(ratePerSec rate.Limit, burst int, cleanupInterval, ttl time.Duration) *AuthConfigLimiter {
	l := &AuthConfigLimiter{
		ratePerSec:      ratePerSec,
		burst:           burst,
		stopCleanup:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
		ttl:             ttl,
	}
	go l.CleanupLoop()
	return l
}

func (l *AuthConfigLimiter) allow(ip string) bool {
	if v, ok := l.clients.Load(ip); ok {
		entry := v.(*authConfigLimiterEntry)
		entry.lastSeen.Store(time.Now().Unix())
		return entry.limiter.Allow()
	}
	entry := &authConfigLimiterEntry{
		limiter: rate.NewLimiter(l.ratePerSec, l.burst),
	}
	entry.lastSeen.Store(time.Now().Unix())
	actual, _ := l.clients.LoadOrStore(ip, entry)
	return actual.(*authConfigLimiterEntry).limiter.Allow()
}

func (l *AuthConfigLimiter) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopCleanup)
	})
}

func (l *AuthConfigLimiter) CleanupLoop() {
	ticker := time.NewTicker(l.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCleanup:
			return
		case <-ticker.C:
			l.evictStale()
		}
	}
}

func (l *AuthConfigLimiter) evictStale() {
	cutoff := time.Now().Add(-l.ttl).Unix()
	l.clients.Range(func(key, value interface{}) bool {
		entry := value.(*authConfigLimiterEntry)
		if entry.lastSeen.Load() < cutoff {
			l.clients.Delete(key)
		}
		return true
	})
}

// HandleAuthConfig returns a handler that serves the server's auth mode
// configuration. This is the bootstrap endpoint for frontend and CLI clients
// to discover whether authentication is required and where to authenticate.
//
// extAuthURL is the Better Auth service base URL (e.g., "https://auth.loomcli.com").
// The frontend uses this URL to construct OAuth redirect URLs and session
// management calls. It is NOT a JWKS URL — JWKS discovery is internal to the
// JWT verification middleware.
//
// issueBackendFn (optional) returns the active backend.IssueBackend. When
// non-nil and the resolved backend is non-nil at request time, the response
// includes the normalized backend family in the "issue_backend" field so
// clients can render a deterministic label (e.g., "beads" or "fleet") in
// the Settings view. The closure is re-evaluated per request to handle
// runtime swaps, though BackendName is documented as immutable. Falls back
// to the LOOM_ISSUE_BACKEND env var when the closure is nil or returns nil,
// which matters during early server bring-up before IssueBackendFn is wired.
//
// The rest of the response is effectively cached per request — config values
// (auth mode, auth URL) don't change at runtime (requires server restart).
func HandleAuthConfig(extAuthURL string, limiter *AuthConfigLimiter, issueBackendFn func(ctx context.Context) backend.IssueBackend) http.HandlerFunc {
	var baseResp authConfigResponse
	if extAuthURL != "" {
		// Return same-origin URL so the frontend BetterAuth client sends
		// requests through the auth proxy at /api/auth/*. This makes cookies
		// first-party and avoids cross-origin issues over HTTP.
		baseResp = authConfigResponse{
			Mode:    authmode.ModeOIDC,
			AuthURL: "", // empty = same-origin proxy at /api/auth/*
		}
	} else {
		baseResp = authConfigResponse{
			Mode: authmode.ModeOpen,
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ip := middleware.ExtractClientIP(r)
		if !limiter.allow(ip) {
			retryAfter := int(math.Ceil(1.0 / float64(limiter.ratePerSec)))
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			handler.WriteJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error":       "rate limit exceeded",
				"retry_after": retryAfter,
			})
			return
		}

		// Resolve issue_backend lazily per request so runtime backend swaps
		// reflect in the response. Most of the time this is a simple pointer
		// load and never-nil — cost is negligible vs the rate limiter above.
		resp := baseResp
		resp.IssueBackend = resolveIssueBackendLabel(r.Context(), issueBackendFn)

		// SECURITY: no-store prevents caching that could enable downgrade attacks.
		// An attacker who poisons a cached response with mode:"open" would bypass
		// auth for the cache lifetime. no-store ensures every page load fetches fresh.
		w.Header().Set("Cache-Control", "no-store")
		handler.WriteJSON(w, http.StatusOK, resp)
	}
}

// resolveIssueBackendLabel returns the normalized issue backend family name
// ("beads", "fleet", "fleetdb", "api", "agent-ipc") for /api/config. The
// normalization collapses backend-specific suffixes (e.g. "beads-pooled" →
// "beads", "fleet-db" → "fleet") so the frontend can switch on a small set
// of stable labels. Falls back to LOOM_ISSUE_BACKEND when no factory is
// wired — matches the resolution order used elsewhere in the codebase.
func resolveIssueBackendLabel(ctx context.Context, issueBackendFn func(ctx context.Context) backend.IssueBackend) string {
	if issueBackendFn != nil {
		if be := issueBackendFn(ctx); be != nil {
			return normalizeBackendName(be.BackendName())
		}
	}
	if v := os.Getenv("LOOM_ISSUE_BACKEND"); v != "" {
		return normalizeBackendName(v)
	}
	return ""
}

// normalizeBackendName maps a raw BackendName() output to the canonical
// family label the frontend/CLI knows about. Unknown values pass through
// so callers can still see what the backend reported.
func normalizeBackendName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "":
		return ""
	case strings.HasPrefix(n, "beads"):
		return "beads"
	case n == "fleet-db" || n == "fleetdb":
		return "fleet"
	case strings.HasPrefix(n, "fleet"):
		return "fleet"
	default:
		return n
	}
}
