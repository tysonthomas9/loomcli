package issues

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/types"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

// Module is a local interface matching webui.Module for compile-time assertions.
type Module interface {
	Register(mux *http.ServeMux)
}

// MaxListLimit from handler package
const MaxListLimit = handler.MaxListLimit

// ---------------------------------------------------------------------------
// Handler function aliases (handleXxx → HandleXxx)
// ---------------------------------------------------------------------------

var handleAddComment = HandleAddComment
var handleGetIssue = HandleGetIssue
var handleListIssues = HandleListIssues
var handleCreateIssue = HandleCreateIssue
var handlePatchIssue = HandlePatchIssue
var handleCloseIssue = HandleCloseIssue
var handleClaimIssue = HandleClaimIssue
var handleDeleteIssue = HandleDeleteIssue
var handleMoveIssue = HandleMoveIssue
var handleGetIssueEvents = HandleGetIssueEvents
var handleAddDependency = HandleAddDependency
var handleRemoveDependency = HandleRemoveDependency
var handleReady = HandleReady

// NewDiffService delegates to the git handlers package.
var NewDiffService = githandlers.NewDiffService

// mockGitOps implements ops.GitOps for diff_stat tests.
type mockGitOps struct {
	resolveFunc          func(name string) (*ops.AgentWorktree, error)
	diffStatFunc         func(worktreePath, fromRef string) ops.DiffStatResult
	resolveMergeBaseFunc func(worktreePath, branch string) (string, error)
	diffCommitsFunc      func(worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error)
	diffFilesFunc        func(worktreePath, from, to string) ([]ops.DiffFileResult, error)
	diffFilePatchFunc    func(worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error)
}

func (m *mockGitOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(name)
	}
	return nil, errors.New("not found")
}
func (m *mockGitOps) Push(_, _, _, _ string) (*ops.GitPushResult, error) {
	return &ops.GitPushResult{}, nil
}
func (m *mockGitOps) Pull(_, _, _, _ string) (*ops.GitPullResult, error) {
	return &ops.GitPullResult{}, nil
}
func (m *mockGitOps) CreatePR(_, _, _, _ string) (*ops.GitPRResult, error) {
	return &ops.GitPRResult{}, nil
}
func (m *mockGitOps) Reset(_, _, _ string, _, _ bool) (*ops.GitResetResult, error) {
	return &ops.GitResetResult{}, nil
}
func (m *mockGitOps) Status(_, _ string) (*ops.GitStatusResult, error) {
	return &ops.GitStatusResult{}, nil
}
func (m *mockGitOps) GetCurrentBranch(_ string) (string, error) { return "main", nil }
func (m *mockGitOps) CheckGhInstalled() error                   { return nil }
func (m *mockGitOps) SetRepoDefaultBranch(_, _, _ string) error { return nil }
func (m *mockGitOps) ListAgentWorktrees(_ string) ([]ops.AgentWorktree, error) {
	return nil, nil
}
func (m *mockGitOps) DiffStat(worktreePath, fromRef string) ops.DiffStatResult {
	if m.diffStatFunc != nil {
		return m.diffStatFunc(worktreePath, fromRef)
	}
	return ops.DiffStatResult{}
}
func (m *mockGitOps) ResolveMergeBase(worktreePath, branch string) (string, error) {
	if m.resolveMergeBaseFunc != nil {
		return m.resolveMergeBaseFunc(worktreePath, branch)
	}
	return "abc123", nil
}
func (m *mockGitOps) DiffCommits(worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error) {
	if m.diffCommitsFunc != nil {
		return m.diffCommitsFunc(worktreePath, mergeBase, limit)
	}
	return nil, nil
}
func (m *mockGitOps) DiffFiles(worktreePath, from, to string) ([]ops.DiffFileResult, error) {
	if m.diffFilesFunc != nil {
		return m.diffFilesFunc(worktreePath, from, to)
	}
	return nil, nil
}
func (m *mockGitOps) DiffFilePatch(worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error) {
	if m.diffFilePatchFunc != nil {
		return m.diffFilePatchFunc(worktreePath, from, to, path)
	}
	return &ops.DiffFilePatchResult{}, nil
}

