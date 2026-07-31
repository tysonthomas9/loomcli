package cli

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
	"github.com/tysonthomas9/loomcli/internal/httpclient"
)

// Issue backend type constants.
const (
	IssueBackendFleetDB = "fleetdb"
	IssueBackendFleet   = "fleet"
	IssueBackendAPI     = "api"
)

// validIssueBackends is the set of accepted values for daemon.issue_backend.
var validIssueBackends = map[string]bool{
	IssueBackendFleetDB: true,
	IssueBackendFleet:   true,
	IssueBackendAPI:     true,
}

// resolveIssueBackendType returns the active issue backend type based on precedence:
//  1. LOOM_ISSUE_BACKEND env var (highest — if set and valid)
//  2. LOOM_SERVER_URL env var (remote HTTP API mode)
//  3. Default: "fleetdb"
func ResolveIssueBackendType() string {
	if v := resolveIssueBackendFromEnv(); v != "" {
		return v
	}
	return IssueBackendFleetDB
}

// resolveIssueBackendFromEnv checks environment variables for issue backend selection.
// Returns "" if no env var determines the backend.
func resolveIssueBackendFromEnv() string {
	// 1. LOOM_ISSUE_BACKEND env var (highest precedence)
	if v, ok := os.LookupEnv("LOOM_ISSUE_BACKEND"); ok && v != "" {
		if validIssueBackends[v] {
			return v
		}
		slog.Warn("invalid LOOM_ISSUE_BACKEND value; ignoring", "value", v)
	}

	// 2. LOOM_SERVER_URL env var — remote HTTP server mode.
	// The --server flag on rootCmd writes into this env var in PersistentPreRun
	// so both paths converge here.
	if v, ok := os.LookupEnv("LOOM_SERVER_URL"); ok && v != "" {
		return IssueBackendAPI
	}

	return ""
}

// isFleetActive returns true if the fleet backend (remote fleet server) is active.
func IsFleetActive() bool {
	return ResolveIssueBackendType() == IssueBackendFleet
}

// isFleetDBActive returns true if the fleet-db backend is active.
func IsFleetDBActive() bool {
	return ResolveIssueBackendType() == IssueBackendFleetDB
}

// IsAPIActive returns true if the remote HTTP API backend is active
// (i.e., --server or LOOM_SERVER_URL is set).
func IsAPIActive() bool {
	return ResolveIssueBackendType() == IssueBackendAPI
}

// --- Package-level IssueBackend state (merged from issue_backend.go) ---

var (
	trackerMu   sync.RWMutex
	trackerInst backend.IssueBackend
)

// defaultIssueBackend returns the package-level IssueBackend, lazily initializing
// from defaultDeps.IssueBackend if not explicitly set.
func DefaultIssueBackend() backend.IssueBackend {
	trackerMu.RLock()
	t := trackerInst
	trackerMu.RUnlock()
	if t != nil {
		return t
	}
	trackerMu.Lock()
	defer trackerMu.Unlock()
	if trackerInst == nil {
		// In fleet mode, use the fleet backend directly — skip IPC wrapping
		// since the fleet server (not a local daemon) manages issues.
		if IsFleetActive() {
			trackerInst = resolveDirectIssueBackend()
		} else if sock := os.Getenv("LOOM_DAEMON_SOCKET"); sock != "" {
			agentName := os.Getenv("LOOM_AGENT_NAME")
			direct := resolveDirectIssueBackend()
			ipcClient := NewAgentIPCClient(sock, agentName)
			ipcClient.SessionID = os.Getenv("LOOM_SESSION_ID")
			ipcClient.AuthToken = os.Getenv("LOOM_AGENT_IPC_AUTH_TOKEN")
			trackerInst = newIPCIssueBackend(ipcClient, direct)
		} else {
			trackerInst = resolveDirectIssueBackend()
		}
	}
	return trackerInst
}

// setDefaultIssueBackend overrides the package-level IssueBackend (for testing).
func SetDefaultIssueBackend(ib backend.IssueBackend) {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = ib
}

// ResetDefaultIssueBackend clears the override so DefaultIssueBackend() re-initializes.
func ResetDefaultIssueBackend() {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = nil
}

// DaemonActivityObserver returns a harness.RetryPolicy-compatible OnActivity
// callback that forwards wrapper PTY-output timestamps to the daemon via the
// active AgentIPCClient. It returns nil when the agent is not running under a
// daemon (no LOOM_DAEMON_SOCKET, fleet mode, etc.) — harness.RetryPolicy
// treats a nil callback as "no observer", so callers can pass through
// unconditionally.
//
// The returned function is safe to invoke concurrently; AgentIPCClient is
// stateless modulo its mutex-protected lastActivityAt.
func DaemonActivityObserver() func(wrapper.Snapshot) {
	client := agentIPCClientFromDefaultBackend()
	if client == nil {
		return nil
	}
	return func(snap wrapper.Snapshot) {
		if snap.LastOutputAt.IsZero() {
			return
		}
		if err := client.Heartbeat(snap.LastOutputAt); err != nil {
			slog.Debug("agent IPC heartbeat failed", "err", err)
		}
	}
}

// agentIPCClientFromDefaultBackend returns the active AgentIPCClient when the
// global issue backend is an ipcIssueBackend wrapping one, else nil.
func agentIPCClientFromDefaultBackend() *AgentIPCClient {
	b := DefaultIssueBackend()
	ipcb, ok := b.(*ipcIssueBackend)
	if !ok {
		return nil
	}
	client, _ := ipcb.ipc.(*AgentIPCClient)
	return client
}

// --- API backend factory (remote --server mode) ---

// createAPIIssueBackend constructs an api.Backend for remote HTTP mode. It
// resolves the server URL from --server / LOOM_SERVER_URL and the workspace
// ID from --workspace / LOOM_WORKSPACE.
//
// Returns an error if the server URL is missing or invalid, the workspace
// cannot be resolved, or if http client construction fails. On success, the
// returned backend uses httpclient.Client for auth (OIDC device flow + token
// cache) transparently via api.AuthTransport.
func createAPIIssueBackend() (backend.IssueBackend, error) {
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

	slog.Info("api issue backend created", "url", serverURL, "workspace", workspaceID)
	return ab, nil
}
