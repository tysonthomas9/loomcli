package misc

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"

	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// clientErrorPayload is the JSON request body for POST /api/client-errors.
type clientErrorPayload struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Stack     string `json:"stack,omitempty"`
	URL       string `json:"url,omitempty"`
	Line      int    `json:"line,omitempty"`
	Col       int    `json:"col,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

const (
	clientErrorMaxBodyBytes    = 16 * 1024 // 16KB
	clientErrorMaxTypeLen      = 50
	clientErrorMaxMessageLen   = 4096
	clientErrorMaxStackLen     = 8192
	clientErrorMaxURLLen       = 2048
	clientErrorMaxUserAgentLen = 512
)

// ClientErrorLimiter is a per-IP token bucket rate limiter for the client-errors endpoint.
type ClientErrorLimiter struct {
	clients         sync.Map // map[string]*clientErrorLimiterEntry
	ratePerSec      rate.Limit
	burst           int
	stopCleanup     chan struct{}
	stopOnce        sync.Once
	cleanupInterval time.Duration
	ttl             time.Duration
}

type clientErrorLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64
}

// NewClientErrorLimiter creates a new per-IP rate limiter for client error reports.
func NewClientErrorLimiter(ratePerSec rate.Limit, burst int, cleanupInterval, ttl time.Duration) *ClientErrorLimiter {
	l := &ClientErrorLimiter{
		ratePerSec:      ratePerSec,
		burst:           burst,
		stopCleanup:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
		ttl:             ttl,
	}
	go l.CleanupLoop()
	return l
}

func (l *ClientErrorLimiter) allow(ip string) bool {
	if v, ok := l.clients.Load(ip); ok {
		entry := v.(*clientErrorLimiterEntry)
		entry.lastSeen.Store(time.Now().Unix())
		return entry.limiter.Allow()
	}
	entry := &clientErrorLimiterEntry{
		limiter: rate.NewLimiter(l.ratePerSec, l.burst),
	}
	entry.lastSeen.Store(time.Now().Unix())
	actual, _ := l.clients.LoadOrStore(ip, entry)
	return actual.(*clientErrorLimiterEntry).limiter.Allow()
}

func (l *ClientErrorLimiter) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopCleanup)
	})
}

func (l *ClientErrorLimiter) CleanupLoop() {
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

func (l *ClientErrorLimiter) evictStale() {
	cutoff := time.Now().Add(-l.ttl).Unix()
	l.clients.Range(func(key, value interface{}) bool {
		entry := value.(*clientErrorLimiterEntry)
		if entry.lastSeen.Load() < cutoff {
			l.clients.Delete(key)
		}
		return true
	})
}

// HandleClientErrors returns a handler for POST /api/client-errors.
// It accepts client-side error reports and logs them via slog.Warn.
func HandleClientErrors(limiter *ClientErrorLimiter) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		// Check per-endpoint rate limit
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

		// Cap request body size
		r.Body = http.MaxBytesReader(w, r.Body, clientErrorMaxBodyBytes)

		var payload clientErrorPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		// Validate required fields
		if payload.Type == "" {
			handler.RespondError(w, http.StatusBadRequest, "type is required")
			return
		}
		if len(payload.Type) > clientErrorMaxTypeLen {
			handler.RespondError(w, http.StatusBadRequest, "type exceeds maximum length")
			return
		}
		if payload.Message == "" {
			handler.RespondError(w, http.StatusBadRequest, "message is required")
			return
		}
		if len(payload.Message) > clientErrorMaxMessageLen {
			handler.RespondError(w, http.StatusBadRequest, "message exceeds maximum length")
			return
		}

		// Truncate optional fields to prevent log flooding
		if len(payload.Stack) > clientErrorMaxStackLen {
			payload.Stack = payload.Stack[:clientErrorMaxStackLen]
		}
		if len(payload.URL) > clientErrorMaxURLLen {
			payload.URL = payload.URL[:clientErrorMaxURLLen]
		}
		if len(payload.UserAgent) > clientErrorMaxUserAgentLen {
			payload.UserAgent = payload.UserAgent[:clientErrorMaxUserAgentLen]
		}

		slog.Warn("client-error",
			"type", payload.Type,
			"message", payload.Message,
			"stack", payload.Stack,
			"url", payload.URL,
			"line", payload.Line,
			"col", payload.Col,
			"user_agent", payload.UserAgent,
			"timestamp", payload.Timestamp,
			"client_ip", ip,
		)

		w.WriteHeader(http.StatusNoContent)
	}
}
