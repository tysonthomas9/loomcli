package store

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// WorkspaceFileStore owns complete immutable workspace file manifests and
// their bytes. Publication derives content identity from Bytes and is atomic:
// callers either observe the whole tree or no tree.
type WorkspaceFileStore interface {
	Publish(ctx context.Context, workspaceKey string, files []domain.WorkspaceFileInput) (*domain.WorkspaceFileTreePublishResult, error)
	GetTree(ctx context.Context, workspaceKey, revision string) (*domain.WorkspaceFileTree, error)
	Stat(ctx context.Context, workspaceKey, revision, path string) (*domain.WorkspaceFile, error)
	Download(ctx context.Context, workspaceKey, revision, path string) ([]byte, error)
}
