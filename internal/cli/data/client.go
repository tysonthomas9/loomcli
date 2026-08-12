package data

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend/api"
	"github.com/tysonthomas9/loomcli/internal/httpclient"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Package-level HTTP client state. The client is lazily constructed on the
// first call to getHTTPClient() and re-used within a single process
// invocation. Tests reset the state via resetClient().
//
// Both getHTTPClient and resetClient hold clientMu so that tests running
// with -race do not observe a torn sync.Once when they rebuild state
// between httptest.Server instances. Production has one command per
// process and only one goroutine calls getHTTPClient, but the lock keeps
// the test story clean.
var (
	clientMu    sync.Mutex
	clientReady bool
	clientErr   error
	httpCli     *http.Client
	rawHC       *httpclient.Client //nolint:unused // kept for future direct Do() access (e.g. SSE)
	resolvedURL string

	providerMu             sync.Mutex
	localWorkItemsProvider func(context.Context) workitems.API
)

// SetLocalWorkItemsProvider wires the non-HTTP Work Items capability used by issue
// commands when no --server/LOOM_SERVER_URL is configured.
func SetLocalWorkItemsProvider(provider func(context.Context) workitems.API) {
	providerMu.Lock()
	defer providerMu.Unlock()
	localWorkItemsProvider = provider
}

// getHTTPClient returns the lazily-initialized *http.Client that wraps
// internal/httpclient.Client via api.AuthTransport. It also returns the
// resolved server URL so callers can build per-request URLs without
// re-reading flags/env.
//
// If neither --server nor LOOM_SERVER_URL is set, it returns an error with
// a clear message — every cli/data command surfaces this to stderr.
func getHTTPClient() (*http.Client, string, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if clientReady {
		return httpCli, resolvedURL, clientErr
	}
	clientReady = true
	url := serverURL
	if url == "" {
		url = os.Getenv("LOOM_SERVER_URL")
	}
	if url == "" {
		clientErr = fmt.Errorf("loom data commands require --server or LOOM_SERVER_URL")
		return nil, "", clientErr
	}
	hc, err := httpclient.New(httpclient.Config{ServerURL: url})
	if err != nil {
		clientErr = fmt.Errorf("auth setup: %w", err)
		return nil, "", clientErr
	}
	rawHC = hc
	httpCli = api.NewAuthHTTPClient(hc)
	resolvedURL = url
	return httpCli, resolvedURL, nil
}

func configuredServerURL() string {
	if serverURL != "" {
		return serverURL
	}
	return os.Getenv("LOOM_SERVER_URL")
}

func getWorkItems(ctx context.Context) (workitems.API, error) {
	if configuredServerURL() == "" {
		providerMu.Lock()
		provider := localWorkItemsProvider
		providerMu.Unlock()
		if provider == nil {
			return nil, fmt.Errorf("loom data commands require --server/LOOM_SERVER_URL or a local Work Items provider")
		}
		items := provider(ctx)
		if items == nil {
			return nil, fmt.Errorf("local Work Items provider returned nil")
		}
		return items, nil
	}
	cli, url, err := getHTTPClient()
	if err != nil {
		return nil, err
	}
	wsID, err := resolveWorkspaceID(ctx, cli, url)
	if err != nil {
		return nil, err
	}
	return api.New(api.Config{BaseURL: url, WorkspaceID: wsID, HTTPClient: cli})
}

// resetClient clears the singleton state so a fresh client can be
// constructed on the next call to getHTTPClient(). Intended for tests that
// switch between httptest.Server instances within the same process.
// Unexported — callers in _test.go files in the same package see it.
func resetClient() {
	clientMu.Lock()
	defer clientMu.Unlock()
	clientReady = false
	clientErr = nil
	httpCli = nil
	rawHC = nil
	resolvedURL = ""
}
