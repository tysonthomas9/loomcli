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
	"github.com/tysonthomas9/loomcli/internal/webui/service/pathsec"
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

	if path == "" {
		path = "."
	}

	fullPath := filepath.Join(wt.Path, filepath.Clean("/"+path))

	if err := webuilog.ValidatePathWithinDir(fullPath, wt.Path); err != nil {
		return nil, service.ErrForbidden("path outside worktree")
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

	entries := convertDirEntries(dirEntries)

	relPath, _ := filepath.Rel(wt.Path, fullPath)
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

// convertDirEntries converts os.DirEntry items to service.FileTreeEntry, skipping symlinks.
func convertDirEntries(dirEntries []os.DirEntry) []service.FileTreeEntry {
	entries := make([]service.FileTreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Type()&os.ModeSymlink != 0 {
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

	if path == "" {
		return nil, service.ErrValidation("path parameter is required")
	}
	if pathsec.IsDeniedPath(path) {
		return nil, service.ErrForbidden("access to this file type is denied")
	}

	fullPath := filepath.Join(wt.Path, filepath.Clean("/"+path))

	if err := webuilog.ValidatePathWithinDir(fullPath, wt.Path); err != nil {
		return nil, service.ErrForbidden("path outside worktree")
	}
	if pathsec.IsDeniedPath(fullPath) {
		return nil, service.ErrForbidden("access to this file type is denied")
	}

	fi, err := validateFilePath(fullPath)
	if err != nil {
		return nil, err
	}

	data, err := readFileContent(fullPath, wt.Path)
	if err != nil {
		return nil, err
	}

	if misc.IsBinaryContent(data) {
		return &service.FileReadResult{Path: path, Size: fi.Size(), Binary: true}, nil
	}
	return &service.FileReadResult{Path: path, Content: string(data), Size: fi.Size()}, nil
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
	if pathsec.IsDeniedPath(path) {
		return service.ErrForbidden("access to this file type is denied")
	}

	fullPath := filepath.Join(wt.Path, filepath.Clean("/"+path))

	if err := webuilog.ValidatePathWithinDir(fullPath, wt.Path); err != nil {
		return service.ErrForbidden("path outside worktree")
	}
	if pathsec.IsDeniedPath(fullPath) {
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
