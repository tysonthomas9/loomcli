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
	terminals  *terminalSessionStore
	artifacts  *artifactStore
	leases     *agentLeaseStore
	ownership  *agentOwnershipLeaseStore
	commands   *agentCommandStore
	inbox      *agentInboxMessageStore
	drivers    *driverStore
	versions   *driverVersionStore
	profiles   *workerProfileStore
	services   *agentServiceStore
	bindings   *triggerBindingStore
	events     *triggerEventStore
	deliveries *triggerDeliveryStore
	routes     *triggerRouteStore
	runs       *driverRunStore
	steps      *driverStepStore
	taskRuns   *taskRunStore
	taskEvents *taskRunEventStore
	outbox     *outboxStore
	workers    *workerStore
	roles      *roleStore
	daemon     *daemonStore
	conns      *connectorStore
	grants     *connectorGrantStore
	audits     *connectorAuditStore
}

// New constructs an empty in-memory store for tests.
//
// Production code uses internal/infra/fleetdb.New instead. Calling New outside
// a Go test process panics so memstore cannot become a real runtime control
// plane by accident.
func New() *Store {
	requireTestProcess()

	drivers := newDriverStore()
	versions := newDriverVersionStore(drivers)
	profiles := newWorkerProfileStore()
	roles := newRoleStore()
	services := newAgentServiceStore(roles, profiles)
	bindings := newTriggerBindingStore(versions, services)
	services.bindings = bindings
	roles.services = services
	profiles.services = services
	nodes := newNodeStore()
	artifacts := newArtifactStore()
	runs := newDriverRunStore(versions, bindings)
	steps := newDriverStepStore(runs)
	taskRuns := newTaskRunStore(runs, steps, artifacts, profiles, nodes)
	runs.steps = steps
	runs.taskRuns = taskRuns
	steps.taskRuns = taskRuns
	events := newTriggerEventStore()
	deliveries := newTriggerDeliveryStore(bindings)
	routes := &triggerRouteStore{bindings: bindings, events: events, deliveries: deliveries, runs: runs}
	return &Store{
		workspaces: newWorkspaceStore(),
		repos:      newRepoStore(),
		agents:     newAgentStore(),
		nodes:      nodes,
		sessions:   newAgentSessionStore(),
		terminals:  newTerminalSessionStore(),
		artifacts:  artifacts,
		leases:     newAgentLeaseStore(),
		ownership:  newAgentOwnershipLeaseStore(),
		commands:   newAgentCommandStore(),
		inbox:      newAgentInboxMessageStore(),
		drivers:    drivers,
		versions:   versions,
		profiles:   profiles,
		services:   services,
		bindings:   bindings,
		events:     events,
		deliveries: deliveries,
		routes:     routes,
		runs:       runs,
		steps:      steps,
		taskRuns:   taskRuns,
		taskEvents: newTaskRunEventStore(),
		outbox:     newOutboxStore(),
		workers:    newWorkerStore(),
		roles:      roles,
		daemon:     newDaemonStore(),
		conns:      newConnectorStore(),
		grants:     newConnectorGrantStore(),
		audits:     newConnectorAuditStore(),
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

func (s *Store) TerminalSessions() store.TerminalSessionStore { return s.terminals }

func (s *Store) Artifacts() store.ArtifactStore { return s.artifacts }

func (s *Store) AgentLeases() store.AgentLeaseStore { return s.leases }

func (s *Store) AgentOwnershipLeases() store.AgentOwnershipLeaseStore { return s.ownership }

func (s *Store) AgentCommands() store.AgentCommandStore { return s.commands }

func (s *Store) AgentInboxMessages() store.AgentInboxMessageStore { return s.inbox }

func (s *Store) Drivers() store.DriverStore { return s.drivers }

func (s *Store) DriverVersions() store.DriverVersionStore { return s.versions }

func (s *Store) WorkerProfiles() store.WorkerProfileStore { return s.profiles }

func (s *Store) AgentServices() store.AgentServiceStore { return s.services }

func (s *Store) TriggerBindings() store.TriggerBindingStore { return s.bindings }

func (s *Store) TriggerEvents() store.TriggerEventStore { return s.events }

func (s *Store) TriggerDeliveries() store.TriggerDeliveryStore { return s.deliveries }

func (s *Store) TriggerRoutes() store.TriggerRouteDispatcher { return s.routes }

func (s *Store) DriverRuns() store.DriverRunStore { return s.runs }

func (s *Store) DriverSteps() store.DriverStepStore { return s.steps }

func (s *Store) TaskRuns() store.TaskRunStore { return s.taskRuns }

// TaskParked reports whether a TaskRunFinish with ParkTask marked the given
// task ID parked. memstore has no issue model (issues live in fleet-db), so
// this is the test-side observable for the parked-issue transition.
func (s *Store) TaskParked(ws, taskID string) bool { return s.taskRuns.TaskParked(ws, taskID) }

// TaskRunEvents returns the TaskRunEventStore.
func (s *Store) TaskRunEvents() store.TaskRunEventStore { return s.taskEvents }

// Outbox returns the OutboxStore.
func (s *Store) Outbox() store.OutboxStore { return s.outbox }

// Workers returns the WorkerStore.
func (s *Store) Workers() store.WorkerStore { return s.workers }

// Roles returns the RoleStore.
func (s *Store) Roles() store.RoleStore { return s.roles }

// Daemon returns the DaemonProfileStore.
func (s *Store) Daemon() store.DaemonProfileStore { return s.daemon }

// Close is a no-op — memory has no resources to release.
func (s *Store) Close() error { return nil }

// Compile-time check.
var _ store.Store = (*Store)(nil)
