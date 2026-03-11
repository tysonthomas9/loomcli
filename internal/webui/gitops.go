package webui

// GitOps defines the interface for git operations on agent worktrees.
// This interface breaks the import cycle between webui and cli packages.
// The cli package provides the concrete implementation.
type GitOps interface {
	// ResolveAgentWorktree resolves an agent name to its worktree info.
	ResolveAgentWorktree(name string) (*AgentWorktree, error)

	// Push merges the source branch into the target branch (loom push semantics).
	Push(worktreePath, sourceBranch, targetBranch, remote string) (*GitPushResult, error)

	// Pull merges the source branch into the worktree's current branch.
	Pull(worktreePath, currentBranch, sourceBranch, remote string) (*GitPullResult, error)

	// CreatePR creates a GitHub PR from the source branch to the target branch.
	CreatePR(worktreePath, sourceBranch, targetBranch, remote string) (*GitPRResult, error)

	// Reset hard-resets a worktree to a target branch.
	Reset(worktreePath, worktreeName, targetBranch string, force bool) (*GitResetResult, error)

	// Status returns comprehensive git status for a worktree.
	Status(worktreePath, targetBranch string) (*GitStatusResult, error)

	// GetCurrentBranch returns the current branch of a worktree.
	GetCurrentBranch(worktreePath string) (string, error)

	// CheckGhInstalled checks if the gh CLI is available.
	CheckGhInstalled() error

	// SetRepoDefaultBranch updates the target branch for a named repo.
	SetRepoDefaultBranch(repoName, branch string) error
}

// AgentWorktree contains resolved worktree info for an agent.
type AgentWorktree struct {
	Name          string
	Path          string
	Branch        string
	DefaultBranch string // integration/target branch
	Remote        string // git remote name (empty = "origin")
	RepoName      string // workspace repo name (empty in legacy mode)
	IsWorkspace   bool   // true if workspace mode
}

// GitPushResult contains the result of a push operation.
type GitPushResult struct {
	Success         bool     `json:"success"`
	Message         string   `json:"message"`
	AlreadyUpToDate bool     `json:"already_up_to_date"`
	ConflictedFiles []string `json:"conflicted_files,omitempty"`
}

// GitPullResult contains the result of a pull operation.
type GitPullResult struct {
	Success         bool     `json:"success"`
	Message         string   `json:"message"`
	AlreadyUpToDate bool     `json:"already_up_to_date"`
	ConflictedFiles []string `json:"conflicted_files,omitempty"`
}

// GitPRResult contains the result of a PR creation.
type GitPRResult struct {
	URL           string `json:"url,omitempty"`
	Created       bool   `json:"created"`
	AlreadyExists bool   `json:"already_exists"`
	NoCommits     bool   `json:"no_commits"`
}

// GitResetResult contains the result of a reset operation.
type GitResetResult struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	PreviousBranch string `json:"previous_branch,omitempty"`
}

// GitResetLockedError indicates a worktree is locked by an active agent.
type GitResetLockedError struct {
	AgentName string
	PID       int
	Duration  string
	TaskID    string
}

func (e *GitResetLockedError) Error() string {
	return "agent locked"
}

// GitStatusResult contains comprehensive git status for a worktree.
type GitStatusResult struct {
	Branch          string   `json:"branch"`
	TargetBranch    string   `json:"target_branch"`
	IsClean         bool     `json:"is_clean"`
	Ahead           int      `json:"ahead"`
	Behind          int      `json:"behind"`
	ChangedFiles    []string `json:"changed_files"`
	ConflictedFiles []string `json:"conflicted_files"`
	HasConflicts    bool     `json:"has_conflicts"`
	StashCount      int      `json:"stash_count"`
}
