// Package memstore provides a test-only in-memory implementation of store.Store.
//
// Runtime code must use the fleet-db HTTP client. Local mode talks to an
// embedded fleet-db subprocess backed by Redis/miniredis; cloud mode talks to a
// fleet-db service backed by Redis/Postgres. This package exists only for unit
// tests that need a lightweight store double.
package memstore

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/store"
)

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
	sessionOps *agentSessionOperationStore
	toolCalls  *agentSessionToolCallStore
	terminals  *terminalSessionStore
	artifacts  *artifactStore
	leases     *agentLeaseStore
	ownership  *agentOwnershipLeaseStore
	commands   *agentCommandStore
	roles      *roleStore
	daemon     *daemonStore
	workflow   *workflowStore
}

// New constructs an empty in-memory store for tests.
//
// Production code uses internal/infra/fleetdb.New instead. Calling New outside
// a Go test process panics so memstore cannot become a real runtime control
// plane by accident.
func New() *Store {
	requireTestProcess()

	return &Store{
		workspaces: newWorkspaceStore(),
		repos:      newRepoStore(),
		agents:     newAgentStore(),
		nodes:      newNodeStore(),
		sessions:   newAgentSessionStore(),
		sessionOps: newAgentSessionOperationStore(),
		toolCalls:  newAgentSessionToolCallStore(),
		terminals:  newTerminalSessionStore(),
		artifacts:  newArtifactStore(),
		leases:     newAgentLeaseStore(),
		ownership:  newAgentOwnershipLeaseStore(),
		commands:   newAgentCommandStore(),
		roles:      newRoleStore(),
		daemon:     newDaemonStore(),
		workflow:   newWorkflowStore(),
	}
}

func requireTestProcess() {
	if runningUnderGoTest() {
		return
	}
	panic("memstore is test-only; runtime code must use fleet-db over HTTP")
}

func runningUnderGoTest() bool {
	if strings.HasSuffix(filepath.Base(os.Args[0]), ".test") {
		return true
	}
	return flag.Lookup("test.v") != nil
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

func (s *Store) AgentSessionOperations() store.AgentSessionOperationStore { return s.sessionOps }

func (s *Store) AgentSessionToolCalls() store.AgentSessionToolCallStore { return s.toolCalls }

func (s *Store) TerminalSessions() store.TerminalSessionStore { return s.terminals }

func (s *Store) Artifacts() store.ArtifactStore { return s.artifacts }

func (s *Store) AgentLeases() store.AgentLeaseStore { return s.leases }

func (s *Store) AgentOwnershipLeases() store.AgentOwnershipLeaseStore { return s.ownership }

func (s *Store) AgentCommands() store.AgentCommandStore { return s.commands }

// Roles returns the RoleStore.
func (s *Store) Roles() store.RoleStore { return s.roles }

// Daemon returns the DaemonProfileStore.
func (s *Store) Daemon() store.DaemonProfileStore { return s.daemon }

func (s *Store) DefinitionVersions() store.DefinitionVersionStore {
	return &definitionVersionStore{core: s.workflow}
}

func (s *Store) WorkflowDefinitions() store.WorkflowDefinitionStore {
	return &workflowDefinitionStore{core: s.workflow}
}

func (s *Store) WorkflowRuns() store.WorkflowRunStore { return &workflowRunStore{core: s.workflow} }

func (s *Store) TaskRuns() store.TaskRunStore { return &taskRunStore{core: s.workflow} }

func (s *Store) RunEvents() store.RunEventStore { return &runEventStore{core: s.workflow} }

func (s *Store) RuntimeProfiles() store.RuntimeProfileStore {
	return &runtimeProfileStore{core: s.workflow}
}

func (s *Store) RouteBindings() store.RouteBindingStore { return &routeBindingStore{core: s.workflow} }

func (s *Store) TriggerBindings() store.TriggerBindingStore {
	return &triggerBindingStore{core: s.workflow}
}

// Close is a no-op — memory has no resources to release.
func (s *Store) Close() error { return nil }

// Compile-time check.
var _ store.Store = (*Store)(nil)
