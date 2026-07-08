package service

import "context"

// FileScope identifies the root a scoped file operation resolves against.
// Phase 1 supports only ScopeWorkspace (the workspace folder, read-only). The
// type is defined so repo/agent scopes and write operations can be added later
// without reshaping the API contract.
type FileScope string

const (
	// ScopeWorkspace roots operations at the workspace folder — the parent
	// directory that contains every repo checkout and agent worktree.
	ScopeWorkspace FileScope = "workspace"
	// ScopeRepo roots operations at a named workspace repo checkout.
	ScopeRepo FileScope = "repo"
	// ScopeAgent roots operations at a named agent worktree.
	ScopeAgent FileScope = "agent"
)

// FileService defines business logic for file operations within agent worktrees.
// Handlers call this interface and map returned errors to HTTP responses.
//
// The *Scoped methods generalize listing/reading to an arbitrary scope root
// (currently the workspace folder) for the dedicated file browser; the
// agent-scoped methods remain for the per-agent file panel.
type FileService interface {
	// ListDirectory lists one level of a directory within an agent's worktree.
	ListDirectory(ctx context.Context, wsID, agentName, path string) (*FileTreeResult, error)

	// ReadFile reads a file within an agent's worktree. Binary files return metadata only.
	ReadFile(ctx context.Context, wsID, agentName, path string) (*FileReadResult, error)

	// WriteFile writes content to a file within an agent's worktree using atomic temp+rename.
	//
	// Deprecated: use WriteFileScoped with ScopeAgent.
	WriteFile(ctx context.Context, wsID, agentName, path, content string) error

	// ListDirectoryScoped lists one level of a directory under a scope root.
	// target names the scope's resource (repo/agent name); it must be empty for
	// ScopeWorkspace. repo optionally qualifies ScopeAgent to a specific
	// workspace repo checkout.
	ListDirectoryScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileTreeResult, error)

	// ReadFileScoped reads a file under a scope root. Binary files return
	// metadata only.
	ReadFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileReadResult, error)

	// ReadFileAtRevScoped reads a file from a git revision in the containing
	// checkout. Binary files return metadata only.
	ReadFileAtRevScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path, rev string) (*FileReadResult, error)

	// IndexFilesScoped returns root-relative file paths under a scope root for
	// quick-open navigation. Results are capped and report Truncated when clipped.
	IndexFilesScoped(ctx context.Context, wsID string, scope FileScope, target, repo string) (*FileIndexResult, error)

	// SearchFilesScoped searches text files under a scope root. The bounded walk
	// reports LimitHit when any search cap clips the scan or result set.
	SearchFilesScoped(ctx context.Context, wsID string, scope FileScope, target, repo string, req FileSearchRequest) (*FileSearchResult, error)

	// GitStatusScoped returns root-relative paths mapped to raw two-character
	// git status --porcelain XY codes for decoration-only file browser status.
	GitStatusScoped(ctx context.Context, wsID string, scope FileScope, target, repo string) (FileGitStatusResult, error)

	// ListFileCheckouts returns every known file-browser checkout and best-effort
	// local status metadata for each.
	ListFileCheckouts(ctx context.Context, wsID string) (*FileCheckoutsResult, error)

	// DiffFileScoped returns a unified diff for one file in the containing checkout.
	DiffFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path, from, to string) (*FileDiffResult, error)

	// BlameFileScoped returns parsed git blame line blocks or a bounded skip signal.
	BlameFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileBlameResult, error)

	// HistoryFileScoped returns commit and browser-save timeline entries for a file.
	HistoryFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileHistoryResult, error)

	// WriteFileScoped creates or updates a file under a scope root.
	WriteFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path, content string) error

	// DeletePathScoped deletes a file or directory under a scope root.
	DeletePathScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string, recursive bool) error

	// MkdirScoped creates a directory path under a scope root.
	MkdirScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) error

	// MovePathScoped renames or moves a path within one scope root.
	MovePathScoped(ctx context.Context, wsID string, scope FileScope, target, repo, from, to string, overwrite bool) error
}

