package webui

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TracingWithRouteName returns a paired outer/inner middleware. The outer
// installs otelhttp's server-span middleware (extracts W3C traceparent from
// inbound requests, starts a span). The inner runs after route capture
// updates r.Pattern so the span name picks up the low-cardinality template.
//
// Mirrors webui.PromMetricsMiddleware so trace span names and Prometheus
// labels share the same low-cardinality route. See
// docs/observability/tracing-contract.md §3.
//
// When the global TracerProvider is no-op, otelhttp is a near-pass-through:
// it still extracts traceparent for downstream propagation but doesn't
// export.
func TracingWithRouteName() (outer, inner func(http.Handler) http.Handler) {
	outer = otelhttp.NewMiddleware("http.server",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			// Pre-routing: r.Pattern is empty. Method-only fallback; the
			// inner middleware overwrites once the mux has matched.
			return r.Method
		}),
	)
	inner = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			pattern := r.Pattern
			if pattern == "" {
				return
			}
			span := trace.SpanFromContext(r.Context())
			if !span.IsRecording() {
				return
			}
			span.SetName(r.Method + " " + pattern)
			span.SetAttributes(semconv.HTTPRoute(pattern))
		})
	}
	return outer, inner
}
