package misc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript/backends"
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

// handleAuthConfig is a 2-arg shim for tests written before the third
// issueBackendFn parameter was added — they don't exercise that path and
// default to nil so the env-var fallback covers them.
func handleAuthConfig(extAuthURL string, limiter *AuthConfigLimiter) http.HandlerFunc {
	return HandleAuthConfig(extAuthURL, limiter, nil)
}
var handleClientErrors = HandleClientErrors
var handleCSPReport = HandleCSPReport
var handleFileRead = HandleFileRead
var handleFileTree = HandleFileTree
var handleFileWrite = HandleFileWrite
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
var handleNotifySessionChange = HandleNotifySessionChange
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

// FileReadResult → service.FileReadResult (used by files_coverage_test.go)
type FileReadResult = service.FileReadResult

// FileTreeResult → service.FileTreeResult
type FileTreeResult = service.FileTreeResult

// FileTreeEntry → service.FileTreeEntry
type FileTreeEntry = service.FileTreeEntry

// ---------------------------------------------------------------------------
// Test-local FileService implementation (mirrors svcimpl.fileServiceImpl)
// ---------------------------------------------------------------------------

// testFileServiceImpl implements service.FileService for handler-level tests.
// This duplicates the essential logic from svcimpl to avoid the import cycle
// svcimpl → handlers/misc → svcimpl.
type testFileServiceImpl struct {
	fileOps ops.FileOps
}

// NewFileService creates a test-local FileService implementation.
func NewFileService(fileOps ops.FileOps) service.FileService {
	return &testFileServiceImpl{fileOps: fileOps}
}

func (s *testFileServiceImpl) resolveAgent(wsID, agentName string) (*ops.AgentWorktree, error) {
	if agentName == "" || !service.ValidAgentName.MatchString(agentName) {
		return nil, service.ErrValidation("invalid agent name")
	}
	wt, err := s.fileOps.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

func (s *testFileServiceImpl) ListDirectory(_ context.Context, wsID, agentName, path string) (*service.FileTreeResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = "."
	}
	fullPath := filepath.Join(wt.Path, filepath.Clean("/"+path))
	if err := validatePathWithinDir(fullPath, wt.Path); err != nil {
		return nil, service.ErrForbidden("path outside worktree")
	}
	fi, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("directory not found")
		}
		return nil, service.ErrInternal("failed to stat path", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, service.ErrForbidden("refusing to follow symlink")
	}
	if !fi.IsDir() {
		return nil, service.ErrValidation("path is not a directory")
	}
	dirEntries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, service.ErrInternal("failed to read directory", err)
	}
	sort.Slice(dirEntries, func(i, j int) bool {
		iDir := dirEntries[i].IsDir()
		jDir := dirEntries[j].IsDir()
		if iDir != jDir {
			return iDir
		}
		return dirEntries[i].Name() < dirEntries[j].Name()
	})
	entries := make([]service.FileTreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, infoErr := de.Info()
		if infoErr != nil {
			continue
		}
		entries = append(entries, service.FileTreeEntry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	relPath, _ := filepath.Rel(wt.Path, fullPath)
	if relPath == "" {
		relPath = "."
	}
	return &service.FileTreeResult{Path: relPath, Entries: entries}, nil
}

func (s *testFileServiceImpl) ReadFile(_ context.Context, wsID, agentName, path string) (*service.FileReadResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, service.ErrValidation("path parameter is required")
	}
	if isDeniedPath(path) {
		return nil, service.ErrForbidden("access to this file type is denied")
	}
	fullPath := filepath.Join(wt.Path, filepath.Clean("/"+path))
	if err := validatePathWithinDir(fullPath, wt.Path); err != nil {
		return nil, service.ErrForbidden("path outside worktree")
	}
	if isDeniedPath(fullPath) {
		return nil, service.ErrForbidden("access to this file type is denied")
	}
	fi, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("file not found")
		}
		return nil, service.ErrInternal("failed to stat file", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, service.ErrForbidden("refusing to follow symlink")
	}
	if fi.IsDir() {
		return nil, service.ErrValidation("path is a directory, not a file")
	}
	if fi.Size() > maxRequestBody {
		return nil, service.ErrPayloadTooLarge(fmt.Sprintf("file too large: %d bytes (max %d)", fi.Size(), maxRequestBody))
	}
	f, err := OpenLogFileSecure(fullPath, wt.Path)
	if err != nil {
		if strings.Contains(err.Error(), "symlink") {
			return nil, service.ErrForbidden("refusing to follow symlink")
		}
		return nil, service.ErrInternal("failed to open file", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRequestBody+1))
	if err != nil {
		return nil, service.ErrInternal("failed to read file", err)
	}
	if IsBinaryContent(data) {
		return &service.FileReadResult{Path: path, Size: fi.Size(), Binary: true}, nil
	}
	return &service.FileReadResult{Path: path, Content: string(data), Size: fi.Size()}, nil
}

