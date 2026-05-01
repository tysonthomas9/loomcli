package cli

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// WorkspaceAwareIssueBackend returns an IssueBackend factory that picks a
// backend based on the workspace ID carried on ctx. In cloud mode
// (LOOM_FLEET_DB_URL set) it builds (and caches) a fleet-db backend per
// workspace so /api/workspaces/{ws}/... handlers see workspace-scoped data
// instead of the single process-global beads backend. Falls back to
// DefaultIssueBackend when ctx has no workspace or the env var is unset
// — preserves legacy single-workspace beads behavior.
func WorkspaceAwareIssueBackend() func(ctx context.Context) backend.IssueBackend {
	return WorkspaceAwareIssueBackendForURL(os.Getenv(bootstrap.EnvFleetDBURL), os.Getenv(bootstrap.EnvFleetDBActor))
}

// WorkspaceAwareIssueBackendForURL returns an IssueBackend factory scoped to a
// concrete fleet-db base URL. Serve uses this for embedded local mode, where
// fleet-db is running but LOOM_FLEET_DB_URL intentionally remains unset.
func WorkspaceAwareIssueBackendForURL(fleetURL, actor string) func(ctx context.Context) backend.IssueBackend {
	if fleetURL == "" {
		// Local/beads mode: ctx-aware factory degenerates to the global
		// backend. Single-workspace beads doesn't multiplex on ctx.
		return func(_ context.Context) backend.IssueBackend {
			return DefaultIssueBackend()
		}
	}

	if actor == "" {
		actor = os.Getenv("USER")
	}
	var (
		mu    sync.Mutex
		cache = make(map[string]backend.IssueBackend)
	)
	return func(ctx context.Context) backend.IssueBackend {
		wsID := middleware.WorkspaceFromContext(ctx)
		if wsID == "" {
			return DefaultIssueBackend()
		}

		mu.Lock()
		defer mu.Unlock()
		if be, ok := cache[wsID]; ok {
			return be
		}
		fb, err := fleet.New(fleet.Config{
			BaseURL:     fleetURL,
			WorkspaceID: wsID,
			Actor:       actor,
		})
		if err != nil {
			slog.Warn("workspace fleet backend construction failed", "ws", wsID, "err", err)
			return DefaultIssueBackend()
		}
		cache[wsID] = fb
		return fb
	}
}
