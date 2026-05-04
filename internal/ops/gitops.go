package ops

// GitOps defines the interface for git operations on agent worktrees.
// This interface breaks the import cycle between webui and cli packages.
// The cli package provides the concrete implementation.
type GitOps interface {
	// ResolveAgentWorktree resolves an agent name to its worktree info.
	// workspaceID scopes discovery to a specific workspace (empty = default).
	ResolveAgentWorktree(workspaceID, name string) (*AgentWorktree, error)

	// Push merges the source branch into the target branch (loom push semantics).
	Push(worktreePath, sourceBranch, targetBranch, remote string) (*GitPushResult, error)

	// Pull merges the source branch into the worktree's current branch.
	Pull(worktreePath, currentBranch, sourceBranch, remote string) (*GitPullResult, error)

	// CreatePR creates a GitHub PR from the source branch to the target branch.
	CreatePR(worktreePath, sourceBranch, targetBranch, remote string) (*GitPRResult, error)

	// Reset hard-resets a worktree to a target branch.
	// If push is true, force-pushes the branch to origin after resetting.
	Reset(worktreePath, worktreeName, targetBranch string, force, push bool) (*GitResetResult, error)

	// Status returns comprehensive git status for a worktree.
	Status(worktreePath, targetBranch string) (*GitStatusResult, error)

	// GetCurrentBranch returns the current branch of a worktree.
	GetCurrentBranch(worktreePath string) (string, error)

	// CheckGhInstalled checks if the gh CLI is available.
	CheckGhInstalled() error

	// SetRepoDefaultBranch updates the target branch for a named repo.
	// workspaceID scopes resolution to a specific workspace (empty = default).
	SetRepoDefaultBranch(workspaceID, repoName, branch string) error

	// ListAgentWorktrees returns all agent worktrees.
	// workspaceID scopes discovery to a specific workspace (empty = default).
	ListAgentWorktrees(workspaceID string) ([]AgentWorktree, error)

	// DiffStat returns line-level diff statistics for a worktree vs a base ref.
	DiffStat(worktreePath, fromRef string) DiffStatResult

	// ResolveMergeBase returns the merge-base commit hash between branch and HEAD.
	ResolveMergeBase(worktreePath, branch string) (string, error)

	// DiffCommits returns the list of commits between mergeBase and HEAD.
	DiffCommits(worktreePath, mergeBase string, limit int) ([]DiffCommitResult, error)

	// DiffFiles returns the list of changed files between two refs.
	DiffFiles(worktreePath, from, to string) ([]DiffFileResult, error)

	// DiffFilePatch returns the unified diff patch for a single file between two refs.
	DiffFilePatch(worktreePath, from, to, path string) (*DiffFilePatchResult, error)
}

// AgentWorktree contains resolved worktree info for an agent.
type AgentWorktree struct {
	Name          string
	Path          string
	Branch        string
	DefaultBranch string // integration/target branch
	Remote        string // git remote name (empty = "origin")
	RepoName      string // workspace repo name
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
	Pushed         bool   `json:"pushed"`
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

// DiffStatResult contains line-level diff statistics for a worktree.
type DiffStatResult struct {
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
}

// DiffCommitResult contains metadata for a single commit in a diff range.
type DiffCommitResult struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"short_hash"`
	Subject   string `json:"subject"`
	Author    string `json:"author"`
	Email     string `json:"email"`
	Date      string `json:"date"`
}

// DiffFileResult contains the status and stats for a changed file.
type DiffFileResult struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	OldPath   string `json:"old_path,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// DiffFilePatchResult contains the unified diff patch for a single file.
type DiffFilePatchResult struct {
	Patch      string `json:"patch"`
	IsBinary   bool   `json:"is_binary"`
	IsTooLarge bool   `json:"is_too_large"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
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
