package git

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/servercapabilities"
	"github.com/tysonthomas9/loomcli/internal/webui/sourcecontrolcoord"
)

// stubGraphBackend implements backend.IssueBackend with just enough surface
// area to exercise the HandleGraphWithBackend path in unit tests.
// Every method not exercised returns a canned error.
type stubGraphBackend struct {
	list    []backend.IssueData
	details map[string]*backend.IssueDetailData
}

func (s *stubGraphBackend) BackendName() string { return "stub-graph" }

func (s *stubGraphBackend) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return s.list, nil
}

func (s *stubGraphBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	if s.details == nil {
		return nil, fmt.Errorf("no details configured")
	}
	if d, ok := s.details[id]; ok {
		return d, nil
	}
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id}}, nil
}

// Remaining IssueBackend methods — unused in graph backend tests, return
// sentinel errors so accidental use shows up as a test failure rather than
// a silent empty payload.
func (s *stubGraphBackend) Ready(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("Ready not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("Blocked not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Stats(_ context.Context) (*backend.StatsData, error) {
	return nil, fmt.Errorf("Stats not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, fmt.Errorf("Count not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) GetChildren(_ context.Context, _ string) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("GetChildren not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) SearchIssues(_ context.Context, _ string, _ int) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("SearchIssues not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Create(_ context.Context, _ backend.CreateParams) (*backend.IssueData, error) {
	return nil, fmt.Errorf("Create not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Update(_ context.Context, _ string, _ backend.UpdateParams) error {
	return fmt.Errorf("Update not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) ClaimIssue(_ context.Context, _ string, _ time.Duration) error {
	return fmt.Errorf("ClaimIssue not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) ReleaseIssueLock(_ context.Context, _, _ string) error {
	return fmt.Errorf("ReleaseIssueLock not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) DeferIssue(_ context.Context, _ string, _ time.Time) error {
	return fmt.Errorf("DeferIssue not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) UndeferIssue(_ context.Context, _ string) error {
	return fmt.Errorf("UndeferIssue not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Close(_ context.Context, _ string, _ backend.CloseParams) (*backend.CloseResult, error) {
	return nil, fmt.Errorf("Close not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Reopen(_ context.Context, _ string, _ backend.ReopenParams) error {
	return fmt.Errorf("Reopen not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Delete(_ context.Context, _ backend.DeleteParams) error {
	return fmt.Errorf("Delete not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) AddDependency(_ context.Context, _ backend.DepAddParams) error {
	return fmt.Errorf("AddDependency not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) RemoveDependency(_ context.Context, _ backend.DepRemoveParams) error {
	return fmt.Errorf("RemoveDependency not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) AddLabel(_ context.Context, _, _ string) error {
	return fmt.Errorf("AddLabel not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) RemoveLabel(_ context.Context, _, _ string) error {
	return fmt.Errorf("RemoveLabel not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, fmt.Errorf("ListComments not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) AddComment(_ context.Context, _ backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, fmt.Errorf("AddComment not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) ListEvents(_ context.Context, _ string, _ int) ([]backend.EventData, error) {
	return nil, fmt.Errorf("ListEvents not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, fmt.Errorf("Batch not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, fmt.Errorf("GetMutations not implemented in stubGraphBackend")
}
func (s *stubGraphBackend) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, fmt.Errorf("WaitForMutations not implemented in stubGraphBackend")
}

// ---------------------------------------------------------------------------
// Aliases for renamed types (old → new)
// ---------------------------------------------------------------------------

// GitModule → Module (struct alias for compile-time assertions in tests)
type GitModule = Module

// NewGitModule → NewModule
var NewGitModule = NewModule

// module is a local interface matching webui.Module for compile-time assertions.
// The root webui.Module interface cannot be imported here without creating a cycle.
type module interface {
	Register(mux *http.ServeMux)
}

// handleAgentDiffStat → HandleAgentDiffStat
var handleAgentDiffStat = HandleAgentDiffStat

// handleDiffCommits → HandleDiffCommits
var handleDiffCommits = HandleDiffCommits

// handleDiffFiles → HandleDiffFiles
var handleDiffFiles = HandleDiffFiles

// handleDiffFile → HandleDiffFile
var handleDiffFile = HandleDiffFile

// handleGitPush → HandleGitPush
var handleGitPush = HandleGitPush

// handleGitPushAll → HandleGitPushAll
var handleGitPushAll = HandleGitPushAll

// handleGitPull → HandleGitPull
var handleGitPull = HandleGitPull

// handleGitSync → HandleGitSync
var handleGitSync = HandleGitSync

// handleGitPR → HandleGitPR
var handleGitPR = HandleGitPR

// handleGitReset → HandleGitReset
var handleGitReset = HandleGitReset

// handleGitStatus → HandleGitStatus
var handleGitStatus = HandleGitStatus

// handleGitTargetUpdate → HandleGitTargetUpdate
var handleGitTargetUpdate = HandleGitTargetUpdate

// AgentDiffStatResult → agentcoord.AgentDiffStatResult
type AgentDiffStatResult = agentcoord.AgentDiffStatResult

// MaxListLimit from handler package
const MaxListLimit = handler.MaxListLimit

// GitSyncResult → agentcoord.GitSyncResult
type GitSyncResult = agentcoord.GitSyncResult

// GitPushAllResult → agentcoord.GitPushAllResult
type GitPushAllResult = agentcoord.GitPushAllResult

// GitPushAllWorktreeResult → agentcoord.GitPushAllWorktreeResult
type GitPushAllWorktreeResult = agentcoord.GitPushAllWorktreeResult

// ---------------------------------------------------------------------------
// stubDiffService implements sourcecontrolcoord.DiffService with no-op defaults for module tests.
// ---------------------------------------------------------------------------

type stubDiffService struct{}

func (s *stubDiffService) DiffCommits(_ context.Context, _, _, _ string, _ int) ([]ops.DiffCommitResult, error) {
	return nil, nil
}
func (s *stubDiffService) DiffFiles(_ context.Context, _, _, _, _ string) ([]ops.DiffFileResult, error) {
	return nil, nil
}
func (s *stubDiffService) DiffFilePatch(_ context.Context, _, _, _, _, _ string) (*ops.DiffFilePatchResult, error) {
	return &ops.DiffFilePatchResult{}, nil
}
func (s *stubDiffService) GetIssueDiffStat(_ context.Context, _, _ string) (*sourcecontrolcoord.IssueDiffStatResult, error) {
	return &sourcecontrolcoord.IssueDiffStatResult{}, nil
}

func newTestDiffService(gitOps ops.GitOps, backendFn func(context.Context) servercapabilities.IssueBackend) sourcecontrolcoord.DiffService {
	return sourcecontrolcoord.NewDiffService(gitOps, backendFn, middleware.WithWorkspace)
}

// ---------------------------------------------------------------------------
// mockAgentService — local copy for handler-level testing
// (The original is in the root webui package's test files and can't be imported.)
// ---------------------------------------------------------------------------

type mockAgentService struct {
	getTerminalInfoFunc       func(ctx context.Context, wsID, agentName string) (*agentcoord.AgentTerminalInfoResult, error)
	generateTerminalTokenFunc func(ctx context.Context, wsID, agentName, userID string) (string, error)
	getLogFunc                func(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*agentcoord.AgentLogResult, error)
	getDiffStatFunc           func(ctx context.Context, wsID, agentName string) (*agentcoord.AgentDiffStatResult, error)
	gitPushFunc               func(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error)
	gitPushAllFunc            func(ctx context.Context, wsID string) (*agentcoord.GitPushAllResult, error)
	gitPullFunc               func(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error)
	gitSyncFunc               func(ctx context.Context, wsID, agentName string) (*agentcoord.GitSyncResult, error)
	createPRFunc              func(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error)
	gitResetFunc              func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error)
	gitStatusFunc             func(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error)
	setTargetBranchFunc       func(ctx context.Context, wsID, agentName, branch string) error
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

func (m *mockAgentService) GetDiffStat(ctx context.Context, wsID, agentName string) (*agentcoord.AgentDiffStatResult, error) {
	if m.getDiffStatFunc != nil {
		return m.getDiffStatFunc(ctx, wsID, agentName)
	}
	return &agentcoord.AgentDiffStatResult{}, nil
}

func (m *mockAgentService) GitPush(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error) {
	if m.gitPushFunc != nil {
		return m.gitPushFunc(ctx, wsID, agentName, target)
	}
	return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
}

func (m *mockAgentService) GitPushAll(ctx context.Context, wsID string) (*agentcoord.GitPushAllResult, error) {
	if m.gitPushAllFunc != nil {
		return m.gitPushAllFunc(ctx, wsID)
	}
	return &agentcoord.GitPushAllResult{}, nil
}

func (m *mockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	if m.gitPullFunc != nil {
		return m.gitPullFunc(ctx, wsID, agentName, source)
	}
	return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
}

func (m *mockAgentService) GitSync(ctx context.Context, wsID, agentName string) (*agentcoord.GitSyncResult, error) {
	if m.gitSyncFunc != nil {
		return m.gitSyncFunc(ctx, wsID, agentName)
	}
	return &agentcoord.GitSyncResult{
		PushResult: &ops.GitPushResult{Success: true},
		PullResult: &ops.GitPullResult{Success: true},
	}, nil
}

func (m *mockAgentService) CreatePR(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error) {
	if m.createPRFunc != nil {
		return m.createPRFunc(ctx, wsID, agentName, target)
	}
	return &ops.GitPRResult{URL: "https://github.com/test/pr/1", Created: true}, nil
}

func (m *mockAgentService) ListPullRequests(context.Context, string, string) (*ops.GitPullRequestList, error) {
	return &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}, nil
}

func (m *mockAgentService) GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error) {
	if m.gitResetFunc != nil {
		return m.gitResetFunc(ctx, wsID, agentName, branch, force, push)
	}
	return &ops.GitResetResult{Success: true, Message: "reset done"}, nil
}

func (m *mockAgentService) GitStatus(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error) {
	if m.gitStatusFunc != nil {
		return m.gitStatusFunc(ctx, wsID, agentName)
	}
	return &ops.GitStatusResult{Branch: "feature", TargetBranch: "main", IsClean: true}, nil
}

func (m *mockAgentService) SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error {
	if m.setTargetBranchFunc != nil {
		return m.setTargetBranchFunc(ctx, wsID, agentName, branch)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Test helpers (duplicated from root webui contract_test.go)
// ---------------------------------------------------------------------------

func assertJSONResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	return result
}

func assertEnvelopeSuccess(t *testing.T, body map[string]interface{}) {
	t.Helper()
	success, ok := body["success"]
	if !ok {
		t.Fatal("missing 'success' field in response")
	}
	if success != true {
		t.Errorf("success = %v, want true", success)
	}
	if errVal, ok := body["error"]; ok {
		if str, isStr := errVal.(string); isStr && str != "" {
			t.Errorf("unexpected 'error' field in success response: %v", errVal)
		}
	}
}

func assertEnvelopeSuccessWithData(t *testing.T, body map[string]interface{}, dataFieldName string) {
	t.Helper()
	assertEnvelopeSuccess(t, body)
	if _, ok := body[dataFieldName]; !ok {
		t.Errorf("missing '%s' field in success response", dataFieldName)
	}
}

func assertEnvelopeError(t *testing.T, body map[string]interface{}, dataFieldName string) {
	t.Helper()
	success, ok := body["success"]
	if !ok {
		t.Fatal("missing 'success' field in response")
	}
	if success != false {
		t.Errorf("success = %v, want false", success)
	}
	errVal, ok := body["error"]
	if !ok {
		t.Fatal("missing 'error' field in error response")
	}
	if _, ok := errVal.(string); !ok {
		t.Errorf("'error' field is %T, want string", errVal)
	}
	if dataVal, ok := body[dataFieldName]; ok && dataVal != nil {
		t.Errorf("unexpected '%s' field in error response: %v", dataFieldName, dataVal)
	}
}
