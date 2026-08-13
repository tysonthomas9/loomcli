package misc

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/filesystem"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// logger is a package-level variable used by test code to capture log output.
// Mirrors the root webui.logger variable.
var logger = slog.Default()

// module is a local interface matching webui.Module for compile-time assertions.
type module interface {
	Register(mux *http.ServeMux)
}

// FileModule → Module (struct alias)
type FileModule = Module

type testFilePorts interface {
	sourcecontrol.Browse
	sourcecontrol.Mutate
	sourcecontrol.Checkout
}

// NewFileModule keeps old handler tests concise while passing the three real
// Source Control ports independently to production composition.
func NewFileModule(ports testFilePorts, accessCfg ...middleware.FileAccessConfig) *Module {
	return NewModule(ports, ports, ports, accessCfg...)
}

// maxRequestBody is the maximum request body size (1MB).
const maxRequestBody = 1 << 20

// ---------------------------------------------------------------------------
// Handler function aliases (handleXxx → HandleXxx)
// ---------------------------------------------------------------------------

// handleAuthConfig is a 2-arg shim for tests written before the third
// workItemsFn parameter was added — they don't exercise that path and
// default to nil so the env-var fallback covers them.
func handleAuthConfig(extAuthURL string, limiter *AuthConfigLimiter) http.HandlerFunc {
	return HandleAuthConfig(extAuthURL, limiter, nil)
}

var handleClientErrors = HandleClientErrors
var handleGetAgentLog = HandleGetAgentLog
var handleGetBackendsHealth = HandleGetBackendsHealth
var handleListEditors = HandleListEditors
var handleOpenEditor = HandleOpenEditor
var handleGetTaskLog = HandleGetTaskLog
var handleListTaskPhases = HandleListTaskPhases
var handleGetSession = HandleGetSession
var handleGetSessionDiff = HandleGetSessionDiff
var handleGetSessionTranscript = HandleGetSessionTranscript
var handleListTaskSessions = HandleListTaskSessions
var handleWorkerRegister = HandleWorkerRegister

// ---------------------------------------------------------------------------
// Limiter constructor aliases (newXxxLimiter → NewXxxLimiter)
// ---------------------------------------------------------------------------

var newAuthConfigLimiter = NewAuthConfigLimiter
var newClientErrorLimiter = NewClientErrorLimiter

// clientErrorLimiter → ClientErrorLimiter (type alias)
type clientErrorLimiter = ClientErrorLimiter

// AgentLogResult → agentcoord.AgentLogResult
type AgentLogResult = agentcoord.AgentLogResult

// FileReadResult is the canonical generated HTTP response used by handler tests.
type FileReadResult = loomapi.FileReadResponse

// FileTreeResult is the canonical generated HTTP response used by handler tests.
type FileTreeResult = loomapi.FileTreeResponse

// FileTreeEntry is the canonical generated HTTP response entry used by tests.
type FileTreeEntry = loomapi.FileTreeEntry

// NewFileService wires handler tests through the production Source Control
// owner. Keeping the tests on the real owner prevents a second file-policy
// implementation from drifting inside the delivery package.
type testWorkspaceLayout struct{}

func (testWorkspaceLayout) ResolveAgentCheckout(context.Context, string, string) (sourcecontrol.AgentCheckout, error) {
	return sourcecontrol.AgentCheckout{}, sourcecontrol.ErrNotFound
}
func (testWorkspaceLayout) ListAgentCheckouts(context.Context, string) ([]sourcecontrol.AgentCheckout, error) {
	return nil, nil
}
func (testWorkspaceLayout) ListRepositoryCheckouts(context.Context, string) ([]sourcecontrol.RepositoryCheckoutView, error) {
	return nil, nil
}
func (testWorkspaceLayout) SetRepositoryDefaultBranch(context.Context, string, string, string) error {
	return nil
}

type testGitBrowseMechanics struct{}

func (testGitBrowseMechanics) DiffStat(context.Context, string, string) (sourcecontrol.DiffStat, error) {
	return sourcecontrol.DiffStat{}, nil
}
func (testGitBrowseMechanics) ResolveMergeBase(context.Context, string, string) (string, error) {
	return "main", nil
}
func (testGitBrowseMechanics) DiffCommits(context.Context, string, string, int) ([]sourcecontrol.DiffCommit, error) {
	return nil, nil
}
func (testGitBrowseMechanics) DiffFiles(context.Context, string, string, string) ([]sourcecontrol.DiffFile, error) {
	return nil, nil
}
func (testGitBrowseMechanics) DiffFilePatch(context.Context, string, string, string, string) (*sourcecontrol.DiffFilePatch, error) {
	return &sourcecontrol.DiffFilePatch{}, nil
}
func (testGitBrowseMechanics) Push(context.Context, string, string, string, string) (*sourcecontrol.PushResult, error) {
	return &sourcecontrol.PushResult{}, nil
}
func (testGitBrowseMechanics) Pull(context.Context, string, string, string, string) (*sourcecontrol.PullResult, error) {
	return &sourcecontrol.PullResult{}, nil
}
func (testGitBrowseMechanics) CurrentBranch(context.Context, string) (string, error) {
	return "main", nil
}
func (testGitBrowseMechanics) Reset(context.Context, string, string, string, bool, bool) (*sourcecontrol.ResetResult, error) {
	return &sourcecontrol.ResetResult{}, nil
}
func (testGitBrowseMechanics) Status(context.Context, string, string) (*sourcecontrol.AgentStatusResult, error) {
	return &sourcecontrol.AgentStatusResult{}, nil
}
func (testGitBrowseMechanics) Available(context.Context) error { return nil }
func (testGitBrowseMechanics) CreatePullRequest(context.Context, string, string, string, string) (*sourcecontrol.PullRequestCreation, error) {
	return &sourcecontrol.PullRequestCreation{}, nil
}
func (testGitBrowseMechanics) ListPullRequests(context.Context, string, string, int) ([]sourcecontrol.PullRequest, error) {
	return nil, nil
}

