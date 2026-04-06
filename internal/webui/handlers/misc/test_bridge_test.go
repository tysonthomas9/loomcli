package misc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
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

// NewFileModule → NewModule
var NewFileModule = NewModule

// maxRequestBody is the maximum request body size (1MB).
const maxRequestBody = 1 << 20

// newCSPReportLimiter → NewCSPReportLimiter
var newCSPReportLimiter = NewCSPReportLimiter

// ---------------------------------------------------------------------------
// Handler function aliases (handleXxx → HandleXxx)
// ---------------------------------------------------------------------------

var handleAuthConfig = HandleAuthConfig
var handleClientErrors = HandleClientErrors
var handleCSPReport = HandleCSPReport
var handleFileRead = HandleFileRead
var handleFileTree = HandleFileTree
var handleFileWrite = HandleFileWrite
var handleGetAgentLog = HandleGetAgentLog
var handleGetBackendsHealth = HandleGetBackendsHealth
var handleGetBackendConfig = HandleGetBackendConfig
var handleListEditors = HandleListEditors
var handleOpenEditor = HandleOpenEditor
var handleGetTaskLog = HandleGetTaskLog
var handleListTaskPhases = HandleListTaskPhases
var handleGetSession = HandleGetSession
var handleGetSessionDiff = HandleGetSessionDiff
var handleGetSessionTranscript = HandleGetSessionTranscript
var handleListTaskSessions = HandleListTaskSessions
var handleNotifySessionChange = HandleNotifySessionChange
var handlePatchBackendConfig = HandlePatchBackendConfig
var handleWorkerRegister = HandleWorkerRegister

// ---------------------------------------------------------------------------
// Limiter constructor aliases (newXxxLimiter → NewXxxLimiter)
// ---------------------------------------------------------------------------

var newAuthConfigLimiter = NewAuthConfigLimiter
var newClientErrorLimiter = NewClientErrorLimiter

// clientErrorLimiter → ClientErrorLimiter (type alias)
type clientErrorLimiter = ClientErrorLimiter

// AgentLogResult → service.AgentLogResult
type AgentLogResult = service.AgentLogResult

// ReadLastNLines wraps the package-internal readLastNLinesFromFile without
// secure-directory restrictions — suitable for unit tests only.
func ReadLastNLines(filepath string, n int) ([]string, int64, error) {
	return readLastNLinesFromFile(filepath, n, nil, 0)
}

// mockAgentService implements service.AgentService with no-op defaults.
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
	return &ops.GitPushResult{Success: true}, nil
}
func (m *mockAgentService) GitPushAll(ctx context.Context, wsID string) (*service.GitPushAllResult, error) {
	return &service.GitPushAllResult{}, nil
}
func (m *mockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	return &ops.GitPullResult{Success: true}, nil
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

// ---------------------------------------------------------------------------
// Stub types for module tests
// ---------------------------------------------------------------------------

// stubFileService implements service.FileService with no-op defaults for module tests.
type stubFileService struct{}

func (s *stubFileService) ListDirectory(_ context.Context, _, _, _ string) (*service.FileTreeResult, error) {
	return &service.FileTreeResult{}, nil
}
func (s *stubFileService) ReadFile(_ context.Context, _, _, _ string) (*service.FileReadResult, error) {
	return &service.FileReadResult{}, nil
}
func (s *stubFileService) WriteFile(_ context.Context, _, _, _, _ string) error { return nil }

// ---------------------------------------------------------------------------
// Test-local SessionService implementation
// ---------------------------------------------------------------------------

// validTaskID and validSessionID are already defined in the misc package
// (logs.go and sessions.go), so we reuse them here.

// testSessionServiceImpl mirrors the root webui.sessionServiceImpl for tests.
type testSessionServiceImpl struct {
	sessStore *sessions.Store
	histStore *sessionhistory.Store
}

// NewSessionService creates a test-local SessionService implementation.
// This mirrors the root webui.NewSessionService, duplicated here to avoid
// a circular import (webui → handlers/misc → webui).
func NewSessionService(sessStore *sessions.Store, histStore *sessionhistory.Store) service.SessionService {
	return &testSessionServiceImpl{sessStore: sessStore, histStore: histStore}
}

func (s *testSessionServiceImpl) ListTaskSessions(_ context.Context, taskID string) ([]service.SessionListItem, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID: must match [a-zA-Z0-9._-]+")
	}
	records, err := s.sessStore.SessionsByTask(taskID)
	if err != nil {
		return nil, service.ErrInternal("failed to list sessions", err)
	}
	items := make([]service.SessionListItem, 0, len(records))
	for _, rec := range records {
		item := service.SessionListItem{
			SessionRecord: rec,
			IsActive:      rec.Status == sessions.StatusRunning,
		}
		if entries, err := s.sessStore.LoadTranscript(rec.SessionID); err == nil && len(entries) > 0 {
			item.HasTranscript = true
		}
		if diff, err := s.sessStore.ReadDiff(rec.SessionID); err == nil && diff != "" {
			item.HasDiff = true
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *testSessionServiceImpl) GetSession(_ context.Context, taskID, sessionID string) (*service.SessionDetailData, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, service.ErrValidation("invalid session ID")
	}
	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, service.ErrNotFound("session not found")
		}
		return nil, service.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return nil, service.ErrNotFound("session not found")
	}
	return &service.SessionDetailData{
		SessionMetadata: *meta,
		IsActive:        meta.Status == sessions.StatusRunning,
	}, nil
}

func (s *testSessionServiceImpl) GetSessionTranscript(_ context.Context, taskID, sessionID string) ([]sessions.TranscriptEntry, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, service.ErrValidation("invalid session ID")
	}
	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, service.ErrNotFound("session not found")
		}
		return nil, service.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return nil, service.ErrNotFound("session not found")
	}
	entries, loadErr := s.sessStore.LoadTranscript(sessionID)
	if loadErr != nil {
		return nil, service.ErrInternal("failed to load transcript", loadErr)
	}
	if entries == nil {
		entries = []sessions.TranscriptEntry{}
	}
	return entries, nil
}

func (s *testSessionServiceImpl) GetSessionDiff(_ context.Context, taskID, sessionID string) (string, error) {
	if s.sessStore == nil {
		return "", service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return "", service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return "", service.ErrValidation("invalid session ID")
	}
	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", service.ErrNotFound("session not found")
		}
		return "", service.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return "", service.ErrNotFound("session not found")
	}
	diff, diffErr := s.sessStore.ReadDiff(sessionID)
	if diffErr != nil {
		if errors.Is(diffErr, os.ErrNotExist) {
			return "", service.ErrNotFound("diff not found")
		}
		return "", service.ErrInternal("failed to read diff", diffErr)
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
	return nil, service.ErrUnavailable("not implemented in test")
}
