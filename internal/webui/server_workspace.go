package webui

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// reconcileConfigWorkspaces registers all configured workspaces via the
// WorkspaceRegistry at startup. Skips the initial workspace if it was already
// registered with a custom pool. Connection pools are lazy-connecting, so
// workspaces whose daemons are not running will connect on first request.
func reconcileConfigWorkspaces(
	listFn func() (map[string]string, error),
	initialID string,
	initialRegistered bool,
	registry *WorkspaceRegistry,
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
	}
	slog.Info("startup reconciliation complete",
		"total_workspaces", len(workspaces),
		"registered", len(registry.WorkspaceIDs()))
}

func wrapWorkspaceCreateFn(
	innerCreate service.WorkspaceCreateFn,
	registry *WorkspaceRegistry,
) service.WorkspaceCreateFn {
	if innerCreate == nil {
		return nil
	}
	return func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
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
			service.AddCreateWarning(ctx, "Could not register workspace with daemon — workspace may not auto-connect until restart")
			return result, nil
		}

		wsDir := result.WorkspacePath
		if wsDir == "" {
			slog.Warn("workspace creation returned empty WorkspacePath — skipping runtime registration",
				"workspace", req.Name)
			service.AddCreateWarning(ctx, "Could not determine workspace directory for daemon registration")
			return result, nil
		}

		_ = registry.Register(wsID, wsDir)

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

		// 3. Clean up pool, subscriber, and fleet state atomically (only if UUID was resolved).
		if resolved && registry != nil {
			registry.Deregister(wsID)
			slog.Info("workspace cleaned up after deletion",
				"workspace", name, "id", wsID)
		}

		return nil
	}
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

func (app *Server) registerWorkerAPIRoutes() {
	workspaceConfigFn := app.config.WorkspaceConfigFn

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
		return service.FindWorkspacePathByID(wsData, id) != ""
	}

	SetupWorkerAPIRoutes(app.mux, workerToken,
		func(workspace, agent string) string {
			wsPath := service.ResolveWorkspacePath(workspaceConfigFn, workspace)
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
			path := service.ResolveWorkspacePath(workspaceConfigFn, workspace)
			if path == "" {
				return ""
			}
			return filepath.Join(path, ".loom", "events")
		},
		func(workspace, agent string) string {
			path := service.ResolveWorkspacePath(workspaceConfigFn, workspace)
			if path == "" {
				return ""
			}
			return safeLogPath(path, agent)
		},
		validateWorkspace,
	)
	slog.Info("worker API routes registered", "component", "worker")
}
