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
// instead of the process-global default backend. Falls back to
// DefaultIssueBackend when ctx has no workspace or the env var is unset.
func WorkspaceAwareIssueBackend() func(ctx context.Context) backend.IssueBackend {
	return WorkspaceAwareIssueBackendForURL(os.Getenv(bootstrap.EnvFleetDBURL), os.Getenv(bootstrap.EnvFleetDBActor))
}

// WorkspaceAwareIssueBackendForURL returns an IssueBackend factory scoped to a
// concrete fleet-db base URL. Serve uses this for embedded local mode, where
// fleet-db is running but LOOM_FLEET_DB_URL intentionally remains unset.
func WorkspaceAwareIssueBackendForURL(fleetURL, actor string) func(ctx context.Context) backend.IssueBackend {
	return WorkspaceAwareIssueBackendForConfig(fleetURL, os.Getenv(bootstrap.EnvFleetDBAPIKey), actor)
}

// WorkspaceAwareIssueBackendForConfig returns an IssueBackend factory scoped
// to a concrete FleetDB connection. The API key is captured in process memory;
// it is never copied into environment state or emitted to logs. Serve uses this
// path for embedded local mode because its service credential is intentionally
// absent from the parent process environment.
func WorkspaceAwareIssueBackendForConfig(fleetURL, apiKey, actor string) func(ctx context.Context) backend.IssueBackend {
	if fleetURL == "" {
		// Local mode: ctx-aware factory degenerates to the global backend.
		return func(_ context.Context) backend.IssueBackend {
			return DefaultIssueBackend()
		}
	}

	if actor == "" {
		actor = os.Getenv(bootstrap.EnvFleetDBActor)
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
			APIKey:      apiKey,
			Actor:       actor,
		})
		if err != nil {
			slog.Error("workspace fleet backend construction failed", "ws", wsID, "err", err)
			return newUnavailableIssueBackend(IssueBackendFleetDB, err)
		}
		cache[wsID] = fb
		return fb
	}
}

// WorkspaceActorIssueBackendForConfig returns the runtime factory used by
// DriverRun and TaskRun HTTP adapters. Unlike WorkspaceAwareIssueBackendForConfig,
// the workspace and actor are explicit inputs derived from the fenced run.
// The FleetDB service credential remains captured in process memory and is
// never copied into ambient environment state or a workflow child process.
func WorkspaceActorIssueBackendForConfig(fleetURL, apiKey string) func(workspace, actor string) (backend.IssueBackend, error) {
	return func(workspace, actor string) (backend.IssueBackend, error) {
		return fleet.New(fleet.Config{
			BaseURL:     fleetURL,
			WorkspaceID: workspace,
			APIKey:      apiKey,
			Actor:       actor,
		})
	}
}