// testWorktree returns a standard ops.AgentWorktree used across tests.
func testWorktree() *ops.AgentWorktree {
	return &ops.AgentWorktree{
		Name:          "test-agent",
		Path:          "/tmp/worktrees/test-agent",
		Branch:        "loomcli-test-agent",
		DefaultBranch: "main",
		Remote:        "origin",
		RepoName:      "myrepo",
		IsWorkspace:   true,
	}
}

// ---------------------------------------------------------------------------
// Local interfaces for test mocks (were in root webui package)
// ---------------------------------------------------------------------------

// blockedClient is a local interface for test mocks.
type blockedClient interface {
	Blocked(args *rpc.BlockedArgs) (*rpc.Response, error)
}

// graphClient is a local interface for test mocks.
type graphClient interface {
	GetGraphData(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error)
}

// ---------------------------------------------------------------------------
// mockIssueService — local copy for handler-level testing
// ---------------------------------------------------------------------------

type mockIssueService struct {
	getIssueFunc         func(ctx context.Context, issueID string) (json.RawMessage, error)
	listIssuesFunc       func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error)
	createIssueFunc      func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error)
	patchIssueFunc       func(ctx context.Context, params service.PatchIssueParams) error
	closeIssueFunc       func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error)
	claimIssueFunc       func(ctx context.Context, params service.ClaimIssueParams) (json.RawMessage, error)
	deleteIssueFunc      func(ctx context.Context, issueID string) (json.RawMessage, error)
	addCommentFunc       func(ctx context.Context, params service.AddCommentParams) (*types.Comment, error)
	addDependencyFunc    func(ctx context.Context, params service.AddDependencyParams) error
	removeDependencyFunc func(ctx context.Context, params service.RemoveDependencyParams) error
	listEventsFunc       func(ctx context.Context, params service.EventListParams) ([]*types.Event, error)
	moveIssueFunc        func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error)
}

