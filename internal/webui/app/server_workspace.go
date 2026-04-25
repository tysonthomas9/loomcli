package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func wrapWorkspaceCreateFn(
	innerCreate service.WorkspaceCreateFn,
	registry *appinfra.WorkspaceRegistry,
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
			logger.Error("workspace creation returned empty WorkspaceID — skipping runtime registration",
				"workspace", req.Name)
			service.AddCreateWarning(ctx, "Could not register workspace with daemon — workspace may not auto-connect until restart")
			return result, nil
		}

		wsDir := result.WorkspacePath
		if wsDir == "" {
			logger.Warn("workspace creation returned empty WorkspacePath — skipping runtime registration",
				"workspace", req.Name)
			service.AddCreateWarning(ctx, "Could not determine workspace directory for daemon registration")
			return result, nil
		}

		if err := registry.Register(wsID, wsDir); err != nil {
			logger.Warn("workspace created but runtime registration failed",
				"workspace", req.Name, "workspace_id", wsID, "err", err)
			service.AddCreateWarning(ctx, "Workspace created but runtime registration failed — some features may be unavailable until restart")
		}
		// Subscriber activation is deferred — the workspace middleware's
		// wsExistsFn lazily activates it on the first API request.

		return result, nil
	}
}

// wrapWorkspaceDeleteFn wraps a workspace deletion function with post-deletion
// cleanup. After the inner delete succeeds, it deregisters the workspace from
// the WorkspaceRegistry (closing pools and stopping subscribers) and the
// FleetStoreRegistry (stopping fleet Store and TimeoutEnforcer).
func wrapWorkspaceDeleteFn(
	innerDelete func(name string) error,
	registry *appinfra.WorkspaceRegistry,
	resolveID webui.WorkspaceIDResolverFn,
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
				logger.Error("failed to resolve workspace UUID for deletion cleanup — pool and fleet store will leak until restart",
					"workspace", name, "err", err)
			} else if id == "" {
				logger.Error("workspace ID resolver returned empty UUID for deletion cleanup — pool and fleet store will leak until restart",
					"workspace", name)
			} else {
				wsID = id
				resolved = true
			}
		} else {
			logger.Error("no workspace ID resolver available — pool and fleet store will leak until restart",
				"workspace", name)
		}

		// 2. Perform the config deletion (the critical path — always proceed).
		if err := innerDelete(name); err != nil {
			return err
		}

		// 3. Clean up pool, subscriber, and fleet state atomically (only if UUID was resolved).
		if resolved && registry != nil {
			registry.Deregister(wsID)
			logger.Info("workspace cleaned up after deletion",
				"workspace", name, "id", wsID)
		}

		return nil
	}
}

// safeLogPath builds a log file path for an agent, guarding against path
// traversal. The basename mirrors cli/config.BuildAgentLogFilename so the
// webui worker API stays collision-free with the daemon supervisor writer.
// role is hardcoded to "task" because loom worker only ever runs task-role
// agents (see internal/cli/serve/worker/worker_cmd.go).
//
// The basename construction is duplicated here (rather than imported) because
// the webui-isolation depguard rule forbids internal/webui from importing
// internal/cli. If the canonical helper changes shape, update this function
// in lockstep — covered by the TestSafeLogPath_RepoDisambiguation regression.
func safeLogPath(basePath, repo, agent string) string {
	logsDir := filepath.Join(basePath, ".loom", "logs")
	candidate := filepath.Clean(filepath.Join(logsDir, agentLogBasename("task", repo, agent)))
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

// agentLogBasename mirrors cli/config.BuildAgentLogFilename. Each segment is
// sanitized with filepath.Base; a "." or ".." repo falls back to the two-
// segment legacy form to avoid filesystem-hostile basenames.
func agentLogBasename(role, repo, worktree string) string {
	safeRole := filepath.Base(role)
	safeWorktree := filepath.Base(worktree)
	if repo == "" {
		return fmt.Sprintf("%s-%s.log", safeRole, safeWorktree)
	}
	safeRepo := filepath.Base(repo)
	if safeRepo == "." || safeRepo == ".." {
		return fmt.Sprintf("%s-%s.log", safeRole, safeWorktree)
	}
	return fmt.Sprintf("%s-%s-%s.log", safeRole, safeRepo, safeWorktree)
}

func (app *Server) registerWorkerAPIRoutes() {
	configFn := app.config.WorkspaceConfigFn

	workerToken := os.Getenv("LOOM_WORKER_TOKEN")
	if workerToken == "" {
		return
	}

	handlermux.SetupWorkerAPIRoutes(app.mux, workerToken,
		workerResolveWorktree(configFn),
		workerResolveEventsDir(configFn),
		workerResolveLogPath(configFn),
		workerValidateWorkspace(configFn),
	)
	logger.Info("worker API routes registered", "component", "worker")
}

// workerValidateWorkspace returns a function that checks whether a workspace ID
// exists in the workspace config.
func workerValidateWorkspace(configFn func() (*ops.WorkspaceData, error)) func(string) bool {
	return func(id string) bool {
		if configFn == nil {
			return false
		}
		wsData, err := configFn()
		if err != nil {
			logger.Warn("workspace validation failed due to config error", "workspace_id", id, "err", err)
			return false
		}
		return wsData != nil && service.FindWorkspacePathByID(wsData, id) != ""
	}
}

// workerResolveWorktree returns a function that resolves a safe worktree path
// for the given workspace and agent, creating the directory if needed.
func workerResolveWorktree(configFn func() (*ops.WorkspaceData, error)) func(string, string) string {
	return func(workspace, agent string) string {
		wsPath := service.ResolveWorkspacePath(configFn, workspace)
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
		if err := os.MkdirAll(candidate, 0700); err != nil { //nolint:gosec // 0700 intentional for worktree dirs
			return ""
		}
		return candidate
	}
}

// workerResolveEventsDir returns a function that resolves the events directory
// path for a workspace.
func workerResolveEventsDir(configFn func() (*ops.WorkspaceData, error)) func(string) string {
	return func(workspace string) string {
		path := service.ResolveWorkspacePath(configFn, workspace)
		if path == "" {
			return ""
		}
		return filepath.Join(path, ".loom", "events")
	}
}

// workerResolveLogPath returns a function that resolves a safe log file path
// for a workspace agent, scoped by its repo when provided (workspace mode).
func workerResolveLogPath(configFn func() (*ops.WorkspaceData, error)) func(string, string, string) string {
	return func(workspace, repo, agent string) string {
		path := service.ResolveWorkspacePath(configFn, workspace)
		if path == "" {
			return ""
		}
		return safeLogPath(path, repo, agent)
	}
}
