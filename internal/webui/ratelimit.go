package webui

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig holds configuration for per-IP rate limiting.
type RateLimitConfig struct {
	ReadRate    rate.Limit // Requests per second for read operations (GET/HEAD/OPTIONS)
	ReadBurst   int        // Maximum burst size for read operations
	MutateRate  rate.Limit // Requests per second for mutating operations (POST/PUT/PATCH/DELETE)
	MutateBurst int        // Maximum burst size for mutating operations

	CleanupInterval time.Duration // How often to evict stale entries
	EntryTTL        time.Duration // How long an entry lives without activity before eviction
}

// DefaultRateLimitConfig returns sensible defaults for a local development tool.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		ReadRate:        100,
		ReadBurst:       200,
		MutateRate:      20,
		MutateBurst:     40,
		CleanupInterval: 5 * time.Minute,
		EntryTTL:        10 * time.Minute,
	}
}

// ipLimiterEntry holds per-IP rate limiters and a last-seen timestamp.
type ipLimiterEntry struct {
	readLimiter   *rate.Limiter
	mutateLimiter *rate.Limiter
	lastSeen      atomic.Int64 // unix timestamp
}

// rateLimiter manages per-IP rate limiters with background cleanup.
type rateLimiter struct {
	clients     sync.Map // map[string]*ipLimiterEntry
	config      RateLimitConfig
	stopCleanup chan struct{}
	stopOnce    sync.Once
}

// NewRateLimitMiddleware creates a per-IP rate limiting middleware and returns
// both the rateLimiter (for graceful shutdown via Stop()) and the middleware function.
func NewRateLimitMiddleware(config RateLimitConfig) (*rateLimiter, func(http.Handler) http.Handler) {
	rl := &rateLimiter{
		config:      config,
		stopCleanup: make(chan struct{}),
	}

	go rl.cleanupLoop()

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip rate limiting for health check endpoints
			if isExcludedFromRateLimit(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			ip := extractClientIP(r)
			entry := rl.getOrCreate(ip)
			entry.lastSeen.Store(time.Now().Unix())

			// Select limiter based on HTTP method
			var limiter *rate.Limiter
			if isMutatingMethod(r.Method) {
				limiter = entry.mutateLimiter
			} else {
				limiter = entry.readLimiter
			}

			if !limiter.Allow() {
				retryAfter := int(math.Ceil(1.0 / float64(limiter.Limit())))
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":       "rate limit exceeded",
					"retry_after": retryAfter,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}

	return rl, mw
}

// Stop terminates the background cleanup goroutine. Safe to call multiple times.
func (rl *rateLimiter) Stop() {
	rl.stopOnce.Do(func() {
		close(rl.stopCleanup)
	})
}

// getOrCreate retrieves or lazily creates a limiter entry for the given IP.
func (rl *rateLimiter) getOrCreate(ip string) *ipLimiterEntry {
	if v, ok := rl.clients.Load(ip); ok {
		return v.(*ipLimiterEntry)
	}
	entry := &ipLimiterEntry{
		readLimiter:   rate.NewLimiter(rl.config.ReadRate, rl.config.ReadBurst),
		mutateLimiter: rate.NewLimiter(rl.config.MutateRate, rl.config.MutateBurst),
	}
	entry.lastSeen.Store(time.Now().Unix())
	actual, _ := rl.clients.LoadOrStore(ip, entry)
	return actual.(*ipLimiterEntry)
}

// cleanupLoop periodically evicts stale entries.
func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCleanup:
			return
		case <-ticker.C:
			rl.evictStale()
		}
	}
}

// evictStale removes entries that haven't been seen within the TTL.
func (rl *rateLimiter) evictStale() {
	cutoff := time.Now().Add(-rl.config.EntryTTL).Unix()
	rl.clients.Range(func(key, value interface{}) bool {
		entry := value.(*ipLimiterEntry)
		if entry.lastSeen.Load() < cutoff {
			rl.clients.Delete(key)
		}
		return true
	})
}

// isExcludedFromRateLimit returns true for paths that should never be rate limited.
func isExcludedFromRateLimit(path string) bool {
	return path == "/health" || path == "/api/health"
}

// isMutatingMethod returns true for HTTP methods that modify server state.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
