package webui

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

const cspReportMaxBodyBytes = 10240 // 10KB

// cspReport represents the fields in a CSP violation report sent by browsers.
type cspReport struct {
	DocumentURI        string `json:"document-uri"`
	ViolatedDirective  string `json:"violated-directive"`
	EffectiveDirective string `json:"effective-directive"`
	OriginalPolicy     string `json:"original-policy"`
	BlockedURI         string `json:"blocked-uri"`
	StatusCode         int    `json:"status-code"`
	SourceFile         string `json:"source-file"`
	LineNumber         int    `json:"line-number"`
	ColumnNumber       int    `json:"column-number"`
}

// cspReportWrapper is the envelope browsers use when sending CSP reports.
type cspReportWrapper struct {
	Report cspReport `json:"csp-report"`
}

// cspReportLimiter is a per-IP token bucket rate limiter for the CSP report endpoint.
type cspReportLimiter struct {
	clients         sync.Map // map[string]*cspReportLimiterEntry
	ratePerSec      rate.Limit
	burst           int
	stopCleanup     chan struct{}
	stopOnce        sync.Once
	cleanupInterval time.Duration
	ttl             time.Duration
}

type cspReportLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64
}

func newCSPReportLimiter(ratePerSec rate.Limit, burst int, cleanupInterval, ttl time.Duration) *cspReportLimiter {
	l := &cspReportLimiter{
		ratePerSec:      ratePerSec,
		burst:           burst,
		stopCleanup:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
		ttl:             ttl,
	}
	go l.cleanupLoop()
	return l
}

func (l *cspReportLimiter) allow(ip string) bool {
	if v, ok := l.clients.Load(ip); ok {
		entry := v.(*cspReportLimiterEntry)
		entry.lastSeen.Store(time.Now().Unix())
		return entry.limiter.Allow()
	}
	entry := &cspReportLimiterEntry{
		limiter: rate.NewLimiter(l.ratePerSec, l.burst),
	}
	entry.lastSeen.Store(time.Now().Unix())
	actual, _ := l.clients.LoadOrStore(ip, entry)
	return actual.(*cspReportLimiterEntry).limiter.Allow()
}

func (l *cspReportLimiter) stop() {
	l.stopOnce.Do(func() {
		close(l.stopCleanup)
	})
}

func (l *cspReportLimiter) cleanupLoop() {
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

func (l *cspReportLimiter) evictStale() {
	cutoff := time.Now().Add(-l.ttl).Unix()
	l.clients.Range(func(key, value interface{}) bool {
		entry := value.(*cspReportLimiterEntry)
		if entry.lastSeen.Load() < cutoff {
			l.clients.Delete(key)
		}
		return true
	})
}

// handleCSPReport returns a handler for POST /api/csp-report.
// It accepts browser CSP violation reports and logs them via slog.Warn.
func handleCSPReport(limiter *cspReportLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check per-endpoint rate limit
		ip := extractClientIP(r)
		if !limiter.allow(ip) {
			retryAfter := int(math.Ceil(1.0 / float64(limiter.ratePerSec)))
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			respondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error":       "rate limit exceeded",
				"retry_after": retryAfter,
			})
			return
		}

		// Validate Content-Type
		ct := r.Header.Get("Content-Type")
		if ct != "application/csp-report" && ct != "application/json" {
			respondError(w, http.StatusUnsupportedMediaType, "unsupported content type")
			return
		}

		// Read body with size limit
		body, err := io.ReadAll(io.LimitReader(r.Body, cspReportMaxBodyBytes))
		if err != nil {
			respondError(w, http.StatusBadRequest, "failed to read body")
			return
		}

		var wrapper cspReportWrapper
		if err := json.Unmarshal(body, &wrapper); err != nil {
			respondError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		report := wrapper.Report
		slog.Warn("csp-violation",
			"document_uri", report.DocumentURI,
			"violated_directive", report.ViolatedDirective,
			"blocked_uri", report.BlockedURI,
			"source_file", report.SourceFile,
			"line_number", report.LineNumber,
		)

		w.WriteHeader(http.StatusNoContent)
	}
}
