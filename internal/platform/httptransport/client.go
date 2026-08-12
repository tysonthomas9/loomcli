package httptransport

import (
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Observe wraps an outbound transport with the process-wide OpenTelemetry
// propagation policy. A nil base uses http.DefaultTransport.
func Observe(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}

// NewObservedClient creates an outbound HTTP client with trace propagation and
// the caller-owned timeout policy.
func NewObservedClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: Observe(nil), Timeout: timeout}
}
