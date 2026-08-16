package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

type workspaceRecordSource interface {
	Workspaces() workspaceowner.WorkspaceStore
}

func wrapWorkspaceCreateFn(
	innerCreate workspacecoord.WorkspaceCreateFn,
	registry *WorkspaceRegistry,
) workspacecoord.WorkspaceCreateFn {
	if innerCreate == nil {
		return nil
	}
	return func(ctx context.Context, req workspacecoord.WorkspaceCreateRequest) (workspacecoord.WorkspaceCreateResult, error) {
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
			workspacecoord.AddCreateWarning(ctx, "Could not register workspace runtime — workspace may not auto-connect until restart")
			return result, nil
		}

		wsDir := result.WorkspacePath
		if wsDir == "" {
			logger.Warn("workspace creation returned empty WorkspacePath — skipping runtime registration",
				"workspace", req.Name)
			workspacecoord.AddCreateWarning(ctx, "Could not determine workspace directory for runtime registration")
			return result, nil
		}

		if err := registry.Register(wsID, wsDir); err != nil {
			logger.Warn("workspace created but runtime registration failed",
				"workspace", req.Name, "workspace_id", wsID, "err", err)
			workspacecoord.AddCreateWarning(ctx, "Workspace created but runtime registration failed — some features may be unavailable until restart")
		}
		// Subscriber activation is deferred to the workspace SSE token/stream
		// routes so ordinary REST traffic does not start FleetDB long-polls.

		return result, nil
	}
}

// wrapWorkspaceDeleteCleanupFn composes machine-local cleanup that runs only
// after the Workspace owner command has durably deleted the aggregate.
func wrapWorkspaceDeleteCleanupFn(
	innerCleanup func(string) error,
	registry *WorkspaceRegistry,
) func(string) error {
	if innerCleanup == nil && registry == nil {
		return nil
	}
	return func(key string) error {
		var cleanupErr error
		if innerCleanup != nil {
			cleanupErr = innerCleanup(key)
		}
		if key != "" && registry != nil {
			registry.Deregister(key)
			logger.Info("workspace runtime cleaned up after owner deletion", "workspace_id", key)
		}
		return cleanupErr
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

	SetupWorkerAPIRoutes(app.mux, workerToken,
		workerResolveWorktree(app.config.ProjectionRecords),
		workerResolveEventsDir(app.config.ProjectionRecords),
		workerResolveLogPath(app.config.ProjectionRecords),
		workerValidateWorkspace(app.config.ProjectionRecords),
	)
	logger.Info("worker API routes registered", "component", "worker")
}

// workerValidateWorkspace returns a function that checks whether a workspace ID
// exists in FleetDB.
func workerValidateWorkspace(st workspaceRecordSource) func(string) bool {
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
func workerResolveWorktree(st workspaceRecordSource) func(string, string) string {
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
func workerResolveEventsDir(st workspaceRecordSource) func(string) string {
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
func workerResolveLogPath(st workspaceRecordSource) func(string, string) string {
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
