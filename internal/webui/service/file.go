package service

import "context"

// FileScope identifies the root a scoped file operation resolves against.
// Phase 1 supports only ScopeWorkspace (the workspace folder, read-only). The
// type is defined so repo/agent scopes and write operations can be added later
// without reshaping the API contract.
type FileScope string

const (
	// ScopeWorkspace roots operations at the workspace folder — the parent
	// directory that contains every repo checkout and agent worktree. Read-only.
	ScopeWorkspace FileScope = "workspace"
)

// FileService defines business logic for file operations within agent worktrees.
// Handlers call this interface and map returned errors to HTTP responses.
//
// The *Scoped methods generalize listing/reading to an arbitrary scope root
// (currently the workspace folder) for the dedicated file browser; the
// agent-scoped methods remain for the per-agent file panel.
type FileService interface {
	// ListDirectory lists one level of a directory within an agent's worktree.
	ListDirectory(ctx context.Context, wsID, agentName, path string) (*FileTreeResult, error)

	// ReadFile reads a file within an agent's worktree. Binary files return metadata only.
	ReadFile(ctx context.Context, wsID, agentName, path string) (*FileReadResult, error)

	// WriteFile writes content to a file within an agent's worktree using atomic temp+rename.
	WriteFile(ctx context.Context, wsID, agentName, path, content string) error

	// ListDirectoryScoped lists one level of a directory under a scope root.
	// target names the scope's resource (repo/agent name); it must be empty for
	// ScopeWorkspace. Read-only.
	ListDirectoryScoped(ctx context.Context, wsID string, scope FileScope, target, path string) (*FileTreeResult, error)

	// ReadFileScoped reads a file under a scope root. Binary files return
	// metadata only. Read-only.
	ReadFileScoped(ctx context.Context, wsID string, scope FileScope, target, path string) (*FileReadResult, error)
}

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
