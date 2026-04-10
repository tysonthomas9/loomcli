package webui

import (
	"context"
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
			rec := newRWRecorder(w)
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
				r.Method, route, strconv.Itoa(rec.Status()),
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

// PromHandler returns the standard Prometheus metrics HTTP handler.
func PromHandler() http.Handler {
	return promhttp.Handler()
}

// promRouteCaptureByPath wraps a handler to set the promRouteStore pattern
// from the actual request URL path. Used for prefix-mounted handlers (proxies)
// where r.Pattern is just the prefix but the URL path has the real endpoint.
func promRouteCaptureByPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if store, ok := r.Context().Value(promRouteCtxKey{}).(*promRouteStore); ok {
			store.pattern = r.URL.Path
		}
	})
}