func (m *mockIssueService) GetIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	if m.getIssueFunc != nil {
		return m.getIssueFunc(ctx, issueID)
	}
	return nil, nil
}
func (m *mockIssueService) ListIssues(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
	if m.listIssuesFunc != nil {
		return m.listIssuesFunc(ctx, params)
	}
	return &service.ListIssuesResult{Issues: []service.IssueWithParent{}}, nil
}
func (m *mockIssueService) CreateIssue(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
	if m.createIssueFunc != nil {
		return m.createIssueFunc(ctx, params)
	}
	return nil, nil
}
func (m *mockIssueService) PatchIssue(ctx context.Context, params service.PatchIssueParams) error {
	if m.patchIssueFunc != nil {
		return m.patchIssueFunc(ctx, params)
	}
	return nil
}
func (m *mockIssueService) CloseIssue(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
	if m.closeIssueFunc != nil {
		return m.closeIssueFunc(ctx, params)
	}
	return nil, nil
}
func (m *mockIssueService) ClaimIssue(ctx context.Context, params service.ClaimIssueParams) (json.RawMessage, error) {
	if m.claimIssueFunc != nil {
		return m.claimIssueFunc(ctx, params)
	}
	return nil, nil
}
func (m *mockIssueService) DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	if m.deleteIssueFunc != nil {
		return m.deleteIssueFunc(ctx, issueID)
	}
	return nil, nil
}
func (m *mockIssueService) AddComment(ctx context.Context, params service.AddCommentParams) (*types.Comment, error) {
	if m.addCommentFunc != nil {
		return m.addCommentFunc(ctx, params)
	}
	return nil, nil
}
func (m *mockIssueService) AddDependency(ctx context.Context, params service.AddDependencyParams) error {
	if m.addDependencyFunc != nil {
		return m.addDependencyFunc(ctx, params)
	}
	return nil
}
func (m *mockIssueService) RemoveDependency(ctx context.Context, params service.RemoveDependencyParams) error {
	if m.removeDependencyFunc != nil {
		return m.removeDependencyFunc(ctx, params)
	}
	return nil
}
func (m *mockIssueService) ListEvents(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
	if m.listEventsFunc != nil {
		return m.listEventsFunc(ctx, params)
	}
	return nil, nil
}
func (m *mockIssueService) MoveIssue(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
	if m.moveIssueFunc != nil {
		return m.moveIssueFunc(ctx, params)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Blocked/graph-related aliases (these functions/types are in handlers/git
// but some integration tests in the issues package reference them)
// ---------------------------------------------------------------------------

// BlockedResponse from git handlers package.
type BlockedResponse = githandlers.BlockedResponse

// handleBlocked → githandlers.HandleBlocked
var handleBlocked = githandlers.HandleBlocked

// splitAndTrim is a local copy of the unexported git utility function.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseBlockedParams is a local copy of the unexported git utility function.
func parseBlockedParams(r *http.Request) (*rpc.BlockedArgs, error) {
	q := r.URL.Query()
	args := &rpc.BlockedArgs{
		ParentID: q.Get("parent_id"),
		Assignee: q.Get("assignee"),
		Type:     q.Get("type"),
	}
	if p := q.Get("priority"); p != "" {
		v, err := strconv.Atoi(p)
		if err != nil {
			return nil, errors.New("invalid priority: must be an integer")
		}
		if v < 0 || v > 4 {
			return nil, errors.New("invalid priority: must be 0-4")
		}
		args.Priority = &v
	}
	if l := q.Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			return nil, errors.New("invalid limit: must be an integer")
		}
		if v < 0 {
			return nil, errors.New("invalid limit: must be non-negative")
		}
		if v > handler.MaxListLimit {
			v = handler.MaxListLimit
		}
		args.Limit = v
	}
	return args, nil
}

// ---------------------------------------------------------------------------
// Test assertion helpers (duplicated from root webui contract_test.go)
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

// ---------------------------------------------------------------------------
// Test-local SessionService (for session_history_test.go)
// ---------------------------------------------------------------------------

type testSessionServiceImpl struct {
	sessStore *sessions.Store
	histStore *sessionhistory.Store
}

// NewSessionService creates a test-local SessionService, mirroring
// svcimpl.NewSessionService to avoid a circular import.
func NewSessionService(sessStore *sessions.Store, histStore *sessionhistory.Store) service.SessionService {
	return &testSessionServiceImpl{sessStore: sessStore, histStore: histStore}
}

func (s *testSessionServiceImpl) ListTaskSessions(_ context.Context, _, taskID string) ([]service.SessionListItem, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	records, err := s.sessStore.SessionsByTask(taskID)
	if err != nil {
		return nil, service.ErrInternal("failed to list sessions", err)
	}
	items := make([]service.SessionListItem, 0, len(records))
	for _, rec := range records {
		item := service.SessionListItem{SessionRecord: rec, IsActive: rec.Status == sessions.StatusRunning}
		items = append(items, item)
	}
	return items, nil
}

func (s *testSessionServiceImpl) GetSession(_ context.Context, _, _, sessionID string) (*service.SessionDetailData, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, service.ErrNotFound("session not found")
		}
		return nil, service.ErrInternal("failed to load session", err)
	}
	return &service.SessionDetailData{SessionMetadata: *meta, IsActive: meta.Status == sessions.StatusRunning}, nil
}