type composedTestFilePorts struct {
	sourcecontrol.Browse
	sourcecontrol.Mutate
	sourcecontrol.Checkout
}

var testFileGrantIssuer = sourcecontrol.NewAccessGrantIssuer()

type testFileMechanics interface {
	ResolveAgentWorktree(string, string) (*sourcecontrol.Worktree, error)
	ResolveAgentWorktreeForRepo(string, string, string) (*sourcecontrol.Worktree, error)
	ResolveWorkspaceRoot(string) (string, error)
	ResolveWorkspaceData(string) (*sourcecontrol.WorkspaceTopology, error)
	ResolveLoomDataDir() (string, error)
	GitStatusPorcelain(context.Context, string) (sourcecontrol.GitFileStatusResult, error)
	GitShowFileAtRev(context.Context, string, string, string, int64) (*sourcecontrol.GitFileContentAtRev, error)
	GitDiffFile(context.Context, string, string, string, string) (sourcecontrol.GitBoundedTextResult, error)
	GitLogFile(context.Context, string, string, int) (sourcecontrol.GitBoundedTextResult, error)
	GitBlamePorcelain(context.Context, string, string) (sourcecontrol.GitBoundedTextResult, error)
	GitCurrentBranch(context.Context, string) (string, error)
	RepairCheckout(string, string, string, string, bool) (sourcecontrol.RepairResult, error)
}

func NewFileService(mechanics testFileMechanics) *composedTestFilePorts {
	adapters := testGitBrowseMechanics{}
	ports, err := sourcecontrol.NewWorkspacePorts(testFileGrantIssuer, testWorkspaceLayout{}, adapters, filesystem.New(mechanics), adapters, adapters)
	if err != nil {
		panic(err)
	}
	return &composedTestFilePorts{Browse: ports.Browse, Mutate: ports.Mutate, Checkout: ports.Checkout}
}

// ReadLastNLines wraps the package-internal readLastNLinesFromFile without
// secure-directory restrictions — suitable for unit tests only.
func ReadLastNLines(filepath string, n int) ([]string, int64, error) {
	return readLastNLinesFromFile(filepath, n, nil, 0)
}

// mockAgentService implements agentcoord.AgentService with no-op defaults.
type mockAgentService struct {
	getTerminalInfoFunc       func(ctx context.Context, wsID, agentName string) (*agentcoord.AgentTerminalInfoResult, error)
	generateTerminalTokenFunc func(ctx context.Context, wsID, agentName, userID string) (string, error)
	getLogFunc                func(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*agentcoord.AgentLogResult, error)
}

func (m *mockAgentService) GetTerminalInfo(ctx context.Context, wsID, agentName string) (*agentcoord.AgentTerminalInfoResult, error) {
	if m.getTerminalInfoFunc != nil {
		return m.getTerminalInfoFunc(ctx, wsID, agentName)
	}
	return &agentcoord.AgentTerminalInfoResult{Agent: agentName, Mode: "archive"}, nil
}
func (m *mockAgentService) GenerateTerminalToken(ctx context.Context, wsID, agentName, userID string) (string, error) {
	if m.generateTerminalTokenFunc != nil {
		return m.generateTerminalTokenFunc(ctx, wsID, agentName, userID)
	}
	return "test-token", nil
}
func (m *mockAgentService) GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*agentcoord.AgentLogResult, error) {
	if m.getLogFunc != nil {
		return m.getLogFunc(ctx, wsID, agentName, lines, beforeLine)
	}
	return &agentcoord.AgentLogResult{Lines: []string{}, LineCount: 0, StartLine: 1}, nil
}

// ---------------------------------------------------------------------------
// Stub types for module tests
// ---------------------------------------------------------------------------

// stubFileService implements the three Source Control workspace ports with
// no-op defaults for module tests.
type stubFileService struct{}

func (s *stubFileService) DiffStat(context.Context, sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error) {
	return sourcecontrol.AgentDiffStat{}, nil
}
func (s *stubFileService) DiffCommits(context.Context, sourcecontrol.DiffCommitsQuery) ([]sourcecontrol.DiffCommit, error) {
	return nil, nil
}
func (s *stubFileService) DiffFiles(context.Context, sourcecontrol.DiffFilesQuery) ([]sourcecontrol.DiffFile, error) {
	return nil, nil
}
func (s *stubFileService) DiffFilePatch(context.Context, sourcecontrol.DiffFilePatchQuery) (*sourcecontrol.DiffFilePatch, error) {
	return &sourcecontrol.DiffFilePatch{}, nil
}

