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
	// List/Read use the lead-aware resolver so a lead (no local worktree) falls
	// back to the workspace primary worktree. Mirrors svcimpl.fileServiceImpl.
	wt, err := s.fileOps.ResolveAgentWorktreeOrPrimary(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

// resolveAgentForWrite resolves the agent's own worktree for writes — no lead
// primary fallback, so a lead can't mutate the primary repo from the viewer.
// Mirrors svcimpl.fileServiceImpl.resolveAgentForWrite.
func (s *testFileServiceImpl) resolveAgentForWrite(wsID, agentName string) (*ops.AgentWorktree, error) {
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
	return s.ListDirectoryScoped(context.Background(), wsID, service.ScopeAgent, agentName, "", path)
}

func (s *testFileServiceImpl) ReadFile(_ context.Context, wsID, agentName, path string) (*service.FileReadResult, error) {
	return s.ReadFileScoped(context.Background(), wsID, service.ScopeAgent, agentName, "", path)
}

func (s *testFileServiceImpl) WriteFile(_ context.Context, wsID, agentName, path, content string) error {
	return s.WriteFileScoped(context.Background(), wsID, service.ScopeAgent, agentName, "", path, content)
}

// resolveScopeRootTest mirrors svcimpl.fileServiceImpl.resolveScopeRoot.
func (s *testFileServiceImpl) resolveScopeRootTest(wsID string, scope service.FileScope, target, repo string) (string, error) {
	if repo != "" && scope != service.ScopeAgent {
		return "", service.ErrValidation("repo qualifier is only supported for agent scope")
	}
	switch scope {
	case service.ScopeWorkspace:
		if target != "" {
			return "", service.ErrValidation("workspace scope does not take a target")
		}
		root, err := s.fileOps.ResolveWorkspaceRoot(wsID)
		if err != nil {
			return "", service.ErrNotFound(err.Error())
		}
		return root, nil
	case service.ScopeRepo:
		if target == "" {
			return "", service.ErrValidation("repo scope requires a target")
		}
		ws, err := s.fileOps.ResolveWorkspaceData(wsID)
		if err != nil {
			return "", service.ErrNotFound(err.Error())
		}
		repoName := ""
		for _, repo := range ws.Repos {
			if repo.Name == target {
				repoName = repo.Name
				break
			}
		}
		if repoName == "" {
			return "", service.ErrNotFound(fmt.Sprintf("repo %q not found", target))
		}
		root, err := s.fileOps.ResolveWorkspaceRoot(wsID)
		if err != nil {
			return "", service.ErrNotFound(err.Error())
		}
		return filepath.Join(root, repoName), nil
	case service.ScopeAgent:
		if target == "" || !service.IsValidAgentName(target) {
			return "", service.ErrValidation("invalid agent name")
		}
		ws, err := s.fileOps.ResolveWorkspaceData(wsID)
		if err != nil {
			return "", service.ErrNotFound(err.Error())
		}
		var found bool
		for _, agent := range ws.Agents {
			if agent.Name == target {
				found = true
				break
			}
		}
		if !found {
			return "", service.ErrNotFound(fmt.Sprintf("agent %q not found", target))
		}
		var wt *ops.AgentWorktree
		if repo != "" {
			wt, err = s.fileOps.ResolveAgentWorktreeForRepo(wsID, target, repo)
		} else {
			wt, err = s.fileOps.ResolveAgentWorktree(wsID, target)
		}
		if err != nil {
			return "", service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", target))
		}
		return wt.Path, nil
	default:
		return "", service.ErrValidation(fmt.Sprintf("unsupported scope %q", scope))
	}
}

func (s *testFileServiceImpl) ListDirectoryScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileTreeResult, error) {
	root, err := s.resolveScopeRootTest(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	_, fullPath, err := testScopedFullPath(root, path, true)
	if err != nil {
		return nil, err
	}
	if err := testNoSymlinkComponents(root, fullPath); err != nil {
		return nil, err
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
		if de.Type()&os.ModeSymlink != 0 || strings.EqualFold(de.Name(), ".git") {
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
	relPath, _ := filepath.Rel(root, fullPath)
	if relPath == "" {
		relPath = "."
	}
	return &service.FileTreeResult{Path: relPath, Entries: entries}, nil
}

func (s *testFileServiceImpl) ReadFileScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileReadResult, error) {
	root, err := s.resolveScopeRootTest(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	cleanPath, fullPath, err := testScopedFullPath(root, path, false)
	if err != nil {
		return nil, err
	}
	if err := testNoSymlinkComponents(root, fullPath); err != nil {
		return nil, err
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
	f, err := OpenLogFileSecure(fullPath, root)
	if err != nil {
		if strings.Contains(err.Error(), "symlink") {
			return nil, service.ErrForbidden("refusing to follow symlink")
		}
		return nil, service.ErrInternal("failed to open file", err)
	}
	defer f.Close()
	truncated := fi.Size() > maxRequestBody
	data, err := io.ReadAll(io.LimitReader(f, maxRequestBody))
	if err != nil {
		return nil, service.ErrInternal("failed to read file", err)
	}
	if IsBinaryContent(data) {
		return &service.FileReadResult{Path: cleanPath, Size: fi.Size(), Binary: true, Truncated: truncated}, nil
	}
	return &service.FileReadResult{Path: cleanPath, Content: string(data), Size: fi.Size(), Truncated: truncated}, nil
}

func (s *testFileServiceImpl) StatPathScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileStatResult, error) {
	return &service.FileStatResult{}, nil
}

func (s *testFileServiceImpl) ReadFileAtRevScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _ string) (*service.FileReadResult, error) {
	return &service.FileReadResult{}, nil
}

func (s *testFileServiceImpl) IndexFilesScoped(_ context.Context, wsID string, scope service.FileScope, target, repo string) (*service.FileIndexResult, error) {
	if _, err := s.resolveScopeRootTest(wsID, scope, target, repo); err != nil {
		return nil, err
	}
	return &service.FileIndexResult{Paths: []string{}, Truncated: false}, nil
}

func (s *testFileServiceImpl) SearchFilesScoped(_ context.Context, wsID string, scope service.FileScope, target, repo string, _ service.FileSearchRequest) (*service.FileSearchResult, error) {
	if _, err := s.resolveScopeRootTest(wsID, scope, target, repo); err != nil {
		return nil, err
	}
	return &service.FileSearchResult{Results: []service.FileSearchFileResult{}, LimitHit: false}, nil
}

func (s *testFileServiceImpl) GitStatusScoped(_ context.Context, _ string, _ service.FileScope, _, _ string) (service.FileGitStatusResult, error) {
	return service.FileGitStatusResult{}, nil
}

func (s *testFileServiceImpl) ListFileCheckouts(_ context.Context, _ string) (*service.FileCheckoutsResult, error) {
	return &service.FileCheckoutsResult{}, nil
}

func (s *testFileServiceImpl) RepairCheckout(_ context.Context, wsID string, req service.FileCheckoutRepairRequest) (*ops.RepairResult, error) {
	result, err := s.fileOps.RepairCheckout(wsID, req.Scope, req.Target, req.Repo, req.Force)
	if err != nil {
		if errors.Is(err, ops.ErrCheckoutTargetNotAllowed) || errors.Is(err, ops.ErrAgentRepoNotAllowed) {
			return nil, service.ErrValidation("checkout target is not allowed")
		}
		return nil, err
	}
	return &result, nil
}

func (s *testFileServiceImpl) DiffFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _, _ string) (*service.FileDiffResult, error) {
	return &service.FileDiffResult{}, nil
}

func (s *testFileServiceImpl) BlameFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileBlameResult, error) {
	return &service.FileBlameResult{}, nil
}

