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
	nodes      *nodeStore
	sessions   *agentSessionStore
	terminals  *terminalSessionStore
	artifacts  *artifactStore
	leases     *agentLeaseStore
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
		nodes:      newNodeStore(),
		sessions:   newAgentSessionStore(),
		terminals:  newTerminalSessionStore(),
		artifacts:  newArtifactStore(),
		leases:     newAgentLeaseStore(),
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

// Nodes returns the NodeStore.
func (s *Store) Nodes() store.NodeStore { return s.nodes }

// AgentSessions returns the AgentSessionStore.
func (s *Store) AgentSessions() store.AgentSessionStore { return s.sessions }

func (s *Store) TerminalSessions() store.TerminalSessionStore { return s.terminals }

func (s *Store) Artifacts() store.ArtifactStore { return s.artifacts }

func (s *Store) AgentLeases() store.AgentLeaseStore { return s.leases }

// Roles returns the RoleStore.
func (s *Store) Roles() store.RoleStore { return s.roles }

// Daemon returns the DaemonProfileStore.
func (s *Store) Daemon() store.DaemonProfileStore { return s.daemon }

// Close is a no-op — memory has no resources to release.
func (s *Store) Close() error { return nil }

// Compile-time check.
var _ store.Store = (*Store)(nil)