// FileTreeEntry describes a directory entry in a file tree listing.
type FileTreeEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// FileTreeResult contains a directory listing result.
type FileTreeResult struct {
	Path    string          `json:"path"`
	Entries []FileTreeEntry `json:"entries"`
}

// FileReadResult contains file read content and metadata.
type FileReadResult struct {
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

// FileIndexResult is the response for scoped quick-open indexing.
type FileIndexResult struct {
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

// FileSearchRequest is the JSON body for a scoped global file search.
type FileSearchRequest struct {
	Query         string    `json:"query"`
	Repo          string    `json:"repo,omitempty"`
	Regex         bool      `json:"regex,omitempty"`
	Include       []string  `json:"include,omitempty"`
	Exclude       *[]string `json:"exclude,omitempty"`
	CaseSensitive bool      `json:"caseSensitive,omitempty"`
}

// FileSearchResult is the response for scoped global file search.
type FileSearchResult struct {
	Results  []FileSearchFileResult `json:"results"`
	LimitHit bool                   `json:"limitHit"`
}

// FileSearchFileResult groups text matches by root-relative file path.
type FileSearchFileResult struct {
	Path    string            `json:"path"`
	Matches []FileSearchMatch `json:"matches"`
}

// FileSearchMatch describes a single one-line text match.
type FileSearchMatch struct {
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Preview string `json:"preview"`
}

// FileGitStatusResult maps root-relative file paths to raw two-character git
// status --porcelain XY codes.
type FileGitStatusResult map[string]string

// FileCheckout describes a concrete repo or agent checkout known to the
// workspace file browser.
type FileCheckout struct {
	Kind        string `json:"kind"`
	Agent       string `json:"agent,omitempty"`
	Repo        string `json:"repo"`
	Exists      bool   `json:"exists"`
	Branch      string `json:"branch,omitempty"`
	ChangeCount int    `json:"change_count"`
	StatusError bool   `json:"status_error,omitempty"`
}

// FileCheckoutsResult is the response for checkout enumeration.
type FileCheckoutsResult struct {
	Checkouts []FileCheckout `json:"checkouts"`
}

// FileDiffResult contains a unified diff patch for one file.
type FileDiffResult struct {
	Path  string `json:"path"`
	Patch string `json:"patch"`
}

// FileBlameLine describes a contiguous line block from git blame --porcelain.
type FileBlameLine struct {
	Line    int    `json:"line"`
	Lines   int    `json:"lines"`
	SHA     string `json:"sha"`
	Author  string `json:"author"`
	Time    string `json:"time"`
	Summary string `json:"summary"`
}

// FileBlameResult contains parsed blame data or a bounded skip signal.
type FileBlameResult struct {
	Path    string          `json:"path"`
	Skipped bool            `json:"skipped"`
	Reason  string          `json:"reason,omitempty"`
	Message string          `json:"message,omitempty"`
	Lines   []FileBlameLine `json:"lines"`
}

// FileHistoryEntry is one Timeline item, kind-tagged as commit or save.
type FileHistoryEntry struct {
	Kind      string `json:"kind"`
	ID        string `json:"id,omitempty"`
	SHA       string `json:"sha,omitempty"`
	Author    string `json:"author,omitempty"`
	Time      string `json:"time"`
	Summary   string `json:"summary"`
	Content   string `json:"content,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// FileHistoryResult contains the merged file Timeline entries.
type FileHistoryResult struct {
	Path    string             `json:"path"`
	Entries []FileHistoryEntry `json:"entries"`
}

// FileMoveRequest is the JSON body for a scoped file move/rename operation.
type FileMoveRequest struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Repo      string `json:"repo,omitempty"`
	Overwrite bool   `json:"overwrite,omitempty"`
}
