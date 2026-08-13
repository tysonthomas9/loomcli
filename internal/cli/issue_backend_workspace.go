package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type issueBackendKey struct{ ws, actor string }

var errOccupantNeedsFleet = errors.New("occupant principals require a fleet-db-backed serve (no local-mode fallback)")

// resolveRequestActor returns the fleet-db actor to use for ctx, or a non-nil
// fail-closed backend when the request principal is unusable. Validate runs
// before the kind check so a zero Actor{} (kind "") cannot slip past.
func resolveRequestActor(ctx context.Context, wsID, processActor string) (string, backend.IssueBackend) {
	a, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return processActor, nil
	}
	if err := a.Validate(); err != nil {
		return "", newUnavailableIssueBackend(IssueBackendFleetDB, err)
	}
	if a.Kind() != middleware.ActorKindOccupant {
		return processActor, nil
	}
	if wsID == "" {
		return "", newUnavailableIssueBackend(IssueBackendFleetDB, errOccupantNeedsFleet)
	}
	return a.BackendActor(), nil
}

// WorkspaceAwareIssueBackend returns an IssueBackend factory that picks a
// backend based on the workspace ID and request actor carried on ctx. In cloud
// mode (LOOM_FLEET_DB_URL set) it builds (and caches) a fleet-db backend per
// (workspace, actor) so /api/workspaces/{ws}/... handlers see workspace-scoped
// data under the correct principal instead of the process-global default
// backend. Falls back to DefaultIssueBackend when ctx has no workspace or the
// env var is unset, except occupant principals fail closed.
func WorkspaceAwareIssueBackend() func(ctx context.Context) backend.IssueBackend {
	return WorkspaceAwareIssueBackendForURL(os.Getenv(bootstrap.EnvFleetDBURL), os.Getenv(bootstrap.EnvFleetDBActor))
}

// WorkspaceAwareIssueBackendForURL returns an IssueBackend factory scoped to a
// concrete fleet-db base URL and keyed by (workspace, actor). Serve uses this
// for embedded local mode, where fleet-db is running but LOOM_FLEET_DB_URL
// intentionally remains unset.
func WorkspaceAwareIssueBackendForURL(fleetURL, actor string) func(ctx context.Context) backend.IssueBackend {
	if fleetURL == "" {
		// Local mode: ctx-aware factory degenerates to the global backend.
		return func(ctx context.Context) backend.IssueBackend {
			if _, unavailable := resolveRequestActor(ctx, "", ""); unavailable != nil {
				return unavailable
			}
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
		cache = make(map[issueBackendKey]backend.IssueBackend)
	)
	return func(ctx context.Context) backend.IssueBackend {
		wsID := middleware.WorkspaceFromContext(ctx)
		reqActor, unavailable := resolveRequestActor(ctx, wsID, actor)
		if unavailable != nil {
			return unavailable
		}
		if wsID == "" {
			return DefaultIssueBackend()
		}

		key := issueBackendKey{ws: wsID, actor: reqActor}
		mu.Lock()
		defer mu.Unlock()
		if be, ok := cache[key]; ok {
			return be
		}
		fb, err := fleet.New(fleet.Config{
			BaseURL:     fleetURL,
			WorkspaceID: wsID,
			Actor:       reqActor,
		})
		if err != nil {
			slog.Error("workspace fleet backend construction failed", "ws", wsID, "err", err)
			return newUnavailableIssueBackend(IssueBackendFleetDB, err)
		}
		cache[key] = fb
		return fb
	}
}
