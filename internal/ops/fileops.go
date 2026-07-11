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

	// ResolveAgentWorktreeOrPrimary resolves an agent name to its worktree,
	// falling back to the workspace's primary repo worktree when the agent is
	// a lead with no local worktree of its own (leads intentionally have none
	// — see svcimpl.ensureLocalAgentWorktrees). Non-lead agents keep
	// ResolveAgentWorktree semantics (a missing worktree still errors). Used by
	// the read-only file viewer so leads can browse the primary repo without a
	// 404. Write paths must NOT use this — they resolve the agent worktree
	// directly so leads cannot mutate the primary worktree from the viewer.
	ResolveAgentWorktreeOrPrimary(workspaceID, name string) (*AgentWorktree, error)
}
