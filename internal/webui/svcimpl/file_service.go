package svcimpl

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Compile-time check.
var _ service.FileService = (*fileServiceImpl)(nil)

// fileServiceImpl is the concrete implementation of FileService.
type fileServiceImpl struct {
	fileOps      ops.FileOps
	indexCacheMu sync.Mutex
	indexCache   map[string]fileIndexCacheEntry
}

// NewFileService creates a new FileService implementation.
func NewFileService(fileOps ops.FileOps) service.FileService {
	return &fileServiceImpl{
		fileOps:    fileOps,
		indexCache: make(map[string]fileIndexCacheEntry),
	}
}

func (s *fileServiceImpl) ListDirectory(ctx context.Context, wsID, agentName, path string) (*service.FileTreeResult, error) {
	return s.ListDirectoryScoped(ctx, wsID, service.ScopeAgent, agentName, path)
}

// listDirectoryAt lists one level under rootDir. hidden names path segments to
// omit from the listing (e.g. ".git"); pass nil to list everything. Shared by
// the scope-rooted listers.
func listDirectoryAt(rootDir, path string, hidden map[string]bool) (*service.FileTreeResult, error) {
	_, fullPath, err := scopedFullPath(rootDir, path, true)
	if err != nil {
		return nil, err
	}
	if err := validateNoSymlinkComponents(rootDir, fullPath); err != nil {
		return nil, err
	}
	if err := validateDirPath(fullPath); err != nil {
		return nil, err
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

	entries := convertDirEntries(dirEntries, hidden)

	relPath, _ := filepath.Rel(rootDir, fullPath)
	if relPath == "" {
		relPath = "."
	}

	return &service.FileTreeResult{Path: relPath, Entries: entries}, nil
}

// validateDirPath checks that fullPath is a real directory (not a symlink).
func validateDirPath(fullPath string) error {
	fi, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return service.ErrNotFound("directory not found")
		}
		return service.ErrInternal("failed to stat path", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("refusing to follow symlink")
	}
	if !fi.IsDir() {
		return service.ErrValidation("path is not a directory")
	}
	return nil
}

// convertDirEntries converts os.DirEntry items to service.FileTreeEntry,
// skipping symlinks and any entry whose name is in hidden (pass nil to keep all).
func convertDirEntries(dirEntries []os.DirEntry, hidden map[string]bool) []service.FileTreeEntry {
	entries := make([]service.FileTreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		if hidden[de.Name()] {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, service.FileTreeEntry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return entries
}

func (s *fileServiceImpl) ReadFile(ctx context.Context, wsID, agentName, path string) (*service.FileReadResult, error) {
	return s.ReadFileScoped(ctx, wsID, service.ScopeAgent, agentName, path)
}

// readFileAt reads a single file under rootDir.
func readFileAt(rootDir, path string) (*service.FileReadResult, error) {
	cleanPath, fullPath, err := scopedFullPath(rootDir, path, false)
	if err != nil {
		return nil, err
	}
	if err := validateNoSymlinkComponents(rootDir, fullPath); err != nil {
		return nil, err
	}
	fi, err := validateFilePath(fullPath)
	if err != nil {
		return nil, err
	}

	data, truncated, err := readFileContent(fullPath, rootDir, fi.Size())
	if err != nil {
		return nil, err
	}

	if misc.IsBinaryContent(data) {
		return &service.FileReadResult{Path: cleanPath, Size: fi.Size(), Binary: true, Truncated: truncated}, nil
	}
	return &service.FileReadResult{Path: cleanPath, Content: string(data), Size: fi.Size(), Truncated: truncated}, nil
}

// hiddenScopeSegments names path segments the workspace browser omits from
// listings. This is rendering only: explicit reads/writes/CRUD on .git paths
// are allowed by the shared scoped core.
var hiddenScopeSegments = map[string]bool{".git": true}

// resolveScopeRoot resolves a scope+target to the absolute root directory that
// scoped file operations are confined to.
func (s *fileServiceImpl) resolveScopeRoot(wsID string, scope service.FileScope, target string) (string, error) {
	switch scope {
	case service.ScopeWorkspace:
		return s.resolveWorkspaceScopeRoot(wsID, target)
	case service.ScopeRepo:
		return s.resolveRepoScopeRoot(wsID, target)
	case service.ScopeAgent:
		return s.resolveAgentScopeRoot(wsID, target)
	default:
		return "", service.ErrValidation(fmt.Sprintf("unsupported scope %q", scope))
	}
}

func (s *fileServiceImpl) resolveWorkspaceScopeRoot(wsID, target string) (string, error) {
	if target != "" {
		return "", service.ErrValidation("workspace scope does not take a target")
	}
	root, err := s.fileOps.ResolveWorkspaceRoot(wsID)
	if err != nil {
		return "", service.ErrNotFound(err.Error())
	}
	return root, nil
}

func (s *fileServiceImpl) resolveRepoScopeRoot(wsID, target string) (string, error) {
	if target == "" {
		return "", service.ErrValidation("repo scope requires a target")
	}
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return "", err
	}
	repoName := repoTargetName(ws, target)
	if repoName == "" {
		return "", service.ErrNotFound(fmt.Sprintf("repo %q not found", target))
	}
	wsRoot, err := s.fileOps.ResolveWorkspaceRoot(wsID)
	if err != nil {
		return "", service.ErrNotFound(err.Error())
	}
	root := filepath.Join(wsRoot, repoName)
	if err := webuilog.ValidatePathWithinDir(root, wsRoot); err != nil {
		return "", service.ErrForbidden("repo root outside workspace")
	}
	return root, nil
}

func repoTargetName(ws *ops.WorkspaceData, target string) string {
	for _, repo := range ws.Repos {
		if repo.Name == target {
			return repo.Name
		}
	}
	return ""
}

func (s *fileServiceImpl) resolveAgentScopeRoot(wsID, target string) (string, error) {
	if err := validateAgentName(target); err != nil {
		return "", err
	}
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return "", err
	}
	if !agentTargetExists(ws, target) {
		return "", service.ErrNotFound(fmt.Sprintf("agent %q not found", target))
	}
	wt, err := s.fileOps.ResolveAgentWorktree(wsID, target)
	if err != nil {
		return "", service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", target))
	}
	return wt.Path, nil
}

