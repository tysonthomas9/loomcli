package svcimpl

import (
	"context"
	"encoding/hex"
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
	fileOps       ops.FileOps
	indexCacheMu  sync.Mutex
	indexCache    map[string]fileIndexCacheEntry
	mutationLocks pathLockSet
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
func listDirectoryAt(ctx context.Context, store *rootedFileStore, path string, hidden map[string]bool) (*service.FileTreeResult, error) {
	cleanPath, err := normalizeFilePath(path, true)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(ctx, cleanPath); err != nil {
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

	entries := convertDirEntries(ctx, cleanPath, dirEntries, hidden)

	return &service.FileTreeResult{Path: cleanPath, Entries: entries}, nil
}

// convertDirEntries converts os.DirEntry items to service.FileTreeEntry,
// skipping symlinks and any entry whose name is in hidden (pass nil to keep all).
func convertDirEntries(ctx context.Context, parent string, dirEntries []os.DirEntry, hidden map[string]bool) []service.FileTreeEntry {
	entries := make([]service.FileTreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		if isHiddenEntry(de.Name(), hidden) {
			continue
		}
		if !service.FilePathAllowsSensitive(ctx, filepath.Join(parent, de.Name())) {
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
func readFileAt(ctx context.Context, store *rootedFileStore, path string) (*service.FileReadResult, error) {
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(ctx, cleanPath); err != nil {
		return nil, err
	}
	data, fi, truncated, sum, err := store.ReadVersioned(cleanPath, maxRequestBody)
	if err != nil {
		return nil, err
	}

	if misc.IsBinaryContent(data) {
		return &service.FileReadResult{Path: cleanPath, Size: fi.Size(), Binary: true, Truncated: truncated, Version: "sha256:" + hex.EncodeToString(sum)}, nil
	}
	return &service.FileReadResult{Path: cleanPath, Content: string(data), Size: fi.Size(), Truncated: truncated, Version: "sha256:" + hex.EncodeToString(sum)}, nil
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
	root, err := openScopedRoot(rootPath)
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

func (s *fileServiceImpl) ListDirectoryScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileTreeResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return listDirectoryAt(ctx, root.store, path, hiddenScopeSegments)
}

func (s *fileServiceImpl) ReadFileScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileReadResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readFileAt(ctx, root.store, path)
}

func (s *fileServiceImpl) StatPathScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileStatResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(ctx, cleanPath); err != nil {
		return nil, err
	}
	version, info, err := versionForPath(root.store, cleanPath)
	if err != nil {
		return nil, err
	}
	return &service.FileStatResult{
		Path: cleanPath, IsDir: info.IsDir(), Size: info.Size(),
		ModTime: info.ModTime().UTC().Format(time.RFC3339), Version: version,
	}, nil
}

func (s *fileServiceImpl) WriteFile(ctx context.Context, wsID, agentName, path, content string) error {
	return s.WriteFileScoped(ctx, wsID, service.ScopeAgent, agentName, "", path, content)
}

func (s *fileServiceImpl) WriteFileScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path, content string) error {
	_, err := s.WriteFileConditionalScoped(ctx, wsID, scope, target, repo, path, content, service.FileWritePreconditions{})
	return err
}

func (s *fileServiceImpl) WriteFileConditionalScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path, content string, preconditions service.FileWritePreconditions) (*service.FileMutationResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(ctx, cleanPath); err != nil {
		return nil, err
	}
	if preconditions.IfMatch != "" && preconditions.IfNoneMatch {
		return nil, service.ErrValidation("If-Match and If-None-Match cannot be combined")
	}
	unlock := s.mutationLocks.lock(root.identity, cleanPath)
	defer unlock()
	if preconditions.IfNoneMatch {
		if _, err := root.store.stat(cleanPath); err == nil {
			return nil, service.ErrPreconditionFailed("path already exists")
		} else if !isNotFound(err) {
			return nil, err
		}
	}
	if preconditions.IfMatch != "" {
		current, _, err := versionForPath(root.store, cleanPath)
		if err != nil {
			if isNotFound(err) {
				return nil, service.ErrPreconditionFailed("file version no longer matches")
			}
			return nil, err
		}
		if current != preconditions.IfMatch {
			return nil, service.ErrPreconditionFailed("file version no longer matches")
		}
	}
	if err := s.snapshotBeforeOverwrite(wsID, scope, target, repo, root.store, cleanPath); err != nil {
		return nil, err
	}
	if err := root.store.WriteAtomic(cleanPath, []byte(content)); err != nil {
		return nil, err
	}
	s.invalidateIndex(root.path)
	version, _, err := versionForPath(root.store, cleanPath)
	if err != nil {
		return nil, err
	}
	return &service.FileMutationResult{Success: true, Version: version}, nil
}

func (s *fileServiceImpl) DeletePathScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string, recursive bool) error {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return err
	}
	if err := requireSensitiveFileAccess(ctx, cleanPath); err != nil {
		return err
	}
	if _, _, err := versionForPath(root.store, cleanPath); err != nil {
		return err
	}
	return service.ErrPreconditionRequired("current source version is required")
}