func (s *testFileServiceImpl) HistoryFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileHistoryResult, error) {
	return &service.FileHistoryResult{}, nil
}

func (s *testFileServiceImpl) WriteFileScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path, content string) error {
	root, err := s.resolveScopeRootTest(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	return testWriteFileAt(root, path, content)
}

func (s *testFileServiceImpl) WriteFileConditionalScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path, content string, _ service.FileWritePreconditions) (*service.FileMutationResult, error) {
	if err := s.WriteFileScoped(ctx, wsID, scope, target, repo, path, content); err != nil {
		return nil, err
	}
	return &service.FileMutationResult{Success: true, Version: "sha256:test"}, nil
}

func (s *testFileServiceImpl) DeletePathScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string, recursive bool) error {
	root, err := s.resolveScopeRootTest(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	return testDeletePathAt(root, path, recursive)
}

func (s *testFileServiceImpl) DeletePathVersionedScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string, recursive bool, _ string) error {
	return s.DeletePathScoped(ctx, wsID, scope, target, repo, path, recursive)
}

func (s *testFileServiceImpl) MkdirScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) error {
	root, err := s.resolveScopeRootTest(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	return testMkdirAt(root, path)
}

func (s *testFileServiceImpl) MovePathScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, from, to string, overwrite bool) error {
	root, err := s.resolveScopeRootTest(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	return testMovePathAt(root, from, to, overwrite)
}

func (s *testFileServiceImpl) MovePathVersionedScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, from, to string, overwrite bool, _, _ string) (*service.FileMutationResult, error) {
	if err := s.MovePathScoped(ctx, wsID, scope, target, repo, from, to, overwrite); err != nil {
		return nil, err
	}
	return &service.FileMutationResult{Success: true, Version: "sha256:test"}, nil
}

