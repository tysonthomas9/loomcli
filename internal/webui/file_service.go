package webui

import "context"

// FileService defines business logic for file operations within agent worktrees.
// Handlers call this interface and map returned errors to HTTP responses.
type FileService interface {
	// ListDirectory lists one level of a directory within an agent's worktree.
	ListDirectory(ctx context.Context, wsID, agentName, path string) (*FileTreeResult, error)

	// ReadFile reads a file within an agent's worktree. Binary files return metadata only.
	ReadFile(ctx context.Context, wsID, agentName, path string) (*FileReadResult, error)

	// WriteFile writes content to a file within an agent's worktree using atomic temp+rename.
	WriteFile(ctx context.Context, wsID, agentName, path, content string) error
}

// Compile-time check that fileServiceImpl satisfies FileService.
var _ FileService = (*fileServiceImpl)(nil)

// FileTreeEntry describes a directory entry in a file tree listing.
type FileTreeEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// FileTreeResult contains a directory listing result.
type FileTreeResult struct {
	Path    string          `json:"path"`
	Entries []FileTreeEntry `json:"entries"`
}

// FileReadResult contains file read content and metadata.
type FileReadResult struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Size    int64  `json:"size"`
	Binary  bool   `json:"binary"`
}
