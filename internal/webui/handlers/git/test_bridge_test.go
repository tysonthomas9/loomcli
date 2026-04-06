package git

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

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

// handleBlockedWithPool → HandleBlockedWithPool
var handleBlockedWithPool = HandleBlockedWithPool

// handleGraphWithPool → HandleGraphWithPool
var handleGraphWithPool = HandleGraphWithPool

// AgentDiffStatResult → service.AgentDiffStatResult
type AgentDiffStatResult = service.AgentDiffStatResult

// MaxListLimit from handler package
const MaxListLimit = handler.MaxListLimit

// GitSyncResult → service.GitSyncResult
type GitSyncResult = service.GitSyncResult

// GitPushAllResult → service.GitPushAllResult
type GitPushAllResult = service.GitPushAllResult

// GitPushAllWorktreeResult → service.GitPushAllWorktreeResult
type GitPushAllWorktreeResult = service.GitPushAllWorktreeResult

// ---------------------------------------------------------------------------
// stubDiffService implements service.DiffService with no-op defaults for module tests.
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
func (s *stubDiffService) GetIssueDiffStat(_ context.Context, _, _ string) (*service.IssueDiffStatResult, error) {
	return &service.IssueDiffStatResult{}, nil
}

// ---------------------------------------------------------------------------
// Mock types for graph_test.go
// ---------------------------------------------------------------------------

// mockBlockedClient implements BlockedClient for testing.
type mockBlockedClient struct {
	blockedFunc func(args *rpc.BlockedArgs) (*rpc.Response, error)
}

func (m *mockBlockedClient) Blocked(args *rpc.BlockedArgs) (*rpc.Response, error) {
	if m.blockedFunc != nil {
		return m.blockedFunc(args)
	}
	return nil, fmt.Errorf("blockedFunc not implemented")
}

// mockBlockedPool implements BlockedConnectionGetter for testing.
type mockBlockedPool struct {
	getFunc func(ctx context.Context) (BlockedClient, error)
	putFunc func(c BlockedClient)
}

func (m *mockBlockedPool) Get(ctx context.Context) (BlockedClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, fmt.Errorf("getFunc not implemented")
}

func (m *mockBlockedPool) Put(c BlockedClient) {
	if m.putFunc != nil {
		m.putFunc(c)
	}
}

// mockGraphClient implements GraphClient for testing.
type mockGraphClient struct {
	getGraphDataFunc func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error)
}

func (m *mockGraphClient) GetGraphData(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
	if m.getGraphDataFunc != nil {
		return m.getGraphDataFunc(args)
	}
	return nil, fmt.Errorf("getGraphDataFunc not implemented")
}

// mockGraphPool implements GraphConnectionGetter for testing.
type mockGraphPool struct {
	getFunc func(ctx context.Context) (GraphClient, error)
	putFunc func(c GraphClient)
}

func (m *mockGraphPool) Get(ctx context.Context) (GraphClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, fmt.Errorf("getFunc not implemented")
}

func (m *mockGraphPool) Put(c GraphClient) {
	if m.putFunc != nil {
		m.putFunc(c)
	}
}

// ---------------------------------------------------------------------------
// mockAgentService — local copy for handler-level testing
// (The original is in the root webui package's test files and can't be imported.)
// ---------------------------------------------------------------------------

type mockAgentService struct {
	getTerminalInfoFunc       func(ctx context.Context, wsID, agentName string) (*service.AgentTerminalInfoResult, error)
	generateTerminalTokenFunc func(ctx context.Context, agentName, userID string) (string, error)
	getLogFunc                func(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*service.AgentLogResult, error)
	getDiffStatFunc           func(ctx context.Context, wsID, agentName string) (*service.AgentDiffStatResult, error)
	gitPushFunc               func(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error)
	gitPushAllFunc            func(ctx context.Context, wsID string) (*service.GitPushAllResult, error)
	gitPullFunc               func(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error)
	gitSyncFunc               func(ctx context.Context, wsID, agentName string) (*service.GitSyncResult, error)
	createPRFunc              func(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error)
	gitResetFunc              func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error)
	gitStatusFunc             func(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error)
	setTargetBranchFunc       func(ctx context.Context, wsID, agentName, branch string) error
}

func (m *mockAgentService) GetTerminalInfo(ctx context.Context, wsID, agentName string) (*service.AgentTerminalInfoResult, error) {
	if m.getTerminalInfoFunc != nil {
		return m.getTerminalInfoFunc(ctx, wsID, agentName)
	}
	return &service.AgentTerminalInfoResult{Agent: agentName, Mode: "archive"}, nil
}

func (m *mockAgentService) GenerateTerminalToken(ctx context.Context, agentName, userID string) (string, error) {
	if m.generateTerminalTokenFunc != nil {
		return m.generateTerminalTokenFunc(ctx, agentName, userID)
	}
	return "test-token", nil
}

func (m *mockAgentService) GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*service.AgentLogResult, error) {
	if m.getLogFunc != nil {
		return m.getLogFunc(ctx, wsID, agentName, lines, beforeLine)
	}
	return &service.AgentLogResult{Lines: []string{}, LineCount: 0, StartLine: 1}, nil
}

func (m *mockAgentService) GetDiffStat(ctx context.Context, wsID, agentName string) (*service.AgentDiffStatResult, error) {
	if m.getDiffStatFunc != nil {
		return m.getDiffStatFunc(ctx, wsID, agentName)
	}
	return &service.AgentDiffStatResult{}, nil
}

func (m *mockAgentService) GitPush(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error) {
	if m.gitPushFunc != nil {
		return m.gitPushFunc(ctx, wsID, agentName, target)
	}
	return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
}

func (m *mockAgentService) GitPushAll(ctx context.Context, wsID string) (*service.GitPushAllResult, error) {
	if m.gitPushAllFunc != nil {
		return m.gitPushAllFunc(ctx, wsID)
	}
	return &service.GitPushAllResult{}, nil
}

func (m *mockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	if m.gitPullFunc != nil {
		return m.gitPullFunc(ctx, wsID, agentName, source)
	}
	return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
}

func (m *mockAgentService) GitSync(ctx context.Context, wsID, agentName string) (*service.GitSyncResult, error) {
	if m.gitSyncFunc != nil {
		return m.gitSyncFunc(ctx, wsID, agentName)
	}
	return &service.GitSyncResult{
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
