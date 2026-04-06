package ops

// FileOps defines the interface for file operations on agent worktrees.
// This interface breaks the import cycle between webui and cli packages.
// The cli package provides the concrete implementation.
//
// FileOps only requires ResolveAgentWorktree because the actual file I/O
// (read/write/listdir) happens directly in the handlers using the resolved
// worktree path. The interface exists solely for agent resolution.
type FileOps interface {
	// ResolveAgentWorktree resolves an agent name to its worktree info.
	// Reuses the same AgentWorktree type from gitops.go.
	// workspaceID scopes discovery to a specific workspace (empty = default).
	ResolveAgentWorktree(workspaceID, name string) (*AgentWorktree, error)
}
