package webui

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/authmode"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// authConfigResponse is the JSON response for GET /api/config.
// It tells clients which authentication mode the server is running.
type authConfigResponse struct {
	Mode    string `json:"mode"`               // "open" or "oidc"
	AuthURL string `json:"auth_url,omitempty"` // Better Auth service base URL for OAuth redirects (only when mode is "oidc")
}

// authConfigLimiter is a per-IP token bucket rate limiter for GET /api/config.
// Follows the same pattern as clientErrorLimiter and cspReportLimiter.
type authConfigLimiter struct {
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

func newAuthConfigLimiter(ratePerSec rate.Limit, burst int, cleanupInterval, ttl time.Duration) *authConfigLimiter {
	l := &authConfigLimiter{
		ratePerSec:      ratePerSec,
		burst:           burst,
		stopCleanup:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
		ttl:             ttl,
	}
	go l.cleanupLoop()
	return l
}

func (l *authConfigLimiter) allow(ip string) bool {
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

func (l *authConfigLimiter) stop() {
	l.stopOnce.Do(func() {
		close(l.stopCleanup)
	})
}

func (l *authConfigLimiter) cleanupLoop() {
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

func (l *authConfigLimiter) evictStale() {
	cutoff := time.Now().Add(-l.ttl).Unix()
	l.clients.Range(func(key, value interface{}) bool {
		entry := value.(*authConfigLimiterEntry)
		if entry.lastSeen.Load() < cutoff {
			l.clients.Delete(key)
		}
		return true
	})
}

// handleAuthConfig returns a handler that serves the server's auth mode
// configuration. This is the bootstrap endpoint for frontend and CLI clients
// to discover whether authentication is required and where to authenticate.
//
// extAuthURL is the Better Auth service base URL (e.g., "https://auth.loomcli.com").
// The frontend uses this URL to construct OAuth redirect URLs and session
// management calls. It is NOT a JWKS URL — JWKS discovery is internal to the
// JWT verification middleware.
//
// The response is pre-built once — config values never change at runtime
// (requires server restart).
func handleAuthConfig(extAuthURL string, limiter *authConfigLimiter) http.HandlerFunc {
	var resp authConfigResponse
	if extAuthURL != "" {
		resp = authConfigResponse{
			Mode:    authmode.ModeOIDC,
			AuthURL: extAuthURL,
		}
	} else {
		resp = authConfigResponse{
			Mode: authmode.ModeOpen,
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ip := middleware.ExtractClientIP(r)
		if !limiter.allow(ip) {
			retryAfter := int(math.Ceil(1.0 / float64(limiter.ratePerSec)))
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			respondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error":       "rate limit exceeded",
				"retry_after": retryAfter,
			})
			return
		}

		// SECURITY: no-store prevents caching that could enable downgrade attacks.
		// An attacker who poisons a cached response with mode:"open" would bypass
		// auth for the cache lifetime. no-store ensures every page load fetches fresh.
		w.Header().Set("Cache-Control", "no-store")
		respondJSON(w, http.StatusOK, resp)
	}
}
