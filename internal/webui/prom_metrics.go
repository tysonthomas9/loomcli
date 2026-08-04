package webui

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests by method, route, and status code.",
		},
		[]string{"method", "route", "code"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "loom",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
)

// statusRecorder wraps http.ResponseWriter to capture the status code
// for metrics reporting. Not safe for concurrent use (matches net/http contract).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Flush delegates to the inner writer if it implements http.Flusher.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the inner ResponseWriter for http.ResponseController compatibility.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Hijack delegates to the inner writer if it implements http.Hijacker.
// Required for WebSocket upgrades — nhooyr.io/websocket calls w.(http.Hijacker)
// directly rather than going through http.ResponseController, so we can't
// rely on the Unwrap chain here.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

type promRouteCtxKey struct{}

// promRouteStore holds the canonical route pattern, written by the inner
// middleware after the mux routes the request.
type promRouteStore struct{ pattern string }

// PromMetricsMiddleware returns a paired outer/inner middleware for HTTP metrics.
// The outer middleware wraps the request, records duration and status.
// The inner middleware (must be innermost, after mux routing) captures r.Pattern.
//
// This follows the fleet-db MetricsWithRouteCapture pattern:
// the mux sets r.Pattern on a copy of the request, so a shared mutable store
// in context is needed to propagate the pattern back to the outer middleware.
func PromMetricsMiddleware() (outer, inner func(http.Handler) http.Handler) {
	outer = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w}
			start := time.Now()

			store := &promRouteStore{}
			ctx := context.WithValue(r.Context(), promRouteCtxKey{}, store)
			next.ServeHTTP(rec, r.WithContext(ctx))

			route := store.pattern
			if route == "" {
				route = "unmatched"
			}
			elapsed := time.Since(start).Seconds()
			httpRequestDuration.WithLabelValues(r.Method, route).Observe(elapsed)
			httpRequestsTotal.WithLabelValues(
				r.Method, route, strconv.Itoa(rec.status),
			).Inc()
		})
	}
	inner = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if store, ok := r.Context().Value(promRouteCtxKey{}).(*promRouteStore); ok && r.Pattern != "" {
				// Only set if not already captured by an inner handler (e.g., sub-mux).
				// Inner handlers set more granular patterns; don't overwrite with prefix.
				if store.pattern == "" {
					pattern := r.Pattern
					if idx := strings.Index(pattern, " /"); idx >= 0 {
						pattern = pattern[idx+1:]
					}
					store.pattern = pattern
				}
			}
		})
	}
	return
}

// PromHandler returns the Prometheus metrics HTTP handler with compression
// disabled. Compression is disabled because /metrics is a composite endpoint:
// loom-specific gauges are written first as plaintext, then this handler
// appends auto-registered Prometheus metrics. With compression enabled, the
// scraper would receive plaintext followed by a gzip blob and fail to parse.
func PromHandler() http.Handler {
	return promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		DisableCompression: true,
	})
}

// PromRouteCaptureByPath wraps a handler to set the promRouteStore pattern
// from the actual request URL path. Used for prefix-mounted handlers (proxies)
// where r.Pattern is just the prefix but the URL path has the real endpoint.
func PromRouteCaptureByPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if store, ok := r.Context().Value(promRouteCtxKey{}).(*promRouteStore); ok {
			store.pattern = r.URL.Path
		}
	})
}

// SetPromRoutePattern writes a route pattern into the request context's
// prom route store, if present. Used by sub-mux handlers (e.g., the workspace
// mux) to surface granular route patterns for metrics. Strips the "METHOD /"
// prefix from net/http patterns so labels show just the path. No-op if the
// metrics middleware is not active or pattern is empty.
func SetPromRoutePattern(ctx context.Context, pattern string) {
	if pattern == "" {
		return
	}
	store, ok := ctx.Value(promRouteCtxKey{}).(*promRouteStore)
	if !ok {
		return
	}
	if idx := strings.Index(pattern, " /"); idx >= 0 {
		pattern = pattern[idx+1:]
	}
	store.pattern = pattern
}
