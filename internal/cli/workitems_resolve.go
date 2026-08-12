package cli

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/httpclient"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	api "github.com/tysonthomas9/loomcli/internal/modules/workitems/httpapi"
)

// Work Items adapter type constants. The LOOM_ISSUE_BACKEND environment key
// remains a stable operator-facing configuration contract.
const (
	WorkItemsAdapterFleetDB = "fleetdb"
	WorkItemsAdapterFleet   = "fleet"
	WorkItemsAdapterAPI     = "api"
)

// validWorkItemsAdapters is the set of accepted values for daemon.issue_backend.
var validWorkItemsAdapters = map[string]bool{
	WorkItemsAdapterFleetDB: true,
	WorkItemsAdapterFleet:   true,
	WorkItemsAdapterAPI:     true,
}

// resolveWorkItemsAdapterType returns the active Work Items adapter type based on precedence:
//  1. LOOM_ISSUE_BACKEND env var (highest — if set and valid)
//  2. LOOM_SERVER_URL env var (remote HTTP API mode)
//  3. Default: "fleetdb"
func ResolveWorkItemsAdapterType() string {
	if v := resolveWorkItemsAdapterFromEnv(); v != "" {
		return v
	}
	return WorkItemsAdapterFleetDB
}

// resolveWorkItemsAdapterFromEnv checks environment variables for Work Items adapter selection.
// Returns "" if no env var determines the backend.
func resolveWorkItemsAdapterFromEnv() string {
	// 1. LOOM_ISSUE_BACKEND env var (highest precedence)
	if v, ok := os.LookupEnv("LOOM_ISSUE_BACKEND"); ok && v != "" {
		if validWorkItemsAdapters[v] {
			return v
		}
		slog.Warn("invalid LOOM_ISSUE_BACKEND value; ignoring", "value", v)
	}

	// 2. LOOM_SERVER_URL env var — remote HTTP server mode.
	// The --server flag on rootCmd writes into this env var in PersistentPreRun
	// so both paths converge here.
	if v, ok := os.LookupEnv("LOOM_SERVER_URL"); ok && v != "" {
		return WorkItemsAdapterAPI
	}

	return ""
}

// isFleetActive returns true if the fleet backend (remote fleet server) is active.
func IsFleetActive() bool {
	return ResolveWorkItemsAdapterType() == WorkItemsAdapterFleet
}

// isFleetDBActive returns true if the fleet-db backend is active.
func IsFleetDBActive() bool {
	return ResolveWorkItemsAdapterType() == WorkItemsAdapterFleetDB
}

// IsAPIActive returns true if the remote HTTP API backend is active
// (i.e., --server or LOOM_SERVER_URL is set).
func IsAPIActive() bool {
	return ResolveWorkItemsAdapterType() == WorkItemsAdapterAPI
}

// --- Package-level Work Items interface state ---

var (
	trackerMu   sync.RWMutex
	trackerInst workitems.API
)

// DefaultWorkItems returns the package-level owner interface.
func DefaultWorkItems() workitems.API {
	trackerMu.RLock()
	t := trackerInst
	trackerMu.RUnlock()
	if t != nil {
		return t
	}
	trackerMu.Lock()
	defer trackerMu.Unlock()
	if trackerInst == nil {
		trackerInst = resolveWorkItems()
	}
	return trackerInst
}

func SetDefaultWorkItems(api workitems.API) {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = api
}

func ResetDefaultWorkItems() {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = nil
}

func resolveWorkItems() workitems.API {
	if t := ensureDefaultDeps().WorkItems; t != nil {
		return t
	}
	store := newFleetDBWorkItemsAdapter()
	api, _ := workitems.New(store)
	return api
}

// --- API backend factory (remote --server mode) ---

// createAPIWorkItems constructs the remote HTTP Work Items adapter. It
// resolves the server URL from --server / LOOM_SERVER_URL and the workspace
// ID from --workspace / LOOM_WORKSPACE.
//
// Returns an error if the server URL is missing or invalid, the workspace
// cannot be resolved, or if http client construction fails. On success, the
// returned backend uses httpclient.Client for auth (OIDC device flow + token
// cache) transparently via api.AuthTransport.
func createAPIWorkItems() (*api.Adapter, error) {
	serverURL := os.Getenv("LOOM_SERVER_URL")
	if serverFlag != "" {
		serverURL = serverFlag
	}
	if serverURL == "" {
		return nil, fmt.Errorf("api backend requires --server URL or LOOM_SERVER_URL env var")
	}

	// Construct the auth-aware httpclient.Client. This performs eager auth
	// discovery (GET /api/config) and may initiate the device flow if the
	// server requires OIDC. If the server is unreachable, this step fails
	// and the caller reports the error.
	hc, err := httpclient.New(httpclient.Config{ServerURL: serverURL})
	if err != nil {
		return nil, fmt.Errorf("api backend auth setup: %w", err)
	}

	httpCli := api.NewAuthHTTPClient(hc)

	// Resolve workspace ID.
	workspaceID := os.Getenv("LOOM_WORKSPACE")
	if workspaceFlag != "" {
		workspaceID = workspaceFlag
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("api backend requires --workspace or LOOM_WORKSPACE")
	}

	ab, err := api.New(api.Config{
		BaseURL:     serverURL,
		WorkspaceID: workspaceID,
		HTTPClient:  httpCli,
	})
	if err != nil {
		return nil, fmt.Errorf("api backend construction: %w", err)
	}

	slog.Info("remote Work Items adapter created", "url", serverURL, "workspace", workspaceID)
	return ab, nil
}
