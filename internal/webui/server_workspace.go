package webui

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
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

// reconcileConfigWorkspaces registers all configured workspaces via the
// WorkspaceRegistry at startup. Skips the initial workspace if it was already
// registered with a custom pool. Connection pools are lazy-connecting, so
// workspaces whose daemons are not running will connect on first request.
func reconcileConfigWorkspaces(
	listFn func() (map[string]string, error),
	initialID string,
	initialRegistered bool,
	registry *WorkspaceRegistry,
	fleetRegistry *fleet.StoreRegistry,
) {
	if listFn == nil {
		return
	}
	workspaces, err := listFn()
	if err != nil {
		slog.Warn("failed to load workspace list for startup reconciliation", "err", err)
		return
	}
	for wsID, wsPath := range workspaces {
		if initialRegistered && wsID == initialID {
			continue
		}
		_ = registry.Register(wsID, wsPath)
		if fleetRegistry != nil {
			if err := fleetRegistry.Register(wsID); err != nil {
				slog.Warn("failed to register workspace in fleet registry",
					"workspace", wsID, "err", err)
			}
		}
	}
	slog.Info("startup reconciliation complete",
		"total_workspaces", len(workspaces),
		"registered", len(registry.WorkspaceIDs()))
}

func wrapWorkspaceCreateFn(
	innerCreate WorkspaceCreateFn,
	registry *WorkspaceRegistry,
	fleetRegistry *fleet.StoreRegistry,
) WorkspaceCreateFn {
	if innerCreate == nil {
		return nil
	}
	return func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		result, err := innerCreate(ctx, req)
		if err != nil {
			return result, err
		}

		// Use the workspace ID returned by innerCreate directly,
		// eliminating the need for a post-creation config re-read.
		wsID := result.WorkspaceID
		if wsID == "" {
			slog.Error("workspace creation returned empty WorkspaceID — skipping runtime registration",
				"workspace", req.Name)
			return result, nil
		}

		wsDir := result.WorkspacePath
		if wsDir == "" {
			slog.Warn("workspace creation returned empty WorkspacePath — skipping runtime registration",
				"workspace", req.Name)
			return result, nil
		}

		_ = registry.Register(wsID, wsDir)

		// Register workspace in fleet store registry (non-fatal on error).
		if fleetRegistry != nil {
			if err := fleetRegistry.Register(wsID); err != nil {
				slog.Warn("failed to register workspace in fleet registry",
					"workspace", wsID, "err", err)
			}
		}

		return result, nil
	}
}

// wrapWorkspaceDeleteFn wraps a workspace deletion function with post-deletion
// cleanup. After the inner delete succeeds, it deregisters the workspace from
// the WorkspaceRegistry (closing pools and stopping subscribers) and the
// FleetStoreRegistry (stopping fleet Store and TimeoutEnforcer).
func wrapWorkspaceDeleteFn(
	innerDelete func(name string) error,
	registry *WorkspaceRegistry,
	fleetRegistry *fleet.StoreRegistry,
	resolveID WorkspaceIDResolverFn,
) func(name string) error {
	if innerDelete == nil {
		return nil
	}
	return func(name string) error {
		// 1. Resolve UUID BEFORE deletion (config entry is removed by innerDelete).
		var wsID string
		var resolved bool
		if resolveID != nil {
			if id, err := resolveID(name); err != nil {
				slog.Error("failed to resolve workspace UUID for deletion cleanup — pool and fleet store will leak until restart",
					"workspace", name, "err", err)
			} else if id == "" {
				slog.Error("workspace ID resolver returned empty UUID for deletion cleanup — pool and fleet store will leak until restart",
					"workspace", name)
			} else {
				wsID = id
				resolved = true
			}
		} else {
			slog.Error("no workspace ID resolver available — pool and fleet store will leak until restart",
				"workspace", name)
		}

		// 2. Perform the config deletion (the critical path — always proceed).
		if err := innerDelete(name); err != nil {
			return err
		}

		// 3. Clean up pool and subscriber state (only if UUID was resolved).
		if resolved && registry != nil {
			registry.Deregister(wsID)
			slog.Info("workspace pool and subscriber cleaned up after deletion",
				"workspace", name, "id", wsID)
		}

		// 4. Clean up fleet state (only if UUID was resolved).
		if resolved && fleetRegistry != nil {
			fleetRegistry.Deregister(wsID)
			slog.Info("workspace fleet store cleaned up after deletion",
				"workspace", name, "id", wsID)
		}

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
			wsPath := resolveWorkspacePath(workspaceConfigFn, workspace)
			if wsPath == "" || agent == "" {
				return ""
			}
			candidate := filepath.Clean(filepath.Join(wsPath, "worktrees", agent))
			absBase, err := filepath.Abs(wsPath)
			if err != nil {
				return ""
			}
			absCandidate, err := filepath.Abs(candidate)
			if err != nil {
				return ""
			}
			if !strings.HasPrefix(absCandidate, absBase+string(filepath.Separator)) {
				return "" // agent name escapes workspace dir
			}
			if err := os.MkdirAll(candidate, 0700); err != nil {
				return ""
			}
			return candidate
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
