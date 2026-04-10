package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// module is a local interface matching webui.Module for compile-time assertions.
type module interface {
	Register(mux *http.ServeMux)
}

// ---------------------------------------------------------------------------
// Type aliases from service and terminal packages
// ---------------------------------------------------------------------------

type TerminalModule = Module
type TerminalTabModule = TabModule

type TerminalService = service.TerminalService
type SpawnParams = service.SpawnParams
type SpawnResult = service.SpawnResult
type LeadSessionParams = service.LeadSessionParams
type LeadSessionResult = service.LeadSessionResult
type TerminalRestartResult = service.TerminalRestartResult
type TerminalStatusResult = service.TerminalStatusResult
type TerminalSessionInfo = service.TerminalSessionInfo
type SeedParams = service.SeedParams
type CloseAllResult = service.CloseAllResult
type ScrollbackInfoResult = service.ScrollbackInfoResult
type ScrollbackResult = service.ScrollbackResult
type PatchTabResult = service.PatchTabResult
type AgentTerminalInfoResult = service.AgentTerminalInfoResult

type TerminalManager = webuterminal.TerminalManager
type TerminalSession = webuterminal.TerminalSession

var ErrTmuxNotFound = webuterminal.ErrTmuxNotFound

// Delegate to terminal package constructors.
var NewTerminalService = webuterminal.NewTerminalService
var NewTerminalManager = webuterminal.NewTerminalManager
var NewTerminalModule = NewModule
var NewTerminalTabModule = NewTabModule

// defaultScrollbackMaxLines from the terminal package.
const defaultScrollbackMaxLines = 10000

// maxRequestBody is the maximum request body size (1MB).
const maxRequestBody = 1 << 20

// validSessionName from terminal/manager.go
var validSessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validBackends from terminal/helpers.go
var validBackends = []string{"claude", "codex", "opencode", "gemini", "cursor"}

// isValidBackend checks if the backend name is in the allowed list.
func isValidBackend(name string) bool {
	for _, b := range validBackends {
		if b == name {
			return true
		}
	}
	return false
}

// Note: shellCommand is already defined in ws.go in this package.

// lookPathTmux from terminal/manager.go
var lookPathTmux = exec.LookPath

// newTestTerminalAuth creates a TerminalAuth for testing.
func newTestTerminalAuth() *realtime.TerminalAuth {
	ta, err := realtime.NewTerminalAuth()
	if err != nil {
		panic("failed to create terminal auth: " + err.Error())
	}
	return ta
}

// ---------------------------------------------------------------------------
// Handler function aliases (handleXxx → HandleXxx)
// ---------------------------------------------------------------------------

var handleTerminalSpawn = HandleTerminalSpawn
var handleExportSession = HandleExportSession
var handleScrollbackInfo = HandleScrollbackInfo
var handleSeedTerminalSession = HandleSeedTerminalSession
var handleListTerminalSessions = HandleListTerminalSessions
var handleTerminalRestart = HandleTerminalRestart
var handleTerminalKill = HandleTerminalKill
var handleTerminalSessionStatus = HandleTerminalSessionStatus
var handleScheduleSessionKill = HandleScheduleSessionKill
var handleCloseAllSessions = HandleCloseAllSessions
var handleGetAgentTerminalInfo = HandleGetAgentTerminalInfo
var handleGetAgentTerminalToken = HandleGetAgentTerminalToken
var handleAgentTerminalWS = HandleAgentTerminalWS
var handleTerminalWS = HandleTerminalWS
var handleListTerminalTabs = HandleListTerminalTabs
var handleGetTerminalTab = HandleGetTerminalTab
var handlePatchTerminalTab = HandlePatchTerminalTab
var handlePutTerminalTab = HandlePutTerminalTab
var handleDeleteTerminalTab = HandleDeleteTerminalTab
var handleListSessionsByIssue = HandleListSessionsByIssue
var handleGetTerminalState = HandleGetTerminalState
var handlePatchTerminalState = HandlePatchTerminalState
var handleGetScrollback = HandleGetScrollback

// ---------------------------------------------------------------------------
// Terminal context type aliases (for utils_test.go)
// ---------------------------------------------------------------------------

type TerminalContext = webuterminal.TerminalContext
type TerminalContextStats = webuterminal.TerminalContextStats
type TerminalAgentInfo = webuterminal.TerminalAgentInfo
type TerminalContextTasks = webuterminal.TerminalContextTasks

// ---------------------------------------------------------------------------
// Test helpers (duplicated from terminal/manager_test.go)
// ---------------------------------------------------------------------------

var testRunPrefix = fmt.Sprintf("tr%d", os.Getpid())

// testSessionName returns a tmux session name unique to this process and test.
func testSessionName(t *testing.T, suffix ...string) string {
	t.Helper()
	name := testRunPrefix + "-" + strings.ReplaceAll(t.Name(), "/", "-")
	if len(suffix) > 0 {
		name += "-" + suffix[0]
	}
	return name
}