func (s *testSessionServiceImpl) GetSessionTranscript(_ context.Context, _, _, sessionID string) ([]transcript.Event, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	events, err := s.sessStore.LoadNativeEvents(sessionID)
	if err != nil {
		return nil, service.ErrInternal("failed to load transcript", err)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func (s *testSessionServiceImpl) ListSessionSubagents(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

func (s *testSessionServiceImpl) GetSessionSubagentTranscript(_ context.Context, _, _, _, _ string) ([]transcript.Event, error) {
	return nil, nil
}

func (s *testSessionServiceImpl) GetSessionDiff(_ context.Context, _, _, sessionID string) (string, error) {
	if s.sessStore == nil {
		return "", service.ErrUnavailable("session store not available")
	}
	diff, err := s.sessStore.ReadDiff(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", service.ErrNotFound("diff not found")
		}
		return "", service.ErrInternal("failed to read diff", err)
	}
	return diff, nil
}

func (s *testSessionServiceImpl) ListSessionHistory(ctx context.Context, wsID, issueID string) ([]sessionhistory.SessionRecord, error) {
	if s.histStore == nil {
		return nil, service.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessionhistory.ValidateIssueID(issueID); err != nil {
		return nil, service.ErrValidation(err.Error())
	}
	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		return nil, service.ErrInternal("failed to list session history", err)
	}
	return records, nil
}

func (s *testSessionServiceImpl) GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*service.SessionScrollbackResult, error) {
	if s.histStore == nil {
		return nil, service.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessionhistory.ValidateIssueID(issueID); err != nil {
		return nil, service.ErrValidation(err.Error())
	}
	if recordID == "" {
		return nil, service.ErrValidation("record ID is required")
	}
	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		return nil, service.ErrInternal("failed to get session history", err)
	}
	var found *sessionhistory.SessionRecord
	for i := range records {
		if records[i].ID == recordID {
			found = &records[i]
			break
		}
	}
	if found == nil {
		return nil, service.ErrNotFound("session record not found")
	}
	if found.ScrollbackPath == "" {
		return nil, service.ErrNotFound("no scrollback available for this session")
	}
	homeDir, _ := os.UserHomeDir()
	expectedPrefix := filepath.Clean(homeDir+"/.loom/session-scrollback") + string(os.PathSeparator)
	cleanPath := filepath.Clean(found.ScrollbackPath)
	if !strings.HasPrefix(cleanPath+string(os.PathSeparator), expectedPrefix) {
		return nil, service.ErrValidation("invalid scrollback path")
	}
	f, err := os.Open(cleanPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("scrollback file not found")
		}
		return nil, service.ErrInternal("failed to read scrollback", err)
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		return nil, service.ErrInternal("failed to read scrollback", err)
	}
	text := string(content)
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return &service.SessionScrollbackResult{Content: text, Lines: lines}, nil
}

func assertPlainError(t *testing.T, body map[string]interface{}) {
	t.Helper()
	errVal, ok := body["error"]
	if !ok {
		t.Fatal("missing 'error' field in response")
	}
	if _, ok := errVal.(string); !ok {
		t.Errorf("'error' field is %T, want string", errVal)
	}
}

// ---------------------------------------------------------------------------
// stubSessionService — no-op service.SessionService for module tests
// ---------------------------------------------------------------------------

type stubSessionService struct{}

func (s *stubSessionService) ListTaskSessions(_ context.Context, _, _ string) ([]service.SessionListItem, error) {
	return nil, nil
}
func (s *stubSessionService) GetSession(_ context.Context, _, _, _ string) (*service.SessionDetailData, error) {
	return &service.SessionDetailData{}, nil
}
func (s *stubSessionService) GetSessionTranscript(_ context.Context, _, _, _ string) ([]transcript.Event, error) {
	return nil, nil
}
func (s *stubSessionService) ListSessionSubagents(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionSubagentTranscript(_ context.Context, _, _, _, _ string) ([]transcript.Event, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionDiff(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}
func (s *stubSessionService) ListSessionHistory(_ context.Context, _, _ string) ([]sessionhistory.SessionRecord, error) {
	return nil, nil
}
func (s *stubSessionService) GetSessionScrollback(_ context.Context, _, _, _ string) (*service.SessionScrollbackResult, error) {
	return &service.SessionScrollbackResult{}, nil
}