func agentTargetExists(ws *ops.WorkspaceData, target string) bool {
	for _, agent := range ws.Agents {
		if agent.Name == target {
			return true
		}
	}
	return false
}

func (s *fileServiceImpl) resolveWorkspaceData(wsID string) (*ops.WorkspaceData, error) {
	ws, err := s.fileOps.ResolveWorkspaceData(wsID)
	if err != nil {
		return nil, service.ErrNotFound(err.Error())
	}
	if ws == nil {
		return nil, service.ErrNotFound("workspace not found")
	}
	return ws, nil
}

func (s *fileServiceImpl) ListDirectoryScoped(_ context.Context, wsID string, scope service.FileScope, target, path string) (*service.FileTreeResult, error) {
	root, err := s.resolveScopeRoot(wsID, scope, target)
	if err != nil {
		return nil, err
	}
	return listDirectoryAt(root, path, hiddenScopeSegments)
}

func (s *fileServiceImpl) ReadFileScoped(_ context.Context, wsID string, scope service.FileScope, target, path string) (*service.FileReadResult, error) {
	root, err := s.resolveScopeRoot(wsID, scope, target)
	if err != nil {
		return nil, err
	}
	return readFileAt(root, path)
}

func (s *fileServiceImpl) WriteFile(ctx context.Context, wsID, agentName, path, content string) error {
	return s.WriteFileScoped(ctx, wsID, service.ScopeAgent, agentName, path, content)
}

func (s *fileServiceImpl) WriteFileScoped(_ context.Context, wsID string, scope service.FileScope, target, path, content string) error {
	root, err := s.resolveScopeRoot(wsID, scope, target)
	if err != nil {
		return err
	}
	if err := s.snapshotBeforeOverwrite(wsID, scope, target, root, path); err != nil {
		return err
	}
	if err := writeFileAt(root, path, content); err != nil {
		return err
	}
	s.invalidateIndex(root)
	return nil
}

func (s *fileServiceImpl) DeletePathScoped(_ context.Context, wsID string, scope service.FileScope, target, path string, recursive bool) error {
	root, err := s.resolveScopeRoot(wsID, scope, target)
	if err != nil {
		return err
	}
	if err := deletePathAt(root, path, recursive); err != nil {
		return err
	}
	s.invalidateIndex(root)
	return nil
}

func (s *fileServiceImpl) MkdirScoped(_ context.Context, wsID string, scope service.FileScope, target, path string) error {
	root, err := s.resolveScopeRoot(wsID, scope, target)
	if err != nil {
		return err
	}
	if err := mkdirAt(root, path); err != nil {
		return err
	}
	s.invalidateIndex(root)
	return nil
}

func (s *fileServiceImpl) MovePathScoped(_ context.Context, wsID string, scope service.FileScope, target, from, to string, overwrite bool) error {
	root, err := s.resolveScopeRoot(wsID, scope, target)
	if err != nil {
		return err
	}
	if err := movePathAt(root, from, to, overwrite); err != nil {
		return err
	}
	s.invalidateIndex(root)
	return nil
}

