package webui

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// WorkspaceData represents the full workspace topology returned by the API.
type WorkspaceData struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Path             string               `json:"path"`
	Repos            []WorkspaceRepo      `json:"repos"`
	Groups           []string             `json:"groups"`
	Agents           []WorkspaceAgentInfo `json:"agents"`
	Workspaces       []WorkspaceSummary   `json:"workspaces"`
	WorkspaceOrder   []string             `json:"workspace_order,omitempty"`
	DefaultWorkspace string               `json:"default_workspace"`
}

// WorkspaceSummary provides a lightweight summary of a configured workspace.
type WorkspaceSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Active    bool   `json:"active"`
	RepoCount int    `json:"repo_count"`
	IsDefault bool   `json:"is_default"`
	Backend   string `json:"backend,omitempty"`
}

// WorkspaceRepo represents a repository within a workspace.
type WorkspaceRepo struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	DefaultBranch string   `json:"default_branch"`
	Remote        string   `json:"remote"`
	SourceRepoID  string   `json:"source_repo_id,omitempty"`
	Groups        []string `json:"groups"`
}

// WorkspaceAgentInfo represents an agent's repo/group assignments.
type WorkspaceAgentInfo struct {
	Name       string   `json:"name"`
	Repos      []string `json:"repos"`
	RepoGroups []string `json:"repo_groups"`
	CrossRepo  bool     `json:"cross_repo"`
}

// reconcileConfigWorkspaces registers all configured workspaces in MultiPool
// at startup. Skips the initial workspace if it was already registered with a
// custom pool. Connection pools are lazy-connecting, so workspaces whose
// daemons are not running will connect on first request.
func reconcileConfigWorkspaces(
	listFn func() (map[string]string, error),
	initialID string,
	initialRegistered bool,
	multiPool *daemon.MultiPool,
	multiSub *MultiWorkspaceSubscriber,
	poolSize int,
) {
	if listFn == nil {
		return
	}
	workspaces, err := listFn()
	if err != nil {
		slog.Warn("failed to load workspace list for startup reconciliation", "err", err)
		return
	}
	for wsName, wsPath := range workspaces {
		if initialRegistered && wsName == initialID {
			continue
		}
		registerWorkspacePool(wsName, wsPath, multiPool, multiSub, poolSize)
	}
	slog.Info("startup reconciliation complete",
		"total_workspaces", len(workspaces),
		"registered", len(multiPool.WorkspaceIDs()))
}

// registerWorkspacePool creates a connection pool with circuit breaker for a
// workspace and registers it in the MultiPool + subscriber. Non-fatal: logs
// warnings on failure.
func registerWorkspacePool(
	wsName, wsPath string,
	multiPool *daemon.MultiPool,
	multiSub *MultiWorkspaceSubscriber,
	poolSize int,
) {
	socketPath := rpc.ShortSocketPath(wsPath)
	rawPool, err := daemon.NewConnectionPool(socketPath, poolSize)
	if err != nil {
		slog.Warn("failed to create connection pool for workspace",
			"workspace", wsName, "socket", socketPath, "err", err)
		return
	}
	breaker := circuitbreaker.NewBreaker("ws-"+wsName, circuitbreaker.Config{
		FailureThreshold:  5,
		OpenTimeout:       30 * time.Second,
		HalfOpenMaxProbes: 1,
		ShouldTrip:        daemon.DaemonShouldTrip,
		OnStateChange: func(from, to circuitbreaker.State) {
			slog.Info("circuit breaker state change", "component", "circuit_breaker", "workspace", wsName, "from", from, "to", to)
		},
	})
	pool := daemon.NewProtectedPool(rawPool, breaker)

	if err := multiPool.Register(wsName, pool); err != nil {
		slog.Warn("failed to register pool for workspace",
			"workspace", wsName, "err", err)
		return
	}
	slog.Info("registered connection pool for workspace",
		"workspace", wsName, "socket", socketPath)

	if err := multiSub.AddWorkspace(wsName); err != nil {
		slog.Warn("failed to start subscriber for workspace",
			"workspace", wsName, "err", err)
	} else {
		slog.Info("started subscriber for workspace", "workspace", wsName)
	}
}

