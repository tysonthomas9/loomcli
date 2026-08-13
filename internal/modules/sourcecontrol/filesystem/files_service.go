package filesystem

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sync/singleflight"
)

const maxRequestBody = 1 << 20

// fileServiceImpl is the concrete implementation of FileService.
type fileServiceImpl struct {
	fileOps       mechanics
	indexCache    *fileIndexCache
	indexBuilds   singleflight.Group
	indexBuilder  func(context.Context, string, string, bool) (*FileIndexResult, error)
	mutationLocks pathLockSet
}

func newFileService(fileOps mechanics) *fileServiceImpl {
	svc := &fileServiceImpl{
		fileOps:    fileOps,
		indexCache: newFileIndexCache(fileIndexCacheMaxEntries, fileIndexCacheMaxBytes, fileIndexCacheTTL),
	}
	svc.indexBuilder = svc.buildFileIndex
	return svc
}

// listDirectoryAt lists one level under rootDir. hidden names path segments to
// omit from the listing (e.g. ".git"); pass nil to list everything. Shared by
// the scope-rooted listers.
func listDirectoryAt(_ context.Context, store *rootedFileRoot, path string, hidden map[string]bool, access fileAccess) (*FileTreeResult, error) {
	cleanPath, err := normalizeFilePath(path, true)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(access, cleanPath); err != nil {
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

	entries := convertDirEntries(cleanPath, dirEntries, hidden, access)

	return &FileTreeResult{Path: cleanPath, Entries: entries}, nil
}

// convertDirEntries converts os.DirEntry items to FileTreeEntry,
// skipping symlinks and any entry whose name is in hidden (pass nil to keep all).
func convertDirEntries(parent string, dirEntries []os.DirEntry, hidden map[string]bool, access fileAccess) []FileTreeEntry {
	entries := make([]FileTreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		if isHiddenEntry(de.Name(), hidden) {
			continue
		}
		if !filePathAllowsSensitive(access, filepath.Join(parent, de.Name())) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, FileTreeEntry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
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

// readFileAt reads a single file under rootDir.
func readFileAt(_ context.Context, store *rootedFileRoot, path string, access fileAccess) (*FileReadResult, error) {
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(access, cleanPath); err != nil {
		return nil, err
	}
	data, fi, truncated, sum, err := store.ReadVersioned(cleanPath, maxRequestBody)
	if err != nil {
		return nil, err
	}

	if IsBinaryContent(data) {
		return &FileReadResult{Path: cleanPath, Size: fi.Size(), Binary: true, Truncated: truncated, Version: "sha256:" + hex.EncodeToString(sum)}, nil
	}
	return &FileReadResult{Path: cleanPath, Content: string(data), Size: fi.Size(), Truncated: truncated, Version: "sha256:" + hex.EncodeToString(sum)}, nil
}

// hiddenScopeSegments names path segments omitted from workspace listings.
// The rooted store separately rejects explicit .git access for every operation.
var hiddenScopeSegments = map[string]bool{".git": true}

// resolveScopeRoot resolves a scope+target to the absolute root directory that
// scoped file operations are confined to.
func (s *fileServiceImpl) resolveScopeRoot(wsID string, scope FileScope, target, repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo != "" && scope != ScopeAgent {
		return "", newInvalid("repo qualifier is only supported for agent scope")
	}
	switch scope {
	case ScopeWorkspace:
		return s.resolveWorkspaceScopeRoot(wsID, target)
	case ScopeRepo:
		return s.resolveRepoScopeRoot(wsID, target)
	case ScopeAgent:
		return s.resolveAgentScopeRoot(wsID, target, repo)
	default:
		return "", newInvalid(fmt.Sprintf("unsupported scope %q", scope))
	}
}

func (s *fileServiceImpl) resolveScopedRoot(wsID string, scope FileScope, target, repo string) (*scopedRoot, error) {
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
		return "", newInvalid("workspace scope does not take a target")
	}
	root, err := s.fileOps.ResolveWorkspaceRoot(wsID)
	if err != nil {
		return "", newNotFound(err.Error())
	}
	return root, nil
}

func (s *fileServiceImpl) resolveRepoScopeRoot(wsID, target string) (string, error) {
	if target == "" {
		return "", newInvalid("repo scope requires a target")
	}
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return "", err
	}
	repo, ok := repoTarget(ws, target)
	if !ok {
		return "", newNotFound(fmt.Sprintf("repo %q not found", target))
	}
	wsRoot, err := s.fileOps.ResolveWorkspaceRoot(wsID)
	if err != nil {
		return "", newNotFound(err.Error())
	}
	root := repoCheckoutPath(wsRoot, repo)
	if err := validatePathWithinDir(root, wsRoot); err != nil {
		return "", newForbidden("repo root outside workspace")
	}
	return root, nil
}

func repoTarget(ws *WorkspaceTopology, target string) (WorkspaceRepo, bool) {
	for _, repo := range ws.Repos {
		if repo.Name == target {
			return repo, true
		}
	}
	return WorkspaceRepo{}, false
}

func (s *fileServiceImpl) resolveAgentScopeRoot(wsID, target, repo string) (string, error) {
	if err := validateAgentName(target); err != nil {
		return "", err
	}
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return "", err
	}
	if len(ws.Agents) > 0 && !agentTargetExists(ws, target) {
		return "", newNotFound(fmt.Sprintf("agent %q not found", target))
	}
	var wt *Worktree
	if repo != "" {
		wt, err = s.fileOps.ResolveAgentWorktreeForRepo(wsID, target, repo)
	} else {
		wt, err = s.fileOps.ResolveAgentWorktree(wsID, target)
	}
	if err != nil {
		if errors.Is(err, ErrAgentRepoNotAllowed) {
			return "", newInvalid(err.Error())
		}
		if errors.Is(err, ErrAgentWorktreeNotFound) {
			return "", newNotFound(err.Error())
		}
		return "", newNotFound(fmt.Sprintf("agent worktree %q not found", target))
	}
	return wt.Path, nil
}

