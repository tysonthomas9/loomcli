package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
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
		// Prefer the argument as the cleanup key. Store-backed workspace
		// delete passes the stable fleet-db key here, so resolver failures
		// must not leak runtime pools/subscribers/terminal managers.
		wsID := name
		if resolveID != nil {
			if id, err := resolveID(name); err != nil {
				logger.Warn("failed to resolve workspace ID for deletion cleanup; using delete key",
					"workspace", name, "err", err)
			} else if id == "" {
				logger.Warn("workspace ID resolver returned empty ID for deletion cleanup; using delete key",
					"workspace", name)
			} else {
				wsID = id
			}
		}

		// 2. Perform the config deletion (the critical path — always proceed).
		if err := innerDelete(name); err != nil {
			return err
		}

		// 3. Clean up pool, subscriber, and fleet state atomically.
		if wsID != "" && registry != nil {
			registry.Deregister(wsID)
			logger.Info("workspace cleaned up after deletion",
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
	workerToken := os.Getenv("LOOM_WORKER_TOKEN")
	if workerToken == "" {
		return
	}

	handlermux.SetupWorkerAPIRoutes(app.mux, workerToken,
		workerResolveWorktree(app.config.Store),
		workerResolveEventsDir(app.config.Store),
		workerResolveLogPath(app.config.Store),
		workerValidateWorkspace(app.config.Store),
	)
	logger.Info("worker API routes registered", "component", "worker")
}

// workerValidateWorkspace returns a function that checks whether a workspace ID
// exists in FleetDB.
func workerValidateWorkspace(st store.Store) func(string) bool {
	return func(id string) bool {
		if st == nil {
			return false
		}
		if _, err := st.Workspaces().Get(context.Background(), id); err != nil {
			logger.Warn("workspace validation failed", "workspace_id", id, "err", err)
			return false
		}
		return storeadapter.ResolveWorkspacePath(id) != ""
	}
}

// workerResolveWorktree returns a function that resolves a safe worktree path
// for the given workspace and agent, creating the directory if needed.
func workerResolveWorktree(st store.Store) func(string, string) string {
	return func(workspace, agent string) string {
		if st == nil {
			return ""
		}
		wsPath := storeadapter.ResolveWorkspacePath(workspace)
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
func workerResolveEventsDir(st store.Store) func(string) string {
	return func(workspace string) string {
		if st == nil {
			return ""
		}
		path := storeadapter.ResolveWorkspacePath(workspace)
		if path == "" {
			return ""
		}
		return filepath.Join(path, ".loom", "events")
	}
}

// workerResolveLogPath returns a function that resolves a safe log file path
// for a workspace agent.
func workerResolveLogPath(st store.Store) func(string, string) string {
	return func(workspace, agent string) string {
		if st == nil {
			return ""
		}
		path := storeadapter.ResolveWorkspacePath(workspace)
		if path == "" {
			return ""
		}
		return safeLogPath(path, agent)
	}
}
