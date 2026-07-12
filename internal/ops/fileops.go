package ops

import (
	"context"
	"errors"
)

var (
	// ErrAgentRepoNotAllowed indicates that a repo qualifier is unknown or not
	// part of the agent's allowed repo set.
	ErrAgentRepoNotAllowed = errors.New("agent repo not allowed")
	// ErrAgentWorktreeNotFound indicates that a valid agent+repo checkout is not
	// present on this machine.
	ErrAgentWorktreeNotFound = errors.New("agent worktree not found")
	// ErrCheckoutTargetNotAllowed indicates that a requested checkout target is
	// unknown, not allowed for the agent, or otherwise outside the workspace map.
	ErrCheckoutTargetNotAllowed = errors.New("checkout target not allowed")
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
	GitStatusPorcelain(ctx context.Context, worktreePath string) (GitFileStatusResult, error)

	// GitShowFileAtRev returns file content from a git revision, capped to
	// maxBytes, keyed by checkout-relative path.
	GitShowFileAtRev(ctx context.Context, worktreePath, rev, path string, maxBytes int64) (*GitFileContentAtRev, error)

	// GitDiffFile returns a unified diff for one checkout-relative file path.
	// When to is empty, the diff compares from against the working tree.
	GitDiffFile(ctx context.Context, worktreePath, path, from, to string) (GitBoundedTextResult, error)

	// GitLogFile returns bounded git log output for one checkout-relative file path.
	GitLogFile(ctx context.Context, worktreePath, path string, limit int) (GitBoundedTextResult, error)

	// GitBlamePorcelain returns git blame --porcelain output for one file path.
	GitBlamePorcelain(ctx context.Context, worktreePath, path string) (GitBoundedTextResult, error)

	// GetCurrentBranch returns the current branch for a git checkout. It is
	// best-effort metadata for file checkout enumeration.
	GitCurrentBranch(ctx context.Context, worktreePath string) (string, error)

	// RepairCheckout repairs or provisions a known workspace checkout. scope is
	// "agent" or "repo"; target is the agent name or repo name; repo qualifies
	// agent scope. Implementations must validate target/repo against workspace
	// topology and never accept arbitrary paths from callers.
	RepairCheckout(workspaceID, scope, target, repo string, force bool) (RepairResult, error)
}

// GitFileStatusResult is bounded, parsed porcelain status.
type GitFileStatusResult struct {
	Entries  map[string]string
	Partial  bool
	LimitHit bool
}

// GitBoundedTextResult is bounded Git text output.
type GitBoundedTextResult struct {
	Output   string
	Partial  bool
	LimitHit bool
}

// GitFileContentAtRev contains bounded content returned from a git revision.
type GitFileContentAtRev struct {
	Content   []byte
	Size      int64
	Truncated bool
}

// RepairResult is returned by file checkout repair/provision operations.
type RepairResult struct {
	Repaired      bool   `json:"repaired"`
	Method        string `json:"method"`
	RequiresForce bool   `json:"requires_force,omitempty"`
	BackupPath    string `json:"backup_path,omitempty"`
	Message       string `json:"message"`
}
