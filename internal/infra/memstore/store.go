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
	awaits     *awaitStore
	workers    *workerStore
	roles      *roleStore
	skills     *skillStore
	skillPacks *skillPackStore
	daemon     *daemonStore
	capabils   *capabilityStore
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

	drivers, versions, profiles, roles, services, bindings, skills := newCatalogGraph()
	nodes := newNodeStore()
	artifacts := newArtifactStore()
	runs := newDriverRunStore(versions, bindings)
	steps := newDriverStepStore(runs)
	taskRuns := newTaskRunStore(runs, steps, artifacts, profiles, nodes)
	events := newTriggerEventStore()
	deliveries := newTriggerDeliveryStore(bindings)
	routes := &triggerRouteStore{bindings: bindings, events: events, deliveries: deliveries, runs: runs}
	awaits := newAwaitStore(events)
	linkRunGraph(runs, steps, taskRuns, awaits)
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
		awaits:     awaits,
		workers:    newWorkerStore(),
		roles:      roles,
		skills:     skills,
		skillPacks: newSkillPackStore(),
		daemon:     newDaemonStore(),
		capabils:   newCapabilityStore(),
		conns:      newConnectorStore(),
		grants:     newConnectorGrantStore(),
		audits:     newConnectorAuditStore(),
	}
}

// linkRunGraph closes the cycles between the run stores, which cannot be set
// at construction because each needs a store the others are still building.
func linkRunGraph(runs *driverRunStore, steps *driverStepStore, taskRuns *taskRunStore, awaits *awaitStore) {
	runs.steps = steps
	runs.taskRuns = taskRuns
	steps.taskRuns = taskRuns
	// ResumeAwaiting's security gate: only a resolved (satisfied/timed_out)
	// await releases its suspended run.
	runs.setAwaitResumeEligible(awaits.resumeEligible)
}

// newCatalogGraph wires the mutually-referencing catalog stores (drivers,
// versions, profiles, roles, services, bindings, skills) and returns the
// handles New needs for the rest of the dependency graph.
func newCatalogGraph() (*driverStore, *driverVersionStore, *workerProfileStore, *roleStore, *agentServiceStore, *triggerBindingStore, *skillStore) {
	drivers := newDriverStore()
	versions := newDriverVersionStore(drivers)
	profiles := newWorkerProfileStore()
	roles := newRoleStore()
	services := newAgentServiceStore(roles, profiles)
	bindings := newTriggerBindingStore(versions, services)
	skills := newSkillStore(roles)
	services.bindings = bindings
	roles.services = services
	profiles.services = services
	// Mirrors the server's refusal to delete a role that still owns skills:
	// cascading would destroy hand-written documents that may be the only copy,
	// and re-scoping the orphans to workspace would promote one persona's
	// instructions into every agent's context.
	roles.skills = skills
	return drivers, versions, profiles, roles, services, bindings, skills
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

// TaskBlocked reports whether a TaskRunFinish with BlockTask marked the given
// task ID blocked. memstore has no issue model (issues live in fleet-db), so
// this is the test-side observable for the blocked-issue transition.
func (s *Store) TaskBlocked(ws, taskID string) bool { return s.taskRuns.TaskBlocked(ws, taskID) }

// TaskRunEvents returns the TaskRunEventStore.
func (s *Store) TaskRunEvents() store.TaskRunEventStore { return s.taskEvents }

// Outbox returns the OutboxStore.
func (s *Store) Outbox() store.OutboxStore { return s.outbox }

// Awaits returns the AwaitStore (chunk AW4). The await index shares the
// trigger-event journal's lock so register-and-check is atomic with event
// appends; see awaitStore.
func (s *Store) Awaits() store.AwaitStore { return s.awaits }

// Workers returns the WorkerStore.
func (s *Store) Workers() store.WorkerStore { return s.workers }

// Roles returns the RoleStore.
func (s *Store) Roles() store.RoleStore { return s.roles }

// Skills returns the SkillStore.
func (s *Store) Skills() store.SkillStore { return s.skills }

// SkillMaterializationLeases is nil because memstore is process-local and
// production materialization coordination belongs to fleet-db's Redis lease.
func (s *Store) SkillMaterializationLeases() store.SkillMaterializationLeaseStore { return nil }

// SkillPacks returns the SkillPackStore.
func (s *Store) SkillPacks() store.SkillPackStore { return s.skillPacks }

// SetSkillActor swaps the identity skill writes are recorded under. memstore
// has no credentials, so this is the stand-in for the authenticated actor
// fleet-db takes from the API key (or X-Actor) — the sole input to the
// ownership guard. A test plays a second writer by calling this between
// writes.
func (s *Store) SetSkillActor(actor string) {
	s.skills.setActor(actor)
	s.skillPacks.mu.Lock()
	s.skillPacks.actor = actor
	s.skillPacks.mu.Unlock()
}

// Daemon returns the DaemonProfileStore.
func (s *Store) Daemon() store.DaemonProfileStore { return s.daemon }

// Capabilities reports a synthetic full-capability document.
//
// memstore is not a fleet-db and has no routes to enumerate; every capability
// loom declares is served, by definition, because memstore implements the
// whole Store interface in process. Reporting the manifest verbatim keeps an
// in-memory run's boot preflight clean instead of failing it on an absence
// that has no meaning here.
func (s *Store) Capabilities() store.CapabilityStore { return s.capabils }

// Close is a no-op — memory has no resources to release.
func (s *Store) Close() error { return nil }

// Compile-time check.
var _ store.Store = (*Store)(nil)
