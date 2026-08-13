package sourcecontrol

import (
	"context"
	"errors"
	"io/fs"
)

var (
	ErrAgentRepoNotAllowed      = errors.New("source control: agent repository not allowed")
	ErrAgentWorktreeNotFound    = errors.New("source control: agent worktree not found")
	ErrCheckoutTargetNotAllowed = errors.New("source control: checkout target not allowed")
)

type gitWorktreeIdentityContextKey struct{}

type GitWorktreeIdentity struct {
	Path string
	Info fs.FileInfo
}

func WithGitWorktreeIdentity(ctx context.Context, path string, info fs.FileInfo) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, gitWorktreeIdentityContextKey{}, GitWorktreeIdentity{Path: path, Info: info})
}

func GitWorktreeIdentityFromContext(ctx context.Context) (GitWorktreeIdentity, bool) {
	if ctx == nil {
		return GitWorktreeIdentity{}, false
	}
	identity, ok := ctx.Value(gitWorktreeIdentityContextKey{}).(GitWorktreeIdentity)
	return identity, ok && identity.Info != nil
}

// workspaceFileAdapter is Source Control's private composition seam for
// machine-local file execution. The module root validates the opaque access
// grant before delegating and passes only the resulting sensitive-content
// decision. Product callers receive Browse, Mutate, or Checkout instead.
type workspaceFileAdapter interface {
	ListDirectoryAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileTreeResult, error)
	ReadFileAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileReadResult, error)
	StatPathAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileStatResult, error)
	ReadFileAtRevisionAuthorized(context.Context, string, FileScope, string, string, string, string, bool) (*FileReadResult, error)
	IndexFilesAuthorized(context.Context, string, FileScope, string, string, bool) (*FileIndexResult, error)
	SearchFilesAuthorized(context.Context, string, FileScope, string, string, FileSearchRequest, bool) (*FileSearchResult, error)
	DiffPathAuthorized(context.Context, string, FileScope, string, string, string, string, string, bool) (*FileDiffResult, error)
	BlamePathAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileBlameResult, error)
	PathHistoryAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileHistoryResult, error)
	WriteFileAuthorized(context.Context, string, FileScope, string, string, string, string, FileWritePreconditions, bool) (*FileMutationResult, error)
	DeletePathAuthorized(context.Context, string, FileScope, string, string, string, bool, string, bool) error
	CreateDirectoryAuthorized(context.Context, string, FileScope, string, string, string, bool) error
	MovePathAuthorized(context.Context, string, FileScope, string, string, string, string, bool, string, string, bool) (*FileMutationResult, error)
	StatusAuthorized(context.Context, string, FileScope, string, string, bool) (FileGitStatusResult, error)
	ListCheckoutsAuthorized(context.Context, string, bool) (*FileCheckoutsResult, error)
	RepairCheckoutAuthorized(context.Context, string, FileCheckoutRepairRequest) (*RepairResult, error)
}

type Worktree struct {
	Name          string
	Path          string
	Branch        string
	DefaultBranch string
	Remote        string
	RepoName      string
	IsWorkspace   bool
}

type WorkspaceTopology struct {
	ID     string
	Name   string
	Path   string
	Repos  []WorkspaceRepo
	Agents []WorkspaceAgent
	Groups []string
}

type WorkspaceRepo struct {
	Name          string
	Path          string
	DefaultBranch string
	Remote        string
	RemoteURL     string
	SourceRepoID  string
	Groups        []string
}

type WorkspaceAgent struct {
	Name       string
	Role       string
	Worktree   string
	Branch     string
	Repos      []string
	RepoGroups []string
}

type GitFileStatusResult struct {
	Entries  map[string]string
	Partial  bool
	LimitHit bool
}

type GitBoundedTextResult struct {
	Output   string
	Partial  bool
	LimitHit bool
}

type GitFileContentAtRev struct {
	Content   []byte
	Size      int64
	Truncated bool
}

type RepairResult struct {
	Repaired      bool
	Method        string
	RequiresForce bool
	BackupPath    string
	Message       string
}
