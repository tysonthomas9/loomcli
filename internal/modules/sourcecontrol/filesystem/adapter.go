package filesystem

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// Model aliases keep the private adapter on Source Control's canonical
// contracts without introducing parallel filesystem DTOs.
type (
	FileScope                 = sourcecontrol.FileScope
	FileTreeEntry             = sourcecontrol.FileTreeEntry
	FileTreeResult            = sourcecontrol.FileTreeResult
	FileReadResult            = sourcecontrol.FileReadResult
	FileStatResult            = sourcecontrol.FileStatResult
	FileWritePreconditions    = sourcecontrol.FileWritePreconditions
	FileMutationResult        = sourcecontrol.FileMutationResult
	FileIndexResult           = sourcecontrol.FileIndexResult
	FilePartialReason         = sourcecontrol.FilePartialReason
	FileSearchRequest         = sourcecontrol.FileSearchRequest
	FileSearchResult          = sourcecontrol.FileSearchResult
	FileSearchFileResult      = sourcecontrol.FileSearchFileResult
	FileSearchMatch           = sourcecontrol.FileSearchMatch
	FileGitStatusResult       = sourcecontrol.FileGitStatusResult
	FileCheckoutError         = sourcecontrol.FileCheckoutError
	FileCheckout              = sourcecontrol.FileCheckout
	FileCheckoutsResult       = sourcecontrol.FileCheckoutsResult
	FileCheckoutRepairRequest = sourcecontrol.FileCheckoutRepairRequest
	FileDiffResult            = sourcecontrol.FileDiffResult
	FileBlameLine             = sourcecontrol.FileBlameLine
	FileBlameResult           = sourcecontrol.FileBlameResult
	FileHistoryEntry          = sourcecontrol.FileHistoryEntry
	FileHistoryResult         = sourcecontrol.FileHistoryResult
	Worktree                  = sourcecontrol.Worktree
	WorkspaceTopology         = sourcecontrol.WorkspaceTopology
	WorkspaceRepo             = sourcecontrol.WorkspaceRepo
	WorkspaceAgent            = sourcecontrol.WorkspaceAgent
	GitFileStatusResult       = sourcecontrol.GitFileStatusResult
	GitBoundedTextResult      = sourcecontrol.GitBoundedTextResult
	GitFileContentAtRev       = sourcecontrol.GitFileContentAtRev
	RepairResult              = sourcecontrol.RepairResult
	Failure                   = sourcecontrol.Failure
	FailureKind               = sourcecontrol.FailureKind
	failureKind               = sourcecontrol.FailureKind
)

const (
	ScopeWorkspace = sourcecontrol.ScopeWorkspace
	ScopeRepo      = sourcecontrol.ScopeRepo
	ScopeAgent     = sourcecontrol.ScopeAgent

	FilePartialFileCount   = sourcecontrol.FilePartialFileCount
	FilePartialResultCount = sourcecontrol.FilePartialResultCount
	FilePartialByteLimit   = sourcecontrol.FilePartialByteLimit
	FilePartialFileSize    = sourcecontrol.FilePartialFileSize
	FilePartialDeadline    = sourcecontrol.FilePartialDeadline
	FilePartialCanceled    = sourcecontrol.FilePartialCanceled

	failureInvalid              = sourcecontrol.FailureInvalid
	failureNotFound             = sourcecontrol.FailureNotFound
	failureForbidden            = sourcecontrol.FailureForbidden
	failureConflict             = sourcecontrol.FailureConflict
	failureUnavailable          = sourcecontrol.FailureUnavailable
	failureInternal             = sourcecontrol.FailureInternal
	failurePayloadTooLarge      = sourcecontrol.FailurePayloadTooLarge
	failurePreconditionFailed   = sourcecontrol.FailurePreconditionFailed
	failurePreconditionRequired = sourcecontrol.FailurePreconditionRequired
	failureTimeout              = sourcecontrol.FailureTimeout
)

var (
	ErrAgentRepoNotAllowed      = sourcecontrol.ErrAgentRepoNotAllowed
	ErrAgentWorktreeNotFound    = sourcecontrol.ErrAgentWorktreeNotFound
	ErrCheckoutTargetNotAllowed = sourcecontrol.ErrCheckoutTargetNotAllowed
	ErrInvalid                  = sourcecontrol.ErrInvalid
	ErrNotFound                 = sourcecontrol.ErrNotFound
	ErrForbidden                = sourcecontrol.ErrForbidden
	ErrUnavailable              = sourcecontrol.ErrUnavailable
	ErrPayloadTooLarge          = sourcecontrol.ErrPayloadTooLarge
	ErrPreconditionFailed       = sourcecontrol.ErrPreconditionFailed
	ErrPreconditionRequired     = sourcecontrol.ErrPreconditionRequired
	ErrTimeout                  = sourcecontrol.ErrTimeout
)

var (
	IsBinaryContent                = sourcecontrol.IsBinaryContent
	IsSensitiveFilePath            = sourcecontrol.IsSensitiveFilePath
	WithGitWorktreeIdentity        = sourcecontrol.WithGitWorktreeIdentity
	GitWorktreeIdentityFromContext = sourcecontrol.GitWorktreeIdentityFromContext
)

type fileAccess struct {
	sensitive bool
}