func writeFileAt(rootDir, path, content string) error {
	_, fullPath, err := scopedFullPath(rootDir, path, false)
	if err != nil {
		return err
	}
	if err := validateNoSymlinkComponents(rootDir, fullPath); err != nil {
		return err
	}
	if err := validateExistingParent(fullPath, rootDir); err != nil {
		return err
	}
	perm, err := resolveWritePermissions(fullPath)
	if err != nil {
		return err
	}
	if err := misc.AtomicWriteFile(fullPath, content, perm); err != nil {
		return service.ErrInternal("failed to save file", err)
	}
	return nil
}

func deletePathAt(rootDir, path string, recursive bool) error {
	_, fullPath, err := scopedFullPath(rootDir, path, false)
	if err != nil {
		return err
	}
	if err := validateNoSymlinkComponents(rootDir, fullPath); err != nil {
		return err
	}
	if err := validateExistingParent(fullPath, rootDir); err != nil {
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
		if os.IsNotExist(err) {
			return service.ErrNotFound("path not found")
		}
		return service.ErrInternal("failed to delete path", err)
	}
	return nil
}

func mkdirAt(rootDir, path string) error {
	_, fullPath, err := scopedFullPath(rootDir, path, false)
	if err != nil {
		return err
	}
	if err := validateNoSymlinkComponents(rootDir, fullPath); err != nil {
		return err
	}
	fi, err := os.Lstat(fullPath)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return service.ErrForbidden("refusing to follow symlink")
		}
		if !fi.IsDir() {
			return service.ErrConflict("file exists at path")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return service.ErrInternal("failed to stat path", err)
	}
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return service.ErrInternal("failed to create directory", err)
	}
	return nil
}

func movePathAt(rootDir, from, to string, overwrite bool) error {
	_, fromPath, err := scopedFullPath(rootDir, from, false)
	if err != nil {
		return err
	}
	_, toPath, err := scopedFullPath(rootDir, to, false)
	if err != nil {
		return err
	}
	if err := validateNoSymlinkComponents(rootDir, fromPath); err != nil {
		return err
	}
	if err := validateNoSymlinkComponents(rootDir, toPath); err != nil {
		return err
	}
	if err := validateExistingParent(fromPath, rootDir); err != nil {
		return err
	}
	if err := validateExistingParent(toPath, rootDir); err != nil {
		return err
	}
	fi, err := os.Lstat(fromPath)
	if err != nil {
		if os.IsNotExist(err) {
			return service.ErrNotFound("source path not found")
		}
		return service.ErrInternal("failed to stat source path", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
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
		if os.IsNotExist(err) {
			return service.ErrNotFound("path not found")
		}
		if os.IsExist(err) {
			return service.ErrConflict("destination exists")
		}
		return service.ErrInternal("failed to move path", err)
	}
	return nil
}

func resolveWritePermissions(fullPath string) (os.FileMode, error) {
	fi, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0644, nil
		}
		return 0, service.ErrInternal("failed to stat file", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return 0, service.ErrForbidden("refusing to overwrite symlink")
	}
	if fi.IsDir() {
		return 0, service.ErrValidation("path is a directory, not a file")
	}
	if !fi.Mode().IsRegular() {
		return 0, service.ErrValidation("path is not a regular file")
	}
	return fi.Mode().Perm(), nil
}

func scopedFullPath(rootDir, path string, allowEmpty bool) (string, string, error) {
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
	fullPath := filepath.Join(rootDir, cleanPath)
	if err := webuilog.ValidatePathWithinDir(fullPath, rootDir); err != nil {
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

func validateNoSymlinkComponents(rootDir, fullPath string) error {
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

func validateExistingParent(fullPath, rootDir string) error {
	parentDir := filepath.Dir(fullPath)
	if err := webuilog.ValidatePathWithinDir(parentDir, rootDir); err != nil {
		return service.ErrForbidden("parent directory outside root")
	}
	if err := validateNoSymlinkComponents(rootDir, parentDir); err != nil {
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

// validateFilePath checks that fullPath is a regular file.
func validateFilePath(fullPath string) (os.FileInfo, error) {
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
	if !fi.Mode().IsRegular() {
		// Sockets, fifos, devices, etc. — reject as a 4xx instead of failing
		// the open with a 500.
		return nil, service.ErrValidation("path is not a regular file")
	}
	return fi, nil
}

// readFileContent opens and reads the file content securely.
func readFileContent(fullPath, basePath string, size int64) ([]byte, bool, error) {
	f, err := misc.OpenLogFileSecure(fullPath, basePath)
	if err != nil {
		if strings.Contains(err.Error(), "symlink") {
			return nil, false, service.ErrForbidden("refusing to follow symlink")
		}
		return nil, false, service.ErrInternal("failed to open file", err)
	}
	defer f.Close()

	truncated := size > maxRequestBody
	data, err := io.ReadAll(io.LimitReader(f, maxRequestBody))
	if err != nil {
		return nil, false, service.ErrInternal("failed to read file", err)
	}
	return data, truncated, nil
}
