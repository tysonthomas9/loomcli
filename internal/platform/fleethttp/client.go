package fleethttp

import (
	"net/http"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/httptransport"
)

// sharedTransport is a process-wide *http.Transport used by every
// Adapter instance that does not supply its own *http.Client. It is
// tuned for the loom-fleet → fleet-db long-poll workload, where N
// subscriber goroutines plus M concurrent UI requests all hammer one
// host:port. The Go default transport caps idle connections at 2 per host,
// which forces a fresh TCP dial (and a fresh fleet-db Redis pool checkout)
// on nearly every request and exhausts fleet-db under load.
//
// See docs/design/fleet-http-connection-reuse.md for the symptom analysis
// and the rationale behind each tuning value.
var sharedTransport = &http.Transport{
	MaxIdleConnsPerHost:   128,
	MaxIdleConns:          256,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

var (
	sharedClientOnce sync.Once
	sharedClient     *http.Client
)

// SharedHTTPClient returns the singleton *http.Client used as the default
// HTTP client for Adapter instances when Config.HTTPClient is nil.
// It is backed by sharedTransport (so connection reuse is shared across
// every Adapter in the process) and uses a Timeout that is decoupled
// from the server-side long-poll deadline.
//
// Timeout rationale: fleet-db's WaitForMutations long-poll runs for up to
// 30s server-side; the previous 30s client timeout raced the server timer
// and manifested as `context canceled` errors under any non-trivial network
// latency. 65s = 30s server poll + 30s slack + 5s response slack, which
// keeps per-call context.WithTimeout (set in the subscriber loop) as the
// dominant exit path.
func SharedHTTPClient() *http.Client {
	sharedClientOnce.Do(func() {
		sharedClient = &http.Client{
			Transport: httptransport.Observe(sharedTransport),
			Timeout:   65 * time.Second,
		}
	})
	return sharedClient
}
