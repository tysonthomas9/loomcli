package misc

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

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const (
	cspReportMaxBodyBytes    = 10240 // 10KB
	cspReportMaxURILen       = 2048
	cspReportMaxDirectiveLen = 512
)

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

// CSPReportLimiter is a per-IP token bucket rate limiter for the CSP report endpoint.
type CSPReportLimiter struct {
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

func NewCSPReportLimiter(ratePerSec rate.Limit, burst int, cleanupInterval, ttl time.Duration) *CSPReportLimiter {
	l := &CSPReportLimiter{
		ratePerSec:      ratePerSec,
		burst:           burst,
		stopCleanup:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
		ttl:             ttl,
	}
	go l.CleanupLoop()
	return l
}

func (l *CSPReportLimiter) allow(ip string) bool {
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

func (l *CSPReportLimiter) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopCleanup)
	})
}

func (l *CSPReportLimiter) CleanupLoop() {
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

func (l *CSPReportLimiter) evictStale() {
	cutoff := time.Now().Add(-l.ttl).Unix()
	l.clients.Range(func(key, value interface{}) bool {
		entry := value.(*cspReportLimiterEntry)
		if entry.lastSeen.Load() < cutoff {
			l.clients.Delete(key)
		}
		return true
	})
}

// HandleCSPReport returns a handler for POST /api/csp-report.
// It accepts browser CSP violation reports and logs them via slog.Warn.
func HandleCSPReport(limiter *CSPReportLimiter) http.HandlerFunc { //nolint:funlen
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

		// Validate Content-Type
		ct := r.Header.Get("Content-Type")
		if ct != "application/csp-report" && ct != "application/json" {
			handler.RespondError(w, http.StatusUnsupportedMediaType, "unsupported content type")
			return
		}

		// Read body with size limit
		body, err := io.ReadAll(io.LimitReader(r.Body, cspReportMaxBodyBytes))
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, "failed to read body")
			return
		}

		var wrapper cspReportWrapper
		if err := json.Unmarshal(body, &wrapper); err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		report := wrapper.Report

		// Truncate fields to prevent oversized log entries
		if len(report.DocumentURI) > cspReportMaxURILen {
			report.DocumentURI = report.DocumentURI[:cspReportMaxURILen]
		}
		if len(report.ViolatedDirective) > cspReportMaxDirectiveLen {
			report.ViolatedDirective = report.ViolatedDirective[:cspReportMaxDirectiveLen]
		}
		if len(report.BlockedURI) > cspReportMaxURILen {
			report.BlockedURI = report.BlockedURI[:cspReportMaxURILen]
		}
		if len(report.SourceFile) > cspReportMaxURILen {
			report.SourceFile = report.SourceFile[:cspReportMaxURILen]
		}

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