func wrapWorkspaceCreateFn(
	innerCreate WorkspaceCreateFn,
	multiPool *daemon.MultiPool,
	multiSub *MultiWorkspaceSubscriber,
	poolSize int,
) WorkspaceCreateFn {
	if innerCreate == nil {
		return nil
	}
	return func(ctx context.Context, req WorkspaceCreateRequest) error {
		if err := innerCreate(ctx, req); err != nil {
			return err
		}

		// Determine the workspace directory (mirrors GetWorkspaceDir logic in cli/config.go)
		wsDir := req.Path
		if wsDir == "" {
			configDir := os.Getenv("LOOM_CONFIG_DIR")
			if configDir == "" {
				if homeDir, err := os.UserHomeDir(); err == nil {
					configDir = filepath.Join(homeDir, ".loom")
				}
			}
			if configDir != "" {
				wsDir = filepath.Join(configDir, "workspaces", req.Name)
			}
		}
		if wsDir == "" {
			slog.Warn("cannot determine workspace dir for pool registration", "workspace", req.Name)
			return nil
		}
		wsDir = filepath.Clean(wsDir)

		registerWorkspacePool(req.Name, wsDir, multiPool, multiSub, poolSize)
		return nil
	}
}

// findWorkspacePathByID scans workspace summaries for a matching UUID and
// returns its filesystem path. Returns empty string if not found.
func findWorkspacePathByID(wsData *WorkspaceData, id string) string {
	if wsData == nil {
		return ""
	}
	for _, ws := range wsData.Workspaces {
		if ws.ID == id {
			return ws.Path
		}
	}
	return ""
}

// resolveWorkspacePath loads config and resolves a workspace UUID to its
// filesystem path. Returns empty string on any failure.
func resolveWorkspacePath(configFn func() (*WorkspaceData, error), workspaceID string) string {
	if configFn == nil {
		return ""
	}
	wsData, err := configFn()
	if err != nil || wsData == nil {
		return ""
	}
	return findWorkspacePathByID(wsData, workspaceID)
}

// safeLogPath builds a log file path for an agent, guarding against path traversal.
func safeLogPath(basePath, agent string) string {
	logsDir := filepath.Join(basePath, ".loom", "logs")
	candidate := filepath.Clean(filepath.Join(logsDir, fmt.Sprintf("task-%s.log", agent)))
	absLogs, err := filepath.Abs(logsDir)
	if err != nil {
		return ""
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(absCandidate, absLogs+string(filepath.Separator)) {
		return "" // agent name escapes log dir
	}
	return candidate
}

func registerWorkerAPIRoutes(mux *http.ServeMux, workspaceConfigFn func() (*WorkspaceData, error)) {
	workerToken := os.Getenv("LOOM_WORKER_TOKEN")
	if workerToken == "" {
		return
	}

	validateWorkspace := func(id string) bool {
		if workspaceConfigFn == nil {
			return false
		}
		wsData, err := workspaceConfigFn()
		if err != nil {
			slog.Warn("workspace validation failed due to config error", "workspace_id", id, "err", err)
			return false
		}
		if wsData == nil {
			return false
		}
		return findWorkspacePathByID(wsData, id) != ""
	}

	SetupWorkerAPIRoutes(mux, workerToken,
		func(workspace, agent string) string {
			return resolveWorkspacePath(workspaceConfigFn, workspace)
		},
		func(workspace string) string {
			path := resolveWorkspacePath(workspaceConfigFn, workspace)
			if path == "" {
				return ""
			}
			return filepath.Join(path, ".loom", "events")
		},
		func(workspace, agent string) string {
			path := resolveWorkspacePath(workspaceConfigFn, workspace)
			if path == "" {
				return ""
			}
			return safeLogPath(path, agent)
		},
		validateWorkspace,
	)
	slog.Info("worker API routes registered", "component", "worker")
}
