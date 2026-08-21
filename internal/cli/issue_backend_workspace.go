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

const maxOccupantBackends = 64

type occupantBackendEntry struct {
	backend  backend.IssueBackend
	lastUsed uint64
}

type workspaceIssueBackendFactory struct {
	fleetURL string
	actor    string

	mu            sync.Mutex
	cache         map[issueBackendKey]backend.IssueBackend
	occupantCache map[issueBackendKey]occupantBackendEntry
	useSequence   uint64
}

var errOccupantNeedsFleet = errors.New("occupant principals require a fleet-db-backed serve (no local-mode fallback)")

// resolveRequestActor returns the fleet-db actor to use for ctx, or a non-nil
// fail-closed backend when the request principal is unusable. Validate runs
// before the kind check so a zero Actor{} (kind "") cannot slip past.
func resolveRequestActor(ctx context.Context, wsID, processActor string) (string, bool, backend.IssueBackend) {
	a, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return processActor, false, nil
	}
	if err := a.Validate(); err != nil {
		return "", false, newUnavailableIssueBackend(IssueBackendFleetDB, err)
	}
	if a.Kind() != middleware.ActorKindOccupant {
		return processActor, false, nil
	}
	if wsID == "" {
		return "", true, newUnavailableIssueBackend(IssueBackendFleetDB, errOccupantNeedsFleet)
	}
	return a.BackendActor(), true, nil
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
			if _, _, unavailable := resolveRequestActor(ctx, "", ""); unavailable != nil {
				return unavailable
			}
			return DefaultIssueBackend()
		}
	}

	factory := newWorkspaceIssueBackendFactory(fleetURL, actor)
	return factory.backend
}

func newWorkspaceIssueBackendFactory(fleetURL, actor string) *workspaceIssueBackendFactory {
	if actor == "" {
		actor = os.Getenv(bootstrap.EnvFleetDBActor)
	}
	if actor == "" {
		actor = os.Getenv("USER")
	}
	return &workspaceIssueBackendFactory{
		fleetURL:      fleetURL,
		actor:         actor,
		cache:         make(map[issueBackendKey]backend.IssueBackend),
		occupantCache: make(map[issueBackendKey]occupantBackendEntry),
	}
}

func (f *workspaceIssueBackendFactory) backend(ctx context.Context) backend.IssueBackend {
	wsID := middleware.WorkspaceFromContext(ctx)
	reqActor, isOccupant, unavailable := resolveRequestActor(ctx, wsID, f.actor)
	if unavailable != nil {
		return unavailable
	}
	if wsID == "" {
		return DefaultIssueBackend()
	}

	key := issueBackendKey{ws: wsID, actor: reqActor}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.useSequence++
	if isOccupant {
		return f.occupantBackend(key)
	}
	if be, ok := f.cache[key]; ok {
		return be
	}
	be := f.newBackend(wsID, reqActor)
	if !isUnavailableIssueBackend(be) {
		f.cache[key] = be
	}
	return be
}

func (f *workspaceIssueBackendFactory) occupantBackend(key issueBackendKey) backend.IssueBackend {
	if entry, ok := f.occupantCache[key]; ok {
		entry.lastUsed = f.useSequence
		f.occupantCache[key] = entry
		return entry.backend
	}
	be := f.newBackend(key.ws, key.actor)
	if isUnavailableIssueBackend(be) {
		return be
	}
	if len(f.occupantCache) >= maxOccupantBackends {
		f.evictOldestOccupant()
	}
	f.occupantCache[key] = occupantBackendEntry{backend: be, lastUsed: f.useSequence}
	return be
}

func (f *workspaceIssueBackendFactory) evictOldestOccupant() {
	var oldestKey issueBackendKey
	var oldestUse uint64
	first := true
	for key, entry := range f.occupantCache {
		if first || entry.lastUsed < oldestUse {
			oldestKey, oldestUse, first = key, entry.lastUsed, false
		}
	}
	if !first {
		delete(f.occupantCache, oldestKey)
	}
}

func (f *workspaceIssueBackendFactory) newBackend(wsID, actor string) backend.IssueBackend {
	fb, err := fleet.New(fleet.Config{BaseURL: f.fleetURL, WorkspaceID: wsID, Actor: actor})
	if err != nil {
		slog.Error("workspace fleet backend construction failed", "ws", wsID, "err", err)
		return newUnavailableIssueBackend(IssueBackendFleetDB, err)
	}
	return fb
}

func isUnavailableIssueBackend(be backend.IssueBackend) bool {
	_, ok := be.(*unavailableIssueBackend)
	return ok
}