func agentTargetExists(ws *WorkspaceTopology, target string) bool {
	for _, agent := range ws.Agents {
		if agent.Name == target {
			return true
		}
	}
	return false
}

func (s *fileServiceImpl) resolveWorkspaceData(wsID string) (*WorkspaceTopology, error) {
	ws, err := s.fileOps.ResolveWorkspaceData(wsID)
	if err != nil {
		return nil, newNotFound(err.Error())
	}
	if ws == nil {
		return nil, newNotFound("workspace not found")
	}
	return ws, nil
}

func (s *fileServiceImpl) ListDirectoryScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileTreeResult, error) {
	return s.listDirectoryScoped(ctx, wsID, scope, target, repo, path, fileAccess{})
}

func (s *fileServiceImpl) listDirectoryScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string, access fileAccess) (*FileTreeResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return listDirectoryAt(ctx, root.store, path, hiddenScopeSegments, access)
}

func (s *fileServiceImpl) ReadFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileReadResult, error) {
	return s.readFileScoped(ctx, wsID, scope, target, repo, path, fileAccess{})
}

func (s *fileServiceImpl) readFileScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string, access fileAccess) (*FileReadResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readFileAt(ctx, root.store, path, access)
}

func (s *fileServiceImpl) StatPathScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) (*FileStatResult, error) {
	return s.statPathScoped(ctx, wsID, scope, target, repo, path, fileAccess{})
}

func (s *fileServiceImpl) statPathScoped(_ context.Context, wsID string, scope FileScope, target, repo, path string, access fileAccess) (*FileStatResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(access, cleanPath); err != nil {
		return nil, err
	}
	version, info, err := versionForPath(root.store, cleanPath)
	if err != nil {
		return nil, err
	}
	return &FileStatResult{
		Path: cleanPath, IsDir: info.IsDir(), Size: info.Size(),
		ModTime: info.ModTime().UTC(), Version: version,
	}, nil
}

func (s *fileServiceImpl) WriteFileConditionalScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path, content string, preconditions FileWritePreconditions) (*FileMutationResult, error) {
	return s.writeFileConditionalScoped(ctx, wsID, scope, target, repo, path, content, preconditions, fileAccess{})
}

func (s *fileServiceImpl) writeFileConditionalScoped(_ context.Context, wsID string, scope FileScope, target, repo, path, content string, preconditions FileWritePreconditions, access fileAccess) (*FileMutationResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(access, cleanPath); err != nil {
		return nil, err
	}
	if preconditions.IfMatch != "" && preconditions.IfNoneMatch {
		return nil, newInvalid("If-Match and If-None-Match cannot be combined")
	}
	unlock := s.mutationLocks.lock(root.identity, cleanPath)
	defer unlock()
	if preconditions.IfNoneMatch {
		if _, err := root.store.stat(cleanPath); err == nil {
			return nil, newPreconditionFailed("path already exists")
		} else if !isNotFound(err) {
			return nil, err
		}
	}
	if preconditions.IfMatch != "" {
		current, _, err := versionForPath(root.store, cleanPath)
		if err != nil {
			if isNotFound(err) {
				return nil, newPreconditionFailed("file version no longer matches")
			}
			return nil, err
		}
		if current != preconditions.IfMatch {
			return nil, newPreconditionFailed("file version no longer matches")
		}
	}
	if err := root.store.WriteAtomic(cleanPath, []byte(content)); err != nil {
		return nil, err
	}
	s.invalidateIndex(root.identity)
	version, _, err := versionForPath(root.store, cleanPath)
	if err != nil {
		return nil, err
	}
	return &FileMutationResult{Success: true, Version: version}, nil
}

