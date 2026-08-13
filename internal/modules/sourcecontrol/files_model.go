package sourcecontrol

import (
	"context"
	"time"
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

// Mutate is Source Control's working-tree mutation port. Source Control owns
// optimistic versions, path policy, cache invalidation, and mutation locking.
type Mutate interface {
	WriteFile(context.Context, WriteCommand) (*FileMutationResult, error)
	DeletePath(context.Context, DeleteCommand) error
	CreateDirectory(context.Context, CreateDirectoryCommand) error
	MovePath(context.Context, MoveCommand) (*FileMutationResult, error)
}

// Checkout is Source Control's checkout lifecycle and working-state port.
// It owns branch working state, checkout repair, and forge publication policy;
// stack lifecycle is exposed through the same Source Control capability.
type Checkout interface {
	Status(context.Context, LocationQuery) (FileGitStatusResult, error)
	ListCheckouts(context.Context, WorkspaceQuery) (*FileCheckoutsResult, error)
	Repair(context.Context, RepairCommand) (*RepairResult, error)
	Push(context.Context, PushCommand) (*PushResult, error)
	PushAll(context.Context, PushAllCommand) (*PushAllResult, error)
	Pull(context.Context, PullCommand) (*PullResult, error)
	Sync(context.Context, SyncCommand) (*SyncResult, error)
	CreatePullRequest(context.Context, CreatePullRequestCommand) (*PullRequestCreation, error)
	ListPullRequests(context.Context, ListPullRequestsQuery) (*PullRequestList, error)
	Reset(context.Context, ResetCommand) (*ResetResult, error)
	AgentStatus(context.Context, AgentStatusQuery) (*AgentStatusResult, error)
	SetTargetBranch(context.Context, SetTargetBranchCommand) error
}

// FileLocation identifies a Workspace-approved product scope. It never carries
// a filesystem path or remote URL.
type FileLocation struct {
	WorkspaceKey string
	Scope        FileScope
	Target       string
	Repository   string
}

type WorkspaceQuery struct {
	Grant        AccessGrant
	WorkspaceKey string
}

type LocationQuery struct {
	Grant    AccessGrant
	Location FileLocation
}

type PathQuery struct {
	Grant    AccessGrant
	Location FileLocation
	Path     string
}

type RevisionQuery struct {
	Grant    AccessGrant
	Location FileLocation
	Path     string
	Revision string
}

type SearchQuery struct {
	Grant    AccessGrant
	Location FileLocation
	Search   FileSearchRequest
}

type PathDiffQuery struct {
	Grant    AccessGrant
	Location FileLocation
	Path     string
	From     string
	To       string
}

type WriteCommand struct {
	Grant           AccessGrant
	Location        FileLocation
	Path            string
	Content         string
	ExpectedVersion string
	CreateOnly      bool
}

type DeleteCommand struct {
	Grant           AccessGrant
	Location        FileLocation
	Path            string
	Recursive       bool
	ExpectedVersion string
}

type CreateDirectoryCommand struct {
	Grant    AccessGrant
	Location FileLocation
	Path     string
}

type MoveCommand struct {
	Grant                      AccessGrant
	Location                   FileLocation
	From                       string
	To                         string
	Overwrite                  bool
	ExpectedSourceVersion      string
	ExpectedDestinationVersion string
}

type RepairCommand struct {
	Grant    AccessGrant
	Location FileLocation
	Force    bool
}

// FileTreeEntry describes a directory entry in a file tree listing.
type FileTreeEntry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// FileTreeResult contains a directory listing result.
type FileTreeResult struct {
	Path    string
	Entries []FileTreeEntry
}

// FileReadResult contains file read content and metadata.
type FileReadResult struct {
	Path      string
	Content   string
	Size      int64
	Binary    bool
	Truncated bool
	Version   string
}

// FileStatResult contains mutation-relevant metadata and a strong version.
type FileStatResult struct {
	Path    string
	IsDir   bool
	Size    int64
	ModTime time.Time
	Version string
}

// FileWritePreconditions represents optional conditional PUT semantics.
type FileWritePreconditions struct {
	IfMatch     string
	IfNoneMatch bool
}

// FileMutationResult reports the version produced by a successful write/move.
type FileMutationResult struct {
	Success bool
	Version string
}

// FileIndexResult is the response for scoped quick-open indexing.
type FileIndexResult struct {
	Paths          []string
	Truncated      bool
	PartialReasons []FilePartialReason
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
	Query         string
	Repo          string
	Regex         bool
	Include       []string
	Exclude       *[]string
	CaseSensitive bool
}

// FileSearchResult is the response for scoped global file search.
type FileSearchResult struct {
	Results        []FileSearchFileResult
	LimitHit       bool
	PartialReasons []FilePartialReason
}

// FileSearchFileResult groups text matches by root-relative file path.
type FileSearchFileResult struct {
	Path    string
	Matches []FileSearchMatch
}

// FileSearchMatch describes a single one-line text match.
type FileSearchMatch struct {
	Line    int
	Col     int
	Preview string
}

// FileGitStatusResult contains bounded status decoration data. Workspace scope
// may be partial when one checkout fails or a bound is reached.
type FileGitStatusResult struct {
	Status   map[string]string
	Partial  bool
	LimitHit bool
	Errors   []FileCheckoutError
}

// FileCheckoutError reports one unavailable checkout without hiding healthy
// status data from the rest of a workspace fan-out.
type FileCheckoutError struct {
	Kind  string
	Agent string
	Repo  string
	Error string
}

// FileCheckout describes a concrete repo or agent checkout known to the
// workspace file browser.
type FileCheckout struct {
	Kind        string
	Agent       string
	Repo        string
	Exists      bool
	Branch      string
	ChangeCount int
	StatusError bool
	Error       string
	Partial     bool
	LimitHit    bool
}

// FileCheckoutsResult is the response for checkout enumeration.
type FileCheckoutsResult struct {
	Checkouts []FileCheckout
	Partial   bool
	LimitHit  bool
	Errors    []FileCheckoutError
}

// FileCheckoutRepairRequest is the JSON body for checkout repair.
type FileCheckoutRepairRequest struct {
	Scope  string
	Target string
	Repo   string
	Force  bool
}

// FileDiffResult contains a unified diff patch for one file.
type FileDiffResult struct {
	Path     string
	Patch    string
	Partial  bool
	LimitHit bool
}

// FileBlameLine describes a contiguous line block from git blame --porcelain.
type FileBlameLine struct {
	Line    int
	Lines   int
	SHA     string
	Author  string
	Time    string
	Summary string
}

// FileBlameResult contains parsed blame data or a bounded skip signal.
type FileBlameResult struct {
	Path     string
	Skipped  bool
	Reason   string
	Message  string
	Lines    []FileBlameLine
	Partial  bool
	LimitHit bool
}

// FileHistoryEntry is one Git commit that changed a file.
type FileHistoryEntry struct {
	Kind    string
	SHA     string
	Author  string
	Time    string
	Summary string
}

// FileHistoryResult contains bounded commit history.
type FileHistoryResult struct {
	Path     string
	Entries  []FileHistoryEntry
	Partial  bool
	LimitHit bool
}
