package ops

// FileOps defines the interface for resolving the roots that file operations
// run against. This interface breaks the import cycle between webui and cli
// packages; the cli package provides the concrete implementation.
//
// FileOps only resolves roots and workspace topology — the actual file I/O
// (read/write/listdir) happens in the service layer against the resolved path.
// Agent roots are an agent worktree; the workspace root is the workspace folder
// that contains every repo checkout and agent worktree.
type FileOps interface {
	// ResolveAgentWorktree resolves an agent name to its worktree info.
	// Reuses the same AgentWorktree type from gitops.go.
	// workspaceID scopes discovery to a specific workspace (empty = default).
	ResolveAgentWorktree(workspaceID, name string) (*AgentWorktree, error)

	// ResolveAgentWorktreeOrPrimary resolves an agent name to its worktree,
	// falling back to the workspace's primary repo worktree when the agent is
	// a lead with no local worktree of its own (leads intentionally have none
	// — see svcimpl.ensureLocalAgentWorktrees). Non-lead agents keep
	// ResolveAgentWorktree semantics (a missing worktree still errors). Used by
	// the read-only file viewer so leads can browse the primary repo without a
	// 404. Write paths must NOT use this — they resolve the agent worktree
	// directly so leads cannot mutate the primary worktree from the viewer.
	ResolveAgentWorktreeOrPrimary(workspaceID, name string) (*AgentWorktree, error)

	// ResolveWorkspaceRoot resolves a workspace to its root folder — the
	// directory that contains every repo checkout and agent worktree — so the
	// file browser can navigate the whole workspace from a single root. Returns
	// the absolute folder path, or an error when the workspace has no local path
	// on this machine (e.g. a distributed/cloud workspace that is not checked
	// out locally).
	ResolveWorkspaceRoot(workspaceID string) (string, error)

	// ResolveWorkspaceData returns the workspace topology used to validate
	// repo/agent file-browser targets against workspace state.
	ResolveWorkspaceData(workspaceID string) (*WorkspaceData, error)

	// GitStatusPorcelain returns git status --porcelain XY codes keyed by
	// checkout-relative path. It is read-only decoration data for file-browser
	// status and conflict badges.
	GitStatusPorcelain(worktreePath string) (map[string]string, error)
}