func (s *stubFileService) ListDirectory(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileTreeResult, error) {
	return &sourcecontrol.FileTreeResult{}, nil
}
func (s *stubFileService) ReadFile(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileReadResult, error) {
	return &sourcecontrol.FileReadResult{}, nil
}
func (s *stubFileService) StatPath(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileStatResult, error) {
	return &sourcecontrol.FileStatResult{}, nil
}
func (s *stubFileService) ReadFileAtRevision(_ context.Context, _ sourcecontrol.RevisionQuery) (*sourcecontrol.FileReadResult, error) {
	return &sourcecontrol.FileReadResult{}, nil
}
func (s *stubFileService) IndexFiles(_ context.Context, _ sourcecontrol.LocationQuery) (*sourcecontrol.FileIndexResult, error) {
	return &sourcecontrol.FileIndexResult{}, nil
}
func (s *stubFileService) SearchFiles(_ context.Context, _ sourcecontrol.SearchQuery) (*sourcecontrol.FileSearchResult, error) {
	return &sourcecontrol.FileSearchResult{}, nil
}
func (s *stubFileService) Status(_ context.Context, _ sourcecontrol.LocationQuery) (sourcecontrol.FileGitStatusResult, error) {
	return sourcecontrol.FileGitStatusResult{}, nil
}
func (s *stubFileService) ListCheckouts(_ context.Context, _ sourcecontrol.WorkspaceQuery) (*sourcecontrol.FileCheckoutsResult, error) {
	return &sourcecontrol.FileCheckoutsResult{}, nil
}
func (s *stubFileService) Repair(_ context.Context, _ sourcecontrol.RepairCommand) (*sourcecontrol.RepairResult, error) {
	return &sourcecontrol.RepairResult{}, nil
}
func (s *stubFileService) DiffPath(_ context.Context, _ sourcecontrol.PathDiffQuery) (*sourcecontrol.FileDiffResult, error) {
	return &sourcecontrol.FileDiffResult{}, nil
}
func (s *stubFileService) BlamePath(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileBlameResult, error) {
	return &sourcecontrol.FileBlameResult{}, nil
}
func (s *stubFileService) PathHistory(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileHistoryResult, error) {
	return &sourcecontrol.FileHistoryResult{}, nil
}
func (s *stubFileService) WriteFile(_ context.Context, _ sourcecontrol.WriteCommand) (*sourcecontrol.FileMutationResult, error) {
	return &sourcecontrol.FileMutationResult{Success: true}, nil
}
func (s *stubFileService) DeletePath(_ context.Context, _ sourcecontrol.DeleteCommand) error {
	return nil
}
func (s *stubFileService) CreateDirectory(_ context.Context, _ sourcecontrol.CreateDirectoryCommand) error {
	return nil
}
func (s *stubFileService) MovePath(_ context.Context, _ sourcecontrol.MoveCommand) (*sourcecontrol.FileMutationResult, error) {
	return &sourcecontrol.FileMutationResult{Success: true}, nil
}
func (s *stubFileService) Push(context.Context, sourcecontrol.PushCommand) (*sourcecontrol.PushResult, error) {
	return &sourcecontrol.PushResult{Success: true}, nil
}
func (s *stubFileService) PushAll(context.Context, sourcecontrol.PushAllCommand) (*sourcecontrol.PushAllResult, error) {
	return &sourcecontrol.PushAllResult{}, nil
}
func (s *stubFileService) Pull(context.Context, sourcecontrol.PullCommand) (*sourcecontrol.PullResult, error) {
	return &sourcecontrol.PullResult{Success: true}, nil
}
func (s *stubFileService) Sync(context.Context, sourcecontrol.SyncCommand) (*sourcecontrol.SyncResult, error) {
	return &sourcecontrol.SyncResult{}, nil
}
func (s *stubFileService) CreatePullRequest(context.Context, sourcecontrol.CreatePullRequestCommand) (*sourcecontrol.PullRequestCreation, error) {
	return &sourcecontrol.PullRequestCreation{}, nil
}
func (s *stubFileService) ListPullRequests(context.Context, sourcecontrol.ListPullRequestsQuery) (*sourcecontrol.PullRequestList, error) {
	return &sourcecontrol.PullRequestList{}, nil
}
func (s *stubFileService) Reset(context.Context, sourcecontrol.ResetCommand) (*sourcecontrol.ResetResult, error) {
	return &sourcecontrol.ResetResult{}, nil
}
func (s *stubFileService) AgentStatus(context.Context, sourcecontrol.AgentStatusQuery) (*sourcecontrol.AgentStatusResult, error) {
	return &sourcecontrol.AgentStatusResult{}, nil
}
func (s *stubFileService) SetTargetBranch(context.Context, sourcecontrol.SetTargetBranchCommand) error {
	return nil
}