func (s *testFileServiceImpl) WriteFile(_ context.Context, wsID, agentName, path, content string) error {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return err
	}
	if path == "" {
		return service.ErrValidation("path parameter is required")
	}
	if isDeniedPath(path) {
		return service.ErrForbidden("access to this file type is denied")
	}
	fullPath := filepath.Join(wt.Path, filepath.Clean("/"+path))
	if err := validatePathWithinDir(fullPath, wt.Path); err != nil {
		return service.ErrForbidden("path outside worktree")
	}
	if isDeniedPath(fullPath) {
		return service.ErrForbidden("access to this file type is denied")
	}
	if writeErr := ValidateParentDir(fullPath, wt.Path); writeErr != nil {
		switch writeErr.Message {
		case "parent directory does not exist":
			return service.ErrNotFound(writeErr.Message)
		case "parent directory is a symlink", "parent directory outside worktree":
			return service.ErrForbidden(writeErr.Message)
		default:
			return service.ErrInternal(writeErr.Message, nil)
		}
	}
	perm, writeErr := ResolveWritePermissions(fullPath)
	if writeErr != nil {
		return service.ErrForbidden(writeErr.Message)
	}
	if err := AtomicWriteFile(fullPath, content, perm); err != nil {
		return service.ErrInternal("failed to save file", err)
	}
	return nil
}

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
func (m *mockAgentService) ListAgents(_ context.Context, _ string) ([]*domain.Agent, error) {
	return nil, nil
}
func (m *mockAgentService) CreateAgent(_ context.Context, _ service.AgentCreateInput) (*domain.Agent, error) {
	return nil, nil
}
func (m *mockAgentService) UpdateAgent(_ context.Context, _, _ string, _ service.AgentUpdateInput) (*domain.Agent, error) {
	return nil, nil
}
func (m *mockAgentService) DeleteAgent(_ context.Context, _, _ string) error { return nil }

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

func (s *testSessionServiceImpl) ListTaskSessions(_ context.Context, _, taskID string) ([]service.SessionListItem, error) {
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
		if info, err := os.Stat(s.sessStore.NativeTranscriptPath(rec.SessionID)); err == nil && info.Size() > 0 {
			item.HasTranscript = true
		}
		if diff, err := s.sessStore.ReadDiff(rec.SessionID); err == nil && diff != "" {
			item.HasDiff = true
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *testSessionServiceImpl) GetSession(_ context.Context, _, taskID, sessionID string) (*service.SessionDetailData, error) {
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

func (s *testSessionServiceImpl) GetSessionTranscript(_ context.Context, _, taskID, sessionID string) ([]transcript.Event, error) {
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
	events, loadErr := s.sessStore.LoadNativeEvents(sessionID)
	if loadErr != nil {
		return nil, service.ErrInternal("failed to load transcript", loadErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func (s *testSessionServiceImpl) ListSessionSubagents(_ context.Context, _, _, sessionID string) ([]string, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	names, err := s.sessStore.ListSubagentTranscripts(sessionID)
	if err != nil {
		return nil, service.ErrInternal("list subagents", err)
	}
	ids := make([]string, 0, len(names))
	for _, n := range names {
		stripped := strings.TrimSuffix(strings.TrimPrefix(n, "agent-"), ".jsonl")
		if stripped != "" {
			ids = append(ids, stripped)
		}
	}
	return ids, nil
}

func (s *testSessionServiceImpl) GetSessionSubagentTranscript(_ context.Context, _, _, sessionID, subagentID string) ([]transcript.Event, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	if subagentID == "" {
		return nil, service.ErrValidation("subagent ID is required")
	}
	path := s.sessStore.SubagentTranscriptPath(sessionID, subagentID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("subagent transcript not found")
		}
		return nil, service.ErrInternal("stat subagent transcript", err)
	}
	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		return nil, service.ErrInternal("load metadata", err)
	}
	events, parseErr := backends.ParseEventsFromFile(meta.Backend, path)
	if parseErr != nil {
		return nil, service.ErrInternal("parse subagent transcript", parseErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func (s *testSessionServiceImpl) GetSessionDiff(_ context.Context, _, taskID, sessionID string) (string, error) {
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