// ---------------------------------------------------------------------------
// Mock config pool (for restart tests that need a workspace path)
// ---------------------------------------------------------------------------

type mockConfigClient struct {
	statusFunc func() (*rpc.StatusResponse, error)
}

func (m *mockConfigClient) Status() (*rpc.StatusResponse, error) {
	if m.statusFunc != nil {
		return m.statusFunc()
	}
	return nil, fmt.Errorf("statusFunc not set")
}

func newMockConfigPool(wsPath string) webuterminal.ConfigConnectionGetter {
	return &mockConfigPool{wsPath: wsPath}
}

type mockConfigPool struct {
	wsPath string
}

func (p *mockConfigPool) Get(_ context.Context) (webuterminal.ConfigClient, error) {
	return &mockConfigClient{
		statusFunc: func() (*rpc.StatusResponse, error) {
			return &rpc.StatusResponse{WorkspacePath: p.wsPath}, nil
		},
	}, nil
}

func (p *mockConfigPool) Put(_ webuterminal.ConfigClient) {}

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping test")
	}
}

func killTmuxSession(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("tmux", "kill-session", "-t", name) //nolint:norawexec
	_ = cmd.Run()
}

// ---------------------------------------------------------------------------
// Test helpers (duplicated from root contract_test.go)
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

// ---------------------------------------------------------------------------
// Mock/stub types from root webui test files
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
	return &service.AgentDiffStatResult{}, nil
}
func (m *mockAgentService) GitPush(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error) {
	return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
}
func (m *mockAgentService) GitPushAll(ctx context.Context, wsID string) (*service.GitPushAllResult, error) {
	return &service.GitPushAllResult{}, nil
}
func (m *mockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
}
func (m *mockAgentService) GitSync(ctx context.Context, wsID, agentName string) (*service.GitSyncResult, error) {
	return &service.GitSyncResult{}, nil
}
func (m *mockAgentService) CreatePR(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error) {
	return &ops.GitPRResult{}, nil
}
func (m *mockAgentService) GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error) {
	return &ops.GitResetResult{}, nil
}
func (m *mockAgentService) GitStatus(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error) {
	return &ops.GitStatusResult{}, nil
}
func (m *mockAgentService) SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error {
	return nil
}

type stubTerminalService struct{}

func (s *stubTerminalService) GenerateToken(_ context.Context, _, _ string) (string, error) {
	return "tok", nil
}
func (s *stubTerminalService) RestartSession(_ context.Context, _, _ string) (*service.TerminalRestartResult, error) {
	return &service.TerminalRestartResult{}, nil
}
func (s *stubTerminalService) KillSession(_ context.Context, _ string) error { return nil }
func (s *stubTerminalService) GetSessionStatus(_ context.Context, _ string) (*service.TerminalStatusResult, error) {
	return &service.TerminalStatusResult{}, nil
}
func (s *stubTerminalService) ListSessions(_ context.Context, _ string) ([]service.TerminalSessionInfo, error) {
	return nil, nil
}
func (s *stubTerminalService) SpawnSession(_ context.Context, _ string, _ *service.SpawnParams) (*service.SpawnResult, error) {
	return &service.SpawnResult{}, nil
}
func (s *stubTerminalService) CreateLeadSession(_ context.Context, _ string, _ *service.LeadSessionParams) (*service.LeadSessionResult, error) {
	return &service.LeadSessionResult{}, nil
}
func (s *stubTerminalService) SeedSession(_ context.Context, _ string, _ *service.SeedParams) error {
	return nil
}
func (s *stubTerminalService) ScheduleKill(_ context.Context, _ string) error { return nil }
func (s *stubTerminalService) CloseAllSessions(_ context.Context) (*service.CloseAllResult, error) {
	return &service.CloseAllResult{}, nil
}
func (s *stubTerminalService) ExportSession(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubTerminalService) GetScrollbackInfo(_ context.Context, _ string) (*service.ScrollbackInfoResult, error) {
	return &service.ScrollbackInfoResult{}, nil
}
func (s *stubTerminalService) GetScrollback(_ context.Context, _ string) (*service.ScrollbackResult, error) {
	return &service.ScrollbackResult{}, nil
}
func (s *stubTerminalService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	return nil, nil
}
func (s *stubTerminalService) GetTab(_ context.Context, _, _ string) (*tabmeta.TabMetadata, error) {
	return &tabmeta.TabMetadata{}, nil
}
func (s *stubTerminalService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*service.PatchTabResult, error) {
	return &service.PatchTabResult{Tab: &tabmeta.TabMetadata{}}, nil
}
func (s *stubTerminalService) PutTab(_ context.Context, _ string, _ *tabmeta.TabMetadata) error {
	return nil
}
func (s *stubTerminalService) DeleteTab(_ context.Context, _, _ string) error { return nil }
func (s *stubTerminalService) ListSessionsByIssue(_ context.Context) (map[string][]string, error) {
	return nil, nil
}
func (s *stubTerminalService) GetTerminalState(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubTerminalService) PatchTerminalState(_ context.Context, _, _ string) error { return nil }
