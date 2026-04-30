// Package memstore provides an in-memory implementation of store.Store.
//
// Used by tests and during early development before the fleet-db HTTP
// client (internal/infra/fleetdb) is wired up. Not safe for production
// — state is lost when the process exits.
package memstore

import "github.com/tysonthomas9/loomcli/internal/store"

// Store implements store.Store with all data held in memory. Safe for
// concurrent use across goroutines. Each entity store carries its own
// RWMutex; the package intentionally has no shared lock.
//
// Memory grows with Create calls and is only released by Delete; treat
// long-lived processes accordingly.
type Store struct {
	workspaces *workspaceStore
	repos      *repoStore
	agents     *agentStore
	roles      *roleStore
	daemon     *daemonStore
}

// New constructs an empty in-memory store. Tests call this directly;
// production code uses internal/infra/fleetdb.New instead.
func New() *Store {
	return &Store{
		workspaces: newWorkspaceStore(),
		repos:      newRepoStore(),
		agents:     newAgentStore(),
		roles:      newRoleStore(),
		daemon:     newDaemonStore(),
	}
}

// clonePtr returns a copy of *p, or nil if p is nil. Used by the
// per-entity clone helpers to deep-copy optional pointer fields.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Workspaces returns the WorkspaceStore.
func (s *Store) Workspaces() store.WorkspaceStore { return s.workspaces }

// Repos returns the RepoStore.
func (s *Store) Repos() store.RepoStore { return s.repos }

// Agents returns the AgentStore.
func (s *Store) Agents() store.AgentStore { return s.agents }

// Roles returns the RoleStore.
func (s *Store) Roles() store.RoleStore { return s.roles }

// Daemon returns the DaemonProfileStore.
func (s *Store) Daemon() store.DaemonProfileStore { return s.daemon }

// Close is a no-op — memory has no resources to release.
func (s *Store) Close() error { return nil }

// Compile-time check.
var _ store.Store = (*Store)(nil)
