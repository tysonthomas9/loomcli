package service

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// FileScope identifies the root a scoped file operation resolves against.
// Workspace, repo, and agent scopes are all wired through the scoped file API.
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

// FileService defines business logic for scoped workspace file operations.
// Handlers call this interface and map returned errors to HTTP responses.
type FileService interface {
	// ListDirectoryScoped lists one level of a directory under a scope root.
	// target names the scope's resource (repo/agent name); it must be empty for
	// ScopeWorkspace. repo optionally qualifies ScopeAgent to a specific
	// workspace repo checkout.
	ListDirectoryScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileTreeResult, error)

	// ReadFileScoped reads a file under a scope root. Binary files return
	// metadata only.
	ReadFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileReadResult, error)

	// StatPathScoped returns metadata and a strong version for a regular file or
	// bounded deterministic manifest version for a directory.
	StatPathScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileStatResult, error)

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

	// RepairCheckout repairs or provisions a known file-browser checkout. The
	// concrete ops layer owns git/worktree mechanics; service keeps HTTP-facing
	// validation and error categorization.
	RepairCheckout(ctx context.Context, wsID string, req FileCheckoutRepairRequest) (*ops.RepairResult, error)

	// DiffFileScoped returns a unified diff for one file in the containing checkout.
	DiffFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path, from, to string) (*FileDiffResult, error)

	// BlameFileScoped returns parsed git blame line blocks or a bounded skip signal.
	BlameFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileBlameResult, error)

	// HistoryFileScoped returns bounded commit history for a file.
	HistoryFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileHistoryResult, error)

	// WriteFileConditionalScoped applies optional HTTP-style preconditions. An
	// empty precondition preserves ordinary editor last-write-wins behavior.
	WriteFileConditionalScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path, content string, preconditions FileWritePreconditions) (*FileMutationResult, error)

	// DeletePathVersionedScoped requires the current source version.
	DeletePathVersionedScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string, recursive bool, version string) error

	// MkdirScoped creates a directory path under a scope root.
	MkdirScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) error

	// MovePathVersionedScoped requires the current source version and, when an
	// existing regular-file destination is overwritten, its current version.
	MovePathVersionedScoped(ctx context.Context, wsID string, scope FileScope, target, repo, from, to string, overwrite bool, sourceVersion, destinationVersion string) (*FileMutationResult, error)
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
	Version   string `json:"version"`
}

// FileStatResult contains mutation-relevant metadata and a strong version.
type FileStatResult struct {
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
	Version string `json:"version"`
}

// FileWritePreconditions represents optional conditional PUT semantics.
type FileWritePreconditions struct {
	IfMatch     string
	IfNoneMatch bool
}

// FileMutationResult reports the version produced by a successful write/move.
type FileMutationResult struct {
	Success bool   `json:"success"`
	Version string `json:"version"`
}

// FileIndexResult is the response for scoped quick-open indexing.
type FileIndexResult struct {
	Paths          []string            `json:"paths"`
	Truncated      bool                `json:"truncated"`
	PartialReasons []FilePartialReason `json:"partial_reasons"`
}

// FilePartialReason identifies the enforced bound that made an index or search
// response incomplete.
type FilePartialReason string

const (
	FilePartialFileCount   FilePartialReason = "file_count"
	FilePartialResultCount FilePartialReason = "result_count"
	FilePartialByteLimit   FilePartialReason = "byte_limit"
	FilePartialFileSize    FilePartialReason = "file_size"
	FilePartialDeadline    FilePartialReason = "deadline"
	FilePartialCanceled    FilePartialReason = "canceled"
)

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
	Results        []FileSearchFileResult `json:"results"`
	LimitHit       bool                   `json:"limitHit"`
	PartialReasons []FilePartialReason    `json:"partial_reasons"`
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

// FileGitStatusResult contains bounded status decoration data. Workspace scope
// may be partial when one checkout fails or a bound is reached.
type FileGitStatusResult struct {
	Status   map[string]string   `json:"status"`
	Partial  bool                `json:"partial"`
	LimitHit bool                `json:"limit_hit"`
	Errors   []FileCheckoutError `json:"errors"`
}

// FileCheckoutError reports one unavailable checkout without hiding healthy
// status data from the rest of a workspace fan-out.
type FileCheckoutError struct {
	Kind  string `json:"kind"`
	Agent string `json:"agent,omitempty"`
	Repo  string `json:"repo"`
	Error string `json:"error"`
}

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
	Error       string `json:"error,omitempty"`
	Partial     bool   `json:"partial,omitempty"`
	LimitHit    bool   `json:"limit_hit,omitempty"`
}

// FileCheckoutsResult is the response for checkout enumeration.
type FileCheckoutsResult struct {
	Checkouts []FileCheckout      `json:"checkouts"`
	Partial   bool                `json:"partial"`
	LimitHit  bool                `json:"limit_hit"`
	Errors    []FileCheckoutError `json:"errors"`
}

// FileCheckoutRepairRequest is the JSON body for checkout repair.
type FileCheckoutRepairRequest struct {
	Scope  string `json:"scope"`
	Target string `json:"target"`
	Repo   string `json:"repo,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

// FileDiffResult contains a unified diff patch for one file.
type FileDiffResult struct {
	Path     string `json:"path"`
	Patch    string `json:"patch"`
	Partial  bool   `json:"partial"`
	LimitHit bool   `json:"limit_hit"`
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
	Path     string          `json:"path"`
	Skipped  bool            `json:"skipped"`
	Reason   string          `json:"reason,omitempty"`
	Message  string          `json:"message,omitempty"`
	Lines    []FileBlameLine `json:"lines"`
	Partial  bool            `json:"partial"`
	LimitHit bool            `json:"limit_hit"`
}

// FileHistoryEntry is one Git commit that changed a file.
type FileHistoryEntry struct {
	Kind    string `json:"kind"`
	SHA     string `json:"sha"`
	Author  string `json:"author"`
	Time    string `json:"time"`
	Summary string `json:"summary"`
}

// FileHistoryResult contains bounded commit history.
type FileHistoryResult struct {
	Path     string             `json:"path"`
	Entries  []FileHistoryEntry `json:"entries"`
	Partial  bool               `json:"partial"`
	LimitHit bool               `json:"limit_hit"`
}

// FileMoveRequest is the JSON body for a scoped file move/rename operation.
type FileMoveRequest struct {
	From               string `json:"from"`
	To                 string `json:"to"`
	Repo               string `json:"repo,omitempty"`
	Overwrite          bool   `json:"overwrite,omitempty"`
	SourceVersion      string `json:"source_version"`
	DestinationVersion string `json:"destination_version,omitempty"`
}
