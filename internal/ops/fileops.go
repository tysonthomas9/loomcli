package ops

import "errors"

var (
	// ErrAgentRepoNotAllowed indicates that a repo qualifier is unknown or not
	// part of the agent's allowed repo set.
	ErrAgentRepoNotAllowed = errors.New("agent repo not allowed")
	// ErrAgentWorktreeNotFound indicates that a valid agent+repo checkout is not
	// present on this machine.
	ErrAgentWorktreeNotFound = errors.New("agent worktree not found")
)

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

	// ResolveAgentWorktreeForRepo resolves a specific agent+repo checkout under
	// <workspace>/worktrees/<repo>/<agent>. The repo must be known and allowed
	// for the agent; invalid repo qualifiers return ErrAgentRepoNotAllowed, and
	// missing local checkouts return ErrAgentWorktreeNotFound.
	ResolveAgentWorktreeForRepo(workspaceID, name, repo string) (*AgentWorktree, error)

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

	// GitShowFileAtRev returns file content from a git revision, capped to
	// maxBytes, keyed by checkout-relative path.
	GitShowFileAtRev(worktreePath, rev, path string, maxBytes int64) (*GitFileContentAtRev, error)

	// GitDiffFile returns a unified diff for one checkout-relative file path.
	// When to is empty, the diff compares from against the working tree.
	GitDiffFile(worktreePath, path, from, to string) (string, error)

	// GitLogFile returns bounded git log output for one checkout-relative file path.
	GitLogFile(worktreePath, path string, limit int) (string, error)

	// GitBlamePorcelain returns git blame --porcelain output for one file path.
	GitBlamePorcelain(worktreePath, path string) (string, error)

	// ResolveLoomDataDir resolves the local loom data/config directory using
	// the established CLI resolver instead of callers reading env directly.
	ResolveLoomDataDir() (string, error)

	// GetCurrentBranch returns the current branch for a git checkout. It is
	// best-effort metadata for file checkout enumeration.
	GetCurrentBranch(worktreePath string) (string, error)
}

// GitFileContentAtRev contains bounded content returned from a git revision.
type GitFileContentAtRev struct {
	Content   []byte
	Size      int64
	Truncated bool
}