func (s *fileServiceImpl) DeletePathVersionedScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string, recursive bool, version string) error {
	return s.deletePathVersionedScoped(ctx, wsID, scope, target, repo, path, recursive, version, fileAccess{})
}

func (s *fileServiceImpl) deletePathVersionedScoped(_ context.Context, wsID string, scope FileScope, target, repo, path string, recursive bool, version string, access fileAccess) error {
	if strings.TrimSpace(version) == "" {
		return newPreconditionRequired("current source version is required")
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
	if err := requireSensitiveFileAccess(access, cleanPath); err != nil {
		return err
	}
	unlock := s.mutationLocks.lock(root.identity, cleanPath)
	defer unlock()
	current, _, err := versionForPath(root.store, cleanPath)
	if err != nil {
		return err
	}
	if current != version {
		return newPreconditionFailed("source version no longer matches")
	}
	if err := root.store.Delete(cleanPath, recursive); err != nil {
		return err
	}
	s.invalidateIndex(root.identity)
	return nil
}

func (s *fileServiceImpl) MkdirScoped(ctx context.Context, wsID string, scope FileScope, target, repo, path string) error {
	return s.mkdirScoped(ctx, wsID, scope, target, repo, path, fileAccess{})
}

func (s *fileServiceImpl) mkdirScoped(_ context.Context, wsID string, scope FileScope, target, repo, path string, access fileAccess) error {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return err
	}
	defer root.Close()
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		return err
	}
	if err := requireSensitiveFileAccess(access, cleanPath); err != nil {
		return err
	}
	unlock := s.mutationLocks.lock(root.identity, cleanPath)
	defer unlock()
	if err := root.store.MkdirAll(cleanPath); err != nil {
		return err
	}
	s.invalidateIndex(root.identity)
	return nil
}

func (s *fileServiceImpl) MovePathVersionedScoped(ctx context.Context, wsID string, scope FileScope, target, repo, from, to string, overwrite bool, sourceVersion, destinationVersion string) (*FileMutationResult, error) {
	return s.movePathVersionedScoped(ctx, wsID, scope, target, repo, from, to, overwrite, sourceVersion, destinationVersion, fileAccess{})
}

func (s *fileServiceImpl) movePathVersionedScoped(_ context.Context, wsID string, scope FileScope, target, repo, from, to string, overwrite bool, sourceVersion, destinationVersion string, access fileAccess) (*FileMutationResult, error) {
	if strings.TrimSpace(sourceVersion) == "" {
		return nil, newPreconditionRequired("current source version is required")
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
	if err := requireSensitiveFileAccess(access, cleanFrom); err != nil {
		return nil, err
	}
	cleanTo, err := normalizeFilePath(to, false)
	if err != nil {
		return nil, err
	}
	if err := requireSensitiveFileAccess(access, cleanTo); err != nil {
		return nil, err
	}
	unlock := s.mutationLocks.lock(root.identity, cleanFrom, cleanTo)
	defer unlock()
	currentSource, _, err := versionForPath(root.store, cleanFrom)
	if err != nil {
		return nil, err
	}
	if currentSource != sourceVersion {
		return nil, newPreconditionFailed("source version no longer matches")
	}
	destinationInfo, destinationErr := root.store.stat(cleanTo)
	if err := validateMoveDestinationVersion(root.store, cleanTo, overwrite, destinationVersion, destinationInfo, destinationErr); err != nil {
		return nil, err
	}
	if err := root.store.Move(cleanFrom, cleanTo, overwrite); err != nil {
		return nil, err
	}
	s.invalidateIndex(root.identity)
	version, _, err := versionForPath(root.store, cleanTo)
	if err != nil {
		return nil, err
	}
	return &FileMutationResult{Success: true, Version: version}, nil
}

func validateMoveDestinationVersion(store *rootedFileRoot, path string, overwrite bool, version string, info os.FileInfo, statErr error) error {
	if isNotFound(statErr) {
		if version != "" {
			return newPreconditionFailed("destination no longer exists")
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
		return newPreconditionRequired("current destination version is required when overwriting a file")
	}
	current, _, err := versionForPath(store, path)
	if err != nil {
		return err
	}
	if current != version {
		return newPreconditionFailed("destination version no longer matches")
	}
	return nil
}

func requireSensitiveFileAccess(access fileAccess, path string) error {
	if !filePathAllowsSensitive(access, path) {
		return newForbidden("sensitive file access denied")
	}
	return nil
}

func validateNoSymlinkComponents(rootDir, fullPath string) error {
	relPath, err := filepath.Rel(rootDir, fullPath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPath) {
		return newForbidden("path outside root")
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
