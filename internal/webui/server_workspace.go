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

		// Compute the daemon socket path and create a connection pool with circuit breaker
		socketPath := rpc.ShortSocketPath(wsDir)
		newRawPool, err := daemon.NewConnectionPool(socketPath, poolSize)
		if err != nil {
			slog.Warn("failed to create connection pool for new workspace",
				"workspace", req.Name, "socket", socketPath, "err", err)
			return nil // non-fatal: workspace was created successfully
		}
		breaker := circuitbreaker.NewBreaker("ws-"+req.Name, circuitbreaker.Config{
			FailureThreshold: 5,
			OpenTimeout:      30 * time.Second,
		})
		newPool := daemon.NewProtectedPool(newRawPool, breaker)

		if err := multiPool.Register(req.Name, newPool); err != nil {
			slog.Warn("failed to register pool for new workspace",
				"workspace", req.Name, "err", err)
			return nil
		}
		slog.Info("registered connection pool for new workspace",
			"workspace", req.Name, "socket", socketPath)

		// Start a DaemonSubscriber for the new workspace
		if err := multiSub.AddWorkspace(req.Name); err != nil {
			slog.Warn("failed to start subscriber for new workspace",
				"workspace", req.Name, "err", err)
		} else {
			slog.Info("started subscriber for new workspace", "workspace", req.Name)
		}

		return nil
	}
}

func registerWorkerAPIRoutes(mux *http.ServeMux, workspaceConfigFn func() (*WorkspaceData, error)) {
	workerToken := os.Getenv("LOOM_WORKER_TOKEN")
	if workerToken == "" {
		return
	}
	SetupWorkerAPIRoutes(mux, workerToken,
		// resolveWorktreePath: map (workspace, agent) to filesystem path
		func(workspace, agent string) string {
			if workspaceConfigFn == nil {
				return ""
			}
			wsData, err := workspaceConfigFn()
			if err != nil || wsData == nil {
				return ""
			}
			// Use the workspace path as the worktree base
			return wsData.Path
		},
		// resolveEventsDir: map workspace to events directory
		func(workspace string) string {
			if workspaceConfigFn == nil {
				return ""
			}
			wsData, err := workspaceConfigFn()
			if err != nil || wsData == nil {
				return ""
			}
			return filepath.Join(wsData.Path, ".loom", "events")
		},
		// resolveLogPath: map (workspace, agent) to log file path
		func(workspace, agent string) string {
			if workspaceConfigFn == nil {
				return ""
			}
			wsData, err := workspaceConfigFn()
			if err != nil || wsData == nil {
				return ""
			}
			logsDir := filepath.Join(wsData.Path, ".loom", "logs")
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
		},
	)
	slog.Info("worker API routes registered", "component", "worker")
}