func (s *fileServiceImpl) DeletePathVersionedScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string, recursive bool, version string) error {
	if strings.TrimSpace(version) == "" {
		return service.ErrPreconditionRequired("current source version is required")
	}
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return err
	}
	if err := requireSensitiveFileAccess(ctx, cleanPath); err != nil {
		return err
	}
	unlock := s.mutationLocks.lock(root.identity, cleanPath)
	defer unlock()
	current, _, err := versionForPath(root.store, cleanPath)
	if err != nil {
		return err
	}
	if current != version {
		return service.ErrPreconditionFailed("source version no longer matches")
	}
	if err := root.store.Delete(cleanPath, recursive); err != nil {
		return err
	}
	s.invalidateIndex(root.path)
	return nil
}

func (s *fileServiceImpl) MkdirScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string) error {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return err
	}
	if err := requireSensitiveFileAccess(ctx, cleanPath); err != nil {
		return err
	}
	unlock := s.mutationLocks.lock(root.identity, cleanPath)
	defer unlock()
	if err := root.store.MkdirAll(cleanPath); err != nil {
		return err
	}
	s.invalidateIndex(root.path)
	return nil
}

func (s *fileServiceImpl) MovePathScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, from, to string, overwrite bool) error {
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
	if err := requireSensitiveFileAccess(ctx, cleanFrom); err != nil {
		return err
	}
	if err := requireSensitiveFileAccess(ctx, cleanTo); err != nil {
		return err
	}
	if _, _, err := versionForPath(root.store, cleanFrom); err != nil {
		return err
	}
	return service.ErrPreconditionRequired("current source version is required")
}

func (s *fileServiceImpl) MovePathVersionedScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, from, to string, overwrite bool, sourceVersion, destinationVersion string) (*service.FileMutationResult, error) {
	if strings.TrimSpace(sourceVersion) == "" {
		return nil, service.ErrPreconditionRequired("current source version is required")
	}
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cleanFrom, err := normalizeFilePath(from, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(ctx, cleanFrom); err != nil {
		return nil, err
	}
	cleanTo, err := normalizeFilePath(to, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(ctx, cleanTo); err != nil {
		return nil, err
	}
	unlock := s.mutationLocks.lock(root.identity, cleanFrom, cleanTo)
	defer unlock()
	currentSource, _, err := versionForPath(root.store, cleanFrom)
	if err != nil {
		return nil, err
	}
	if currentSource != sourceVersion {
		return nil, service.ErrPreconditionFailed("source version no longer matches")
	}
	destinationInfo, destinationErr := root.store.stat(cleanTo)
	if err := validateMoveDestinationVersion(root.store, cleanTo, overwrite, destinationVersion, destinationInfo, destinationErr); err != nil {
		return nil, err
	}
	if err := root.store.Move(cleanFrom, cleanTo, overwrite); err != nil {
		return nil, err
	}
	s.invalidateIndex(root.path)
	version, _, err := versionForPath(root.store, cleanTo)
	if err != nil {
		return nil, err
	}
	return &service.FileMutationResult{Success: true, Version: version}, nil
}

func validateMoveDestinationVersion(store *rootedFileStore, path string, overwrite bool, version string, info os.FileInfo, statErr error) error {
	if isNotFound(statErr) {
		if version != "" {
			return service.ErrPreconditionFailed("destination no longer exists")
		}
		return nil
	}
	if statErr != nil {
		return statErr
	}
	if !overwrite || !info.Mode().IsRegular() {
		return nil
	}
	if strings.TrimSpace(version) == "" {
		return service.ErrPreconditionRequired("current destination version is required when overwriting a file")
	}
	current, _, err := versionForPath(store, path)
	if err != nil {
		return err
	}
	if current != version {
		return service.ErrPreconditionFailed("destination version no longer matches")
	}
	return nil
}

func requireSensitiveFileAccess(ctx context.Context, path string) error {
	if !service.FilePathAllowsSensitive(ctx, path) {
		return service.ErrForbidden("sensitive file access denied")
	}
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
	root, err := openScopedRoot(rootDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.store.ensureNoSymlinks(cleanPath, true)
}