func testScopedFullPath(rootDir, path string, allowEmpty bool) (string, string, error) {
	if path == "" {
		if !allowEmpty {
			return "", "", service.ErrValidation("path parameter is required")
		}
		path = "."
	}
	if filepath.IsAbs(path) {
		return "", "", service.ErrForbidden("path outside root")
	}
	cleanPath := filepath.Clean(path)
	for _, segment := range strings.Split(cleanPath, string(filepath.Separator)) {
		if strings.EqualFold(segment, ".git") {
			return "", "", service.ErrForbidden(".git paths are not available")
		}
	}
	fullPath := filepath.Join(rootDir, cleanPath)
	if err := validatePathWithinDir(fullPath, rootDir); err != nil {
		return "", "", service.ErrForbidden("path outside root")
	}
	relPath, err := filepath.Rel(rootDir, fullPath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPath) {
		return "", "", service.ErrForbidden("path outside root")
	}
	if relPath == "" {
		relPath = "."
	}
	return relPath, fullPath, nil
}

func testNoSymlinkComponents(rootDir, fullPath string) error {
	relPath, err := filepath.Rel(rootDir, fullPath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPath) {
		return service.ErrForbidden("path outside root")
	}
	if relPath == "." {
		return nil
	}
	current := rootDir
	for _, segment := range strings.Split(relPath, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		fi, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return service.ErrInternal("failed to stat path", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return service.ErrForbidden("refusing to follow symlink")
		}
	}
	return nil
}

func testExistingParent(fullPath, rootDir string) error {
	parentDir := filepath.Dir(fullPath)
	if err := validatePathWithinDir(parentDir, rootDir); err != nil {
		return service.ErrForbidden("parent directory outside root")
	}
	if err := testNoSymlinkComponents(rootDir, parentDir); err != nil {
		return err
	}
	parentFi, err := os.Lstat(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return service.ErrNotFound("parent directory does not exist")
		}
		return service.ErrInternal("failed to stat parent directory", err)
	}
	if parentFi.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("parent directory is a symlink")
	}
	if !parentFi.IsDir() {
		return service.ErrValidation("parent path is not a directory")
	}
	return nil
}

func testWriteFileAt(rootDir, path, content string) error {
	_, fullPath, err := testScopedFullPath(rootDir, path, false)
	if err != nil {
		return err
	}
	if err := testNoSymlinkComponents(rootDir, fullPath); err != nil {
		return err
	}
	if err := testExistingParent(fullPath, rootDir); err != nil {
		return err
	}
	perm := os.FileMode(0644)
	if fi, err := os.Lstat(fullPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return service.ErrForbidden("refusing to overwrite symlink")
		}
		if fi.IsDir() {
			return service.ErrValidation("path is a directory, not a file")
		}
		perm = fi.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return service.ErrInternal("failed to stat file", err)
	}
	if err := AtomicWriteFile(fullPath, content, perm); err != nil {
		return service.ErrInternal("failed to save file", err)
	}
	return nil
}

func testDeletePathAt(rootDir, path string, recursive bool) error {
	_, fullPath, err := testScopedFullPath(rootDir, path, false)
	if err != nil {
		return err
	}
	if err := testNoSymlinkComponents(rootDir, fullPath); err != nil {
		return err
	}
	if err := testExistingParent(fullPath, rootDir); err != nil {
		return err
	}
	fi, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return service.ErrNotFound("path not found")
		}
		return service.ErrInternal("failed to stat path", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("refusing to follow symlink")
	}
	if fi.IsDir() {
		if recursive {
			if err := os.RemoveAll(fullPath); err != nil {
				return service.ErrInternal("failed to delete directory", err)
			}
			return nil
		}
		entries, err := os.ReadDir(fullPath)
		if err != nil {
			return service.ErrInternal("failed to read directory", err)
		}
		if len(entries) > 0 {
			return service.ErrConflict("directory not empty")
		}
	}
	if err := os.Remove(fullPath); err != nil {
		return service.ErrInternal("failed to delete path", err)
	}
	return nil
}

func testMkdirAt(rootDir, path string) error {
	_, fullPath, err := testScopedFullPath(rootDir, path, false)
	if err != nil {
		return err
	}
	if err := testNoSymlinkComponents(rootDir, fullPath); err != nil {
		return err
	}
	if fi, err := os.Lstat(fullPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return service.ErrForbidden("refusing to follow symlink")
		}
		if !fi.IsDir() {
			return service.ErrConflict("file exists at path")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return service.ErrInternal("failed to stat path", err)
	}
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return service.ErrInternal("failed to create directory", err)
	}
	return nil
}

