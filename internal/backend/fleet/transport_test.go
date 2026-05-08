package fleet

import (
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// TestSharedHTTPClient_SingletonAndTimeout pins three contracts:
//
//  1. SharedHTTPClient() returns the same *http.Client on every call.
//     FleetBackend constructors rely on this for transport-level connection
//     reuse — handing out a fresh client per call would silently revert
//     fleet to the per-instance pool sizing this transport was introduced
//     to fix.
//  2. The returned client's Timeout is the documented 65s value (server
//     long-poll + slack). Drift here will reintroduce the 30s vs 30s
//     race that surfaced as `context canceled` log spam in production.
//  3. The Transport is the otelhttp wrapper around sharedTransport. Bare
//     sharedTransport would mean we lost the trace-context propagator.
//     Identity check on c1 == c2 covers the underlying pool-reuse
//     property: as long as the wrapper instance is stable, every caller
//     still hits the same idle-connection pool.
func TestSharedHTTPClient_SingletonAndTimeout(t *testing.T) {
	c1 := SharedHTTPClient()
	if c1 == nil {
		t.Fatal("SharedHTTPClient returned nil")
	}
	if want := 65 * time.Second; c1.Timeout != want {
		t.Errorf("Timeout = %v, want %v", c1.Timeout, want)
	}

	c2 := SharedHTTPClient()
	if c1 != c2 {
		t.Errorf("SharedHTTPClient is not a singleton: %p != %p", c1, c2)
	}

	if c1.Transport == nil {
		t.Fatal("SharedHTTPClient returned a client with nil Transport")
	}
	// Transport must be the otelhttp wrapper — bare sharedTransport means
	// outgoing requests skip traceparent injection.
	if _, ok := c1.Transport.(*otelhttp.Transport); !ok {
		t.Errorf("client.Transport type = %T, want *otelhttp.Transport (otelhttp wrapping lost?)", c1.Transport)
	}
	// Singleton identity at the wrapper level — c2 sees the same wrapper,
	// which composes the same sharedTransport beneath it.
	if c1.Transport != c2.Transport {
		t.Errorf("client.Transport identity drift across SharedHTTPClient calls")
	}
}
