package svcimpl

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	fileOps ops.FileOps
}

// NewFileService creates a new FileService implementation.
func NewFileService(fileOps ops.FileOps) service.FileService {
	return &fileServiceImpl{fileOps: fileOps}
}

func (s *fileServiceImpl) resolveAgent(wsID, agentName string) (*ops.AgentWorktree, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	// List/Read use the lead-aware resolver so a lead agent (which has no
	// local worktree) falls back to the workspace primary worktree and the
	// file viewer works instead of 404ing.
	wt, err := s.fileOps.ResolveAgentWorktreeOrPrimary(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

// resolveAgentForWrite resolves the agent's own worktree for write operations.
// Unlike resolveAgent, this does NOT fall back to the workspace primary
// worktree for leads — writes must target the agent worktree only, so a lead
// can never mutate the primary repo from the read-only file viewer.
func (s *fileServiceImpl) resolveAgentForWrite(wsID, agentName string) (*ops.AgentWorktree, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	wt, err := s.fileOps.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

func (s *fileServiceImpl) ListDirectory(_ context.Context, wsID, agentName, path string) (*service.FileTreeResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}
	return listDirectoryAt(wt.Path, path, nil)
}

// listDirectoryAt lists one level under rootDir. hidden names path segments to
// omit from the listing and refuse to descend into (e.g. ".git"); pass nil to
// list everything. Shared by the agent-scoped and workspace-scoped listers.
func listDirectoryAt(rootDir, path string, hidden map[string]bool) (*service.FileTreeResult, error) {
	if path == "" {
		path = "."
	}
	if pathHasHiddenSegment(path, hidden) {
		return nil, service.ErrForbidden("access to this path is denied")
	}

	fullPath := filepath.Join(rootDir, filepath.Clean("/"+path))

	if err := webuilog.ValidatePathWithinDir(fullPath, rootDir); err != nil {
		return nil, service.ErrForbidden("path outside root")
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

func (s *fileServiceImpl) ReadFile(_ context.Context, wsID, agentName, path string) (*service.FileReadResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}
	return readFileAt(wt.Path, path, nil)
}

// readFileAt reads a single file under rootDir. hidden names path segments that
// are refused (e.g. ".git"); pass nil to allow all. Shared by the agent-scoped
// and workspace-scoped readers.
func readFileAt(rootDir, path string, hidden map[string]bool) (*service.FileReadResult, error) {
	if path == "" {
		return nil, service.ErrValidation("path parameter is required")
	}
	if isDeniedPath(path) {
		return nil, service.ErrForbidden("access to this file type is denied")
	}
	if pathHasHiddenSegment(path, hidden) {
		return nil, service.ErrForbidden("access to this path is denied")
	}

	fullPath := filepath.Join(rootDir, filepath.Clean("/"+path))

	if err := webuilog.ValidatePathWithinDir(fullPath, rootDir); err != nil {
		return nil, service.ErrForbidden("path outside root")
	}
	if isDeniedPath(fullPath) {
		return nil, service.ErrForbidden("access to this file type is denied")
	}

	fi, err := validateFilePath(fullPath)
	if err != nil {
		return nil, err
	}

	data, err := readFileContent(fullPath, rootDir)
	if err != nil {
		return nil, err
	}

	if misc.IsBinaryContent(data) {
		return &service.FileReadResult{Path: path, Size: fi.Size(), Binary: true}, nil
	}
	return &service.FileReadResult{Path: path, Content: string(data), Size: fi.Size()}, nil
}

// hiddenScopeSegments names path segments the workspace browser omits from
// listings and refuses to read. The workspace root spans every repo, so this
// keeps each repo's .git and Loom's workspace metadata out of the cross-repo
// view. This is the workspace-scope policy, not a global
// guarantee — the per-agent file panel passes nil and applies none. A set so
// more segments (e.g. node_modules) can be added without touching call sites.
var hiddenScopeSegments = map[string]bool{".git": true, ".loom": true}

// pathHasHiddenSegment reports whether any cleaned segment of path is hidden.
func pathHasHiddenSegment(path string, hidden map[string]bool) bool {
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Clean("/"+path)), "/") {
		if seg != "" && hidden[seg] {
			return true
		}
	}
	return false
}

// resolveScopeRoot resolves a scope+target to the absolute root directory that
// scoped file operations are confined to. Phase 1 supports only the read-only
// workspace scope; repo/agent scopes (and a per-scope writable decision for
// future write operations) slot into this switch without changing call sites.
func (s *fileServiceImpl) resolveScopeRoot(wsID string, scope service.FileScope, target string) (string, error) {
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
	default:
		return "", service.ErrValidation(fmt.Sprintf("unsupported scope %q", scope))
	}
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
	return readFileAt(root, path, hiddenScopeSegments)
}

// validateFilePath checks that fullPath is a regular file within size limits.
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
	if fi.Size() > maxRequestBody {
		return nil, service.ErrPayloadTooLarge(fmt.Sprintf("file too large: %d bytes (max %d)", fi.Size(), maxRequestBody))
	}
	return fi, nil
}

// readFileContent opens and reads the file content securely.
func readFileContent(fullPath, basePath string) ([]byte, error) {
	f, err := misc.OpenLogFileSecure(fullPath, basePath)
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
	return data, nil
}

func (s *fileServiceImpl) WriteFile(_ context.Context, wsID, agentName, path, content string) error {
	wt, err := s.resolveAgentForWrite(wsID, agentName)
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

	if err := webuilog.ValidatePathWithinDir(fullPath, wt.Path); err != nil {
		return service.ErrForbidden("path outside worktree")
	}
	if isDeniedPath(fullPath) {
		return service.ErrForbidden("access to this file type is denied")
	}

	if writeErr := misc.ValidateParentDir(fullPath, wt.Path); writeErr != nil {
		// Map fileWriteError to service errors
		switch writeErr.Message {
		case "parent directory does not exist":
			return service.ErrNotFound(writeErr.Message)
		case "parent directory is a symlink", "parent directory outside worktree":
			return service.ErrForbidden(writeErr.Message)
		default:
			return service.ErrInternal(writeErr.Message, nil)
		}
	}

	perm, writeErr := misc.ResolveWritePermissions(fullPath)
	if writeErr != nil {
		return service.ErrForbidden(writeErr.Message)
	}

	if err := misc.AtomicWriteFile(fullPath, content, perm); err != nil {
		return service.ErrInternal("failed to save file", err)
	}
	return nil
}
