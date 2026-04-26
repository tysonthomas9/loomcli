package fleet

import (
	"testing"
	"time"
)

// TestSharedHTTPClient_SingletonAndTimeout pins two contracts:
//
//  1. SharedHTTPClient() returns the same *http.Client on every call.
//     FleetBackend constructors rely on this for transport-level connection
//     reuse — handing out a fresh client per call would silently revert
//     fleet to the per-instance pool sizing this transport was introduced
//     to fix.
//  2. The returned client's Timeout is the documented 65s value (server
//     long-poll + slack). Drift here will reintroduce the 30s vs 30s
//     race that surfaced as `context canceled` log spam in production.
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

	// Transport identity is part of the singleton contract — every caller
	// must share the same idle-connection pool.
	if c1.Transport == nil {
		t.Fatal("SharedHTTPClient returned a client with nil Transport")
	}
	if c1.Transport != sharedTransport {
		t.Errorf("client.Transport != sharedTransport (singleton broken)")
	}
}