func testMovePathAt(rootDir, from, to string, overwrite bool) error {
	_, fromPath, err := testScopedFullPath(rootDir, from, false)
	if err != nil {
		return err
	}
	_, toPath, err := testScopedFullPath(rootDir, to, false)
	if err != nil {
		return err
	}
	if err := testNoSymlinkComponents(rootDir, fromPath); err != nil {
		return err
	}
	if err := testNoSymlinkComponents(rootDir, toPath); err != nil {
		return err
	}
	if err := testExistingParent(fromPath, rootDir); err != nil {
		return err
	}
	if err := testExistingParent(toPath, rootDir); err != nil {
		return err
	}
	if fi, err := os.Lstat(fromPath); err != nil {
		if os.IsNotExist(err) {
			return service.ErrNotFound("source path not found")
		}
		return service.ErrInternal("failed to stat source path", err)
	} else if fi.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("refusing to follow symlink")
	}
	if destFi, err := os.Lstat(toPath); err == nil {
		if destFi.Mode()&os.ModeSymlink != 0 {
			return service.ErrForbidden("refusing to follow symlink")
		}
		if !overwrite {
			return service.ErrConflict("destination exists")
		}
	} else if !os.IsNotExist(err) {
		return service.ErrInternal("failed to stat destination path", err)
	}
	if err := os.Rename(fromPath, toPath); err != nil {
		return service.ErrInternal("failed to move path", err)
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
	generateTerminalTokenFunc func(ctx context.Context, wsID, agentName, userID string) (string, error)
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
func (m *mockAgentService) GenerateTerminalToken(ctx context.Context, wsID, agentName, userID string) (string, error) {
	if m.generateTerminalTokenFunc != nil {
		return m.generateTerminalTokenFunc(ctx, wsID, agentName, userID)
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

func (m *mockAgentService) ListPullRequests(context.Context, string, string) (*ops.GitPullRequestList, error) {
	return &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}, nil
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
func (m *mockAgentService) RequestAgentLifecycle(_ context.Context, _, _ string, _ service.AgentLifecycleInput) (*domain.Agent, error) {
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
func (s *stubFileService) ListDirectoryScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileTreeResult, error) {
	return &service.FileTreeResult{}, nil
}
func (s *stubFileService) ReadFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileReadResult, error) {
	return &service.FileReadResult{}, nil
}
func (s *stubFileService) StatPathScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileStatResult, error) {
	return &service.FileStatResult{}, nil
}
func (s *stubFileService) ReadFileAtRevScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _ string) (*service.FileReadResult, error) {
	return &service.FileReadResult{}, nil
}
func (s *stubFileService) IndexFilesScoped(_ context.Context, _ string, _ service.FileScope, _, _ string) (*service.FileIndexResult, error) {
	return &service.FileIndexResult{}, nil
}
func (s *stubFileService) SearchFilesScoped(_ context.Context, _ string, _ service.FileScope, _, _ string, _ service.FileSearchRequest) (*service.FileSearchResult, error) {
	return &service.FileSearchResult{}, nil
}
func (s *stubFileService) GitStatusScoped(_ context.Context, _ string, _ service.FileScope, _, _ string) (service.FileGitStatusResult, error) {
	return service.FileGitStatusResult{}, nil
}
func (s *stubFileService) ListFileCheckouts(_ context.Context, _ string) (*service.FileCheckoutsResult, error) {
	return &service.FileCheckoutsResult{}, nil
}
func (s *stubFileService) RepairCheckout(_ context.Context, _ string, _ service.FileCheckoutRepairRequest) (*ops.RepairResult, error) {
	return &ops.RepairResult{}, nil
}
func (s *stubFileService) DiffFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _, _ string) (*service.FileDiffResult, error) {
	return &service.FileDiffResult{}, nil
}
func (s *stubFileService) BlameFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileBlameResult, error) {
	return &service.FileBlameResult{}, nil
}
func (s *stubFileService) HistoryFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) (*service.FileHistoryResult, error) {
	return &service.FileHistoryResult{}, nil
}
func (s *stubFileService) WriteFileScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _ string) error {
	return nil
}
func (s *stubFileService) WriteFileConditionalScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _ string, _ service.FileWritePreconditions) (*service.FileMutationResult, error) {
	return &service.FileMutationResult{Success: true}, nil
}
func (s *stubFileService) DeletePathScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string, _ bool) error {
	return nil
}
func (s *stubFileService) DeletePathVersionedScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string, _ bool, _ string) error {
	return nil
}
func (s *stubFileService) MkdirScoped(_ context.Context, _ string, _ service.FileScope, _, _, _ string) error {
	return nil
}
func (s *stubFileService) MovePathScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _ string, _ bool) error {
	return nil
}
func (s *stubFileService) MovePathVersionedScoped(_ context.Context, _ string, _ service.FileScope, _, _, _, _ string, _ bool, _, _ string) (*service.FileMutationResult, error) {
	return &service.FileMutationResult{Success: true}, nil
}

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