// mechanics is the adapter-local machine seam. Keeping it private prevents
// filesystem execution authority from becoming another Source Control port.
type mechanics interface {
	ResolveAgentWorktree(workspaceID, name string) (*Worktree, error)
	ResolveAgentWorktreeForRepo(workspaceID, name, repo string) (*Worktree, error)
	ResolveWorkspaceRoot(workspaceID string) (string, error)
	ResolveWorkspaceData(workspaceID string) (*WorkspaceTopology, error)
	ResolveLoomDataDir() (string, error)
	GitStatusPorcelain(context.Context, string) (GitFileStatusResult, error)
	GitShowFileAtRev(context.Context, string, string, string, int64) (*GitFileContentAtRev, error)
	GitDiffFile(context.Context, string, string, string, string) (GitBoundedTextResult, error)
	GitLogFile(context.Context, string, string, int) (GitBoundedTextResult, error)
	GitBlamePorcelain(context.Context, string, string) (GitBoundedTextResult, error)
	GitCurrentBranch(context.Context, string) (string, error)
	RepairCheckout(string, string, string, string, bool) (RepairResult, error)
}

func fileAccessFromGrant(grant sourcecontrol.AccessGrant) fileAccess {
	return fileAccess{sensitive: grant.Capabilities().Sensitive}
}

func filePathAllowsSensitive(access fileAccess, path string) bool {
	return !IsSensitiveFilePath(path) || access.sensitive
}

func newFailure(kind FailureKind, message string, cause error) *Failure {
	return &Failure{Kind: kind, Message: message, Cause: cause}
}

func newInvalid(message string) *Failure {
	return newFailure(failureInvalid, message, nil)
}

func newNotFound(message string) *Failure {
	return newFailure(failureNotFound, message, nil)
}

func newForbidden(message string) *Failure {
	return newFailure(failureForbidden, message, nil)
}

func newConflict(message string) *Failure {
	return newFailure(failureConflict, message, nil)
}

func newInternal(message string, cause error) *Failure {
	return newFailure(failureInternal, message, cause)
}

func newPayloadTooLarge(message string) *Failure {
	return newFailure(failurePayloadTooLarge, message, nil)
}

func newPreconditionFailed(message string) *Failure {
	return newFailure(failurePreconditionFailed, message, nil)
}

func newPreconditionRequired(message string) *Failure {
	return newFailure(failurePreconditionRequired, message, nil)
}

func newTimeout(message string) *Failure {
	return newFailure(failureTimeout, message, nil)
}

// Adapter executes Source Control's bounded machine-local file operations.
// Only the serve composition root constructs it.
type Adapter struct {
	*fileServiceImpl
}

// New constructs Source Control's private filesystem adapter.
func New(machine mechanics) *Adapter {
	if machine == nil {
		return nil
	}
	return &Adapter{fileServiceImpl: newFileService(machine)}
}

func (s *fileServiceImpl) ListDirectoryAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path string, sensitive bool) (*FileTreeResult, error) {
	return s.listDirectoryScoped(ctx, workspace, scope, target, repo, path, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) ReadFileAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path string, sensitive bool) (*FileReadResult, error) {
	return s.readFileScoped(ctx, workspace, scope, target, repo, path, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) StatPathAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path string, sensitive bool) (*FileStatResult, error) {
	return s.statPathScoped(ctx, workspace, scope, target, repo, path, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) ReadFileAtRevisionAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path, revision string, sensitive bool) (*FileReadResult, error) {
	return s.readFileAtRevScoped(ctx, workspace, scope, target, repo, path, revision, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) IndexFilesAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo string, sensitive bool) (*FileIndexResult, error) {
	return s.indexFilesScoped(ctx, workspace, scope, target, repo, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) SearchFilesAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo string, request FileSearchRequest, sensitive bool) (*FileSearchResult, error) {
	return s.searchFilesScoped(ctx, workspace, scope, target, repo, request, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) DiffPathAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path, from, to string, sensitive bool) (*FileDiffResult, error) {
	return s.diffFileScoped(ctx, workspace, scope, target, repo, path, from, to, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) BlamePathAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path string, sensitive bool) (*FileBlameResult, error) {
	return s.blameFileScoped(ctx, workspace, scope, target, repo, path, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) PathHistoryAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path string, sensitive bool) (*FileHistoryResult, error) {
	return s.historyFileScoped(ctx, workspace, scope, target, repo, path, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) WriteFileAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path, content string, conditions FileWritePreconditions, sensitive bool) (*FileMutationResult, error) {
	return s.writeFileConditionalScoped(ctx, workspace, scope, target, repo, path, content, conditions, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) DeletePathAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path string, recursive bool, version string, sensitive bool) error {
	return s.deletePathVersionedScoped(ctx, workspace, scope, target, repo, path, recursive, version, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) CreateDirectoryAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, path string, sensitive bool) error {
	return s.mkdirScoped(ctx, workspace, scope, target, repo, path, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) MovePathAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo, from, to string, overwrite bool, sourceVersion, destinationVersion string, sensitive bool) (*FileMutationResult, error) {
	return s.movePathVersionedScoped(ctx, workspace, scope, target, repo, from, to, overwrite, sourceVersion, destinationVersion, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) StatusAuthorized(ctx context.Context, workspace string, scope FileScope, target, repo string, sensitive bool) (FileGitStatusResult, error) {
	return s.gitStatusScoped(ctx, workspace, scope, target, repo, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) ListCheckoutsAuthorized(ctx context.Context, workspace string, sensitive bool) (*FileCheckoutsResult, error) {
	return s.listFileCheckouts(ctx, workspace, fileAccess{sensitive: sensitive})
}

func (s *fileServiceImpl) RepairCheckoutAuthorized(ctx context.Context, workspace string, request FileCheckoutRepairRequest) (*RepairResult, error) {
	return s.RepairCheckout(ctx, workspace, request)
}
