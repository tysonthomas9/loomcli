package store

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// DaemonProfileStore is the persistence interface for per-workspace
// daemon settings. There is exactly one DaemonProfile per Workspace,
// auto-created with defaults when the workspace is created — hence Get
// + Upsert rather than Create / Update / Delete.
type DaemonProfileStore interface {
	// Get returns the workspace's daemon profile. Returns ErrNotFound
	// only if the workspace itself does not exist; otherwise returns the
	// default-valued profile if no explicit settings have been written.
	Get(ctx context.Context, workspaceKey string) (*domain.DaemonProfile, error)

	// Upsert writes the full DaemonProfile for the workspace, replacing
	// any prior values. Callers wanting partial-update semantics should
	// Get + mutate + Upsert.
	Upsert(ctx context.Context, profile *domain.DaemonProfile) (*domain.DaemonProfile, error)
}
