package cmdstore

// Traced wrapper for the daemon profile store, mirroring
// internal/store/daemon_store.go. Shared span helpers live in
// store_tracing.go.

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// --- DaemonProfileStore ---

type tracedDaemonStore struct{ inner store.DaemonProfileStore }

func (t *tracedDaemonStore) Get(ctx context.Context, ws string) (*domain.DaemonProfile, error) {
	return traced(ctx, "Daemon", "Get", func(ctx context.Context) (*domain.DaemonProfile, error) {
		return t.inner.Get(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDaemonStore) Upsert(ctx context.Context, profile *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	ws := ""
	if profile != nil {
		ws = profile.WorkspaceKey
	}
	return traced(ctx, "Daemon", "Upsert", func(ctx context.Context) (*domain.DaemonProfile, error) {
		return t.inner.Upsert(ctx, profile)
	},
		attribute.String("loom.workspace", ws),
	)
}
