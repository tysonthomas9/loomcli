package svcimpl

import (
	"context"
	"errors"
	"fmt"
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
	return s.ListDirectoryScoped(ctx, wsID, service.ScopeAgent, agentName, "", path)
}

// listDirectoryAt lists one level under rootDir. hidden names path segments to
// omit from the listing (e.g. ".git"); pass nil to list everything. Shared by
// the scope-rooted listers.
func listDirectoryAt(store *rootedFileStore, path string, hidden map[string]bool) (*service.FileTreeResult, error) {
	cleanPath, err := normalizeFilePath(path, true)
	if err != nil {
		return nil, err
	}
	dirEntries, err := store.List(cleanPath)
	if err != nil {
		return nil, err
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

	return &service.FileTreeResult{Path: cleanPath, Entries: entries}, nil
}

// convertDirEntries converts os.DirEntry items to service.FileTreeEntry,
// skipping symlinks and any entry whose name is in hidden (pass nil to keep all).
func convertDirEntries(dirEntries []os.DirEntry, hidden map[string]bool) []service.FileTreeEntry {
	entries := make([]service.FileTreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		if isHiddenEntry(de.Name(), hidden) {
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

func isHiddenEntry(name string, hidden map[string]bool) bool {
	for candidate := range hidden {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	return false
}

func (s *fileServiceImpl) ReadFile(ctx context.Context, wsID, agentName, path string) (*service.FileReadResult, error) {
	return s.ReadFileScoped(ctx, wsID, service.ScopeAgent, agentName, "", path)
}

// readFileAt reads a single file under rootDir.
func readFileAt(store *rootedFileStore, path string) (*service.FileReadResult, error) {
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return nil, err
	}
	data, fi, truncated, err := store.Read(cleanPath, maxRequestBody)
	if err != nil {
		return nil, err
	}

	if misc.IsBinaryContent(data) {
		return &service.FileReadResult{Path: cleanPath, Size: fi.Size(), Binary: true, Truncated: truncated}, nil
	}
	return &service.FileReadResult{Path: cleanPath, Content: string(data), Size: fi.Size(), Truncated: truncated}, nil
}

// hiddenScopeSegments names path segments omitted from workspace listings.
// The rooted store separately rejects explicit .git access for every operation.
var hiddenScopeSegments = map[string]bool{".git": true}

// resolveScopeRoot resolves a scope+target to the absolute root directory that
// scoped file operations are confined to.
func (s *fileServiceImpl) resolveScopeRoot(wsID string, scope service.FileScope, target, repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo != "" && scope != service.ScopeAgent {
		return "", service.ErrValidation("repo qualifier is only supported for agent scope")
	}
	switch scope {
	case service.ScopeWorkspace:
		return s.resolveWorkspaceScopeRoot(wsID, target)
	case service.ScopeRepo:
		return s.resolveRepoScopeRoot(wsID, target)
	case service.ScopeAgent:
		return s.resolveAgentScopeRoot(wsID, target, repo)
	default:
		return "", service.ErrValidation(fmt.Sprintf("unsupported scope %q", scope))
	}
}

func (s *fileServiceImpl) resolveScopedRoot(wsID string, scope service.FileScope, target, repo string) (*scopedRoot, error) {
	rootPath, err := s.resolveScopeRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	identity := strings.Join([]string{wsID, string(scope), target, strings.TrimSpace(repo)}, "\x00")
	root, err := openScopedRoot(identity, rootPath)
	if err != nil {
		return nil, err
	}
	root.workspaceID = wsID
	root.scope = scope
	root.target = target
	root.repo = strings.TrimSpace(repo)
	return root, nil
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

func (s *fileServiceImpl) resolveAgentScopeRoot(wsID, target, repo string) (string, error) {
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
	var wt *ops.AgentWorktree
	if repo != "" {
		wt, err = s.fileOps.ResolveAgentWorktreeForRepo(wsID, target, repo)
	} else {
		wt, err = s.fileOps.ResolveAgentWorktree(wsID, target)
	}
	if err != nil {
		if errors.Is(err, ops.ErrAgentRepoNotAllowed) {
			return "", service.ErrValidation(err.Error())
		}
		if errors.Is(err, ops.ErrAgentWorktreeNotFound) {
			return "", service.ErrNotFound(err.Error())
		}
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

func (s *fileServiceImpl) ListDirectoryScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileTreeResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return listDirectoryAt(root.store, path, hiddenScopeSegments)
}

func (s *fileServiceImpl) ReadFileScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileReadResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readFileAt(root.store, path)
}

func (s *fileServiceImpl) WriteFile(ctx context.Context, wsID, agentName, path, content string) error {
	return s.WriteFileScoped(ctx, wsID, service.ScopeAgent, agentName, "", path, content)
}

func (s *fileServiceImpl) WriteFileScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path, content string) error {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return err
	}
	if err := s.snapshotBeforeOverwrite(wsID, scope, target, repo, root.store, cleanPath); err != nil {
		return err
	}
	if err := root.store.WriteAtomic(cleanPath, []byte(content)); err != nil {
		return err
	}
	s.invalidateIndex(root.path)
	return nil
}

func (s *fileServiceImpl) DeletePathScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string, recursive bool) error {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return err
	}
	if err := root.store.Delete(cleanPath, recursive); err != nil {
		return err
	}
	s.invalidateIndex(root.path)
	return nil
}

func (s *fileServiceImpl) MkdirScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) error {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return err
	}
	if err := root.store.MkdirAll(cleanPath); err != nil {
		return err
	}
	s.invalidateIndex(root.path)
	return nil
}

func (s *fileServiceImpl) MovePathScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, from, to string, overwrite bool) error {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanFrom, err := normalizeFilePath(from, false)
	if err != nil {
		return err
	}
	cleanTo, err := normalizeFilePath(to, false)
	if err != nil {
		return err
	}
	if err := root.store.Move(cleanFrom, cleanTo, overwrite); err != nil {
		return err
	}
	s.invalidateIndex(root.path)
	return nil
}

func validateNoSymlinkComponents(rootDir, fullPath string) error {
	relPath, err := filepath.Rel(rootDir, fullPath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPath) {
		return service.ErrForbidden("path outside root")
	}
	cleanPath, err := normalizeFilePath(relPath, true)
	if err != nil {
		return err
	}
	root, err := openScopedRoot("validation", rootDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.store.ensureNoSymlinks(cleanPath, true)
}
