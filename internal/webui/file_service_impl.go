package webui

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
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// fileServiceImpl is the concrete implementation of FileService.
type fileServiceImpl struct {
	fileOps ops.FileOps
}

// NewFileService creates a new FileService implementation.
func NewFileService(fileOps ops.FileOps) FileService {
	return &fileServiceImpl{fileOps: fileOps}
}

func (s *fileServiceImpl) resolveAgent(wsID, agentName string) (*ops.AgentWorktree, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	wt, err := s.fileOps.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

func (s *fileServiceImpl) ListDirectory(_ context.Context, wsID, agentName, path string) (*FileTreeResult, error) {
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

	entries := make([]FileTreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Type()&os.ModeSymlink != 0 {
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
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	relPath, _ := filepath.Rel(wt.Path, fullPath)
	if relPath == "" {
		relPath = "."
	}

	return &FileTreeResult{Path: relPath, Entries: entries}, nil
}

func (s *fileServiceImpl) ReadFile(_ context.Context, wsID, agentName, path string) (*FileReadResult, error) {
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

	f, err := openLogFileSecure(fullPath, wt.Path)
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

	if isBinaryContent(data) {
		return &FileReadResult{
			Path:   path,
			Size:   fi.Size(),
			Binary: true,
		}, nil
	}

	return &FileReadResult{
		Path:    path,
		Content: string(data),
		Size:    fi.Size(),
		Binary:  false,
	}, nil
}

func (s *fileServiceImpl) WriteFile(_ context.Context, wsID, agentName, path, content string) error {
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

	if writeErr := validateParentDir(fullPath, wt.Path); writeErr != nil {
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

	perm, writeErr := resolveWritePermissions(fullPath)
	if writeErr != nil {
		return service.ErrForbidden(writeErr.Message)
	}

	if err := atomicWriteFile(fullPath, content, perm); err != nil {
		return service.ErrInternal("failed to save file", err)
	}
	return nil
}
