// Package memstore provides test-only in-memory implementations of
// capability-owned persistence ports.
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

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

// Store exposes owner-specific in-memory adapters with all data held in memory. Safe for
// concurrent use across goroutines. Each entity store carries its own
// RWMutex; the package intentionally has no shared lock.
//
// Memory grows with Create calls and is only released by Delete; treat
// long-lived processes accordingly.
type Store struct {
	workspaces *workspaceStore
	repos      *repoStore
	nodes      *nodeStore
	sessions   *agentSessionStore
	terminals  *terminalSessionStore
	artifacts  *artifactStore
	leases     *agentLeaseStore
	ownership  *agentOwnershipLeaseStore
	inbox      *agentInboxMessageStore
	drivers    *driverStore
	versions   *driverVersionStore
	profiles   *workerProfileStore
	services   *agentServiceStore
	bindings   *triggerBindingStore
	events     *triggerEventStore
	deliveries *triggerDeliveryStore
	runs       *driverRunStore
	steps      *driverStepStore
	taskRuns   *taskRunStore
	taskEvents *taskRunEventStore
	outbox     *outboxStore
	awaits     *awaitStore
	workers    *workerStore
	roles      *roleStore
	conns      *connectorStore
	grants     *connectorGrantStore
	audits     *connectorAuditStore
}

// New constructs an empty in-memory store for tests.
//
// Production code uses internal/infra/fleetdb.New instead. Calling New outside
// a Go test process panics so memstore cannot become a real runtime control
// plane by accident.
func New() *Store { //nolint:funlen // Constructor wiring is deliberately explicit so test-store dependencies remain auditable.
	requireTestProcess()

	drivers, versions, profiles, roles, services, bindings := newCatalogGraph()
	nodes := newNodeStore()
	artifacts := newArtifactStore()
	runs := newDriverRunStore(versions, bindings)
	steps := newDriverStepStore(runs)
	taskRuns := newTaskRunStore(runs, steps, artifacts, profiles, nodes)
	runs.steps = steps
	runs.taskRuns = taskRuns
	steps.taskRuns = taskRuns
	events := newTriggerEventStore()
	runs.events = events
	deliveries := newTriggerDeliveryStore(bindings)
	awaits := newAwaitStore(events)
	awaits.runs = runs
	ownership := newAgentOwnershipLeaseStore()
	// ResumeAwaiting's security gate: only a resolved (satisfied/timed_out)
	// await releases its suspended run.
	runs.setAwaitResumeEligible(awaits.resumeEligible)
	return &Store{
		workspaces: newWorkspaceStore(),
		repos:      newRepoStore(),
		nodes:      nodes,
		sessions:   newAgentSessionStore(),
		terminals:  newTerminalSessionStore(),
		artifacts:  artifacts,
		leases:     newAgentLeaseStore(),
		ownership:  ownership,
		inbox:      newAgentInboxMessageStore(),
		drivers:    drivers,
		versions:   versions,
		profiles:   profiles,
		services:   services,
		bindings:   bindings,
		events:     events,
		deliveries: deliveries,
		runs:       runs,
		steps:      steps,
		taskRuns:   taskRuns,
		taskEvents: newTaskRunEventStore(),
		outbox:     newOutboxStore(),
		awaits:     awaits,
		workers:    newWorkerStore(),
		roles:      roles,
		conns:      newConnectorStore(),
		grants:     newConnectorGrantStore(),
		audits:     newConnectorAuditStore(),
	}
}

// newCatalogGraph wires the mutually-referencing catalog stores (drivers,
// versions, profiles, roles, services, bindings) and returns the handles New
// needs for the rest of the dependency graph.
func newCatalogGraph() (*driverStore, *driverVersionStore, *workerProfileStore, *roleStore, *agentServiceStore, *triggerBindingStore) {
	drivers := newDriverStore()
	versions := newDriverVersionStore(drivers)
	profiles := newWorkerProfileStore()
	roles := newRoleStore()
	services := newAgentServiceStore(roles, profiles, drivers, versions)
	bindings := newTriggerBindingStore(versions, services)
	services.bindings = bindings
	roles.services = services
	profiles.services = services
	return drivers, versions, profiles, roles, services, bindings
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
func (s *Store) Workspaces() workspaceowner.WorkspaceStore { return s.workspaces }

// Repos returns the RepoStore.
func (s *Store) Repos() workspaceowner.RepoStore { return s.repos }

// Nodes returns the NodeStore.
func (s *Store) Nodes() execution.NodeStore { return s.nodes }

// AgentSessions returns the AgentSessionStore.
func (s *Store) AgentSessions() interaction.AgentSessionStore { return s.sessions }

func (s *Store) TerminalSessions() interaction.TerminalSessionStore { return s.terminals }

// ArtifactQueries exposes the Artifacts-owned read port implemented by the
// same in-memory persistence adapter used by lifecycle tests.
func (s *Store) ArtifactQueries() artifacts.QueryStore { return s.artifacts }

func (s *Store) AgentLeases() interaction.AgentLeaseStore { return s.leases }

func (s *Store) AgentOwnershipLeases() agents.AgentOwnershipLeaseStore { return s.ownership }

func (s *Store) AgentInboxMessages() interaction.AgentInboxMessageStore { return s.inbox }

func (s *Store) Drivers() workflowcatalog.DriverStore { return s.drivers }

func (s *Store) DriverVersions() workflowcatalog.DriverVersionStore { return s.versions }

func (s *Store) WorkerProfiles() execution.WorkerProfileStore { return s.profiles }

func (s *Store) AgentServices() agents.AgentServiceStore { return s.services }

func (s *Store) TriggerBindings() automation.TriggerBindingStore { return s.bindings }

func (s *Store) TriggerEvents() automation.TriggerEventStore { return s.events }

func (s *Store) TriggerDeliveries() automation.TriggerDeliveryStore { return s.deliveries }

func (s *Store) DriverRuns() execution.DriverRunStore { return s.runs }

func (s *Store) DriverSteps() execution.DriverStepStore { return s.steps }

func (s *Store) TaskRuns() execution.TaskRunStore { return s.taskRuns }

// TaskBlocked reports whether a TaskRunFinish with BlockTask marked the given
// task ID blocked. memstore has no issue model (issues live in fleet-db), so
// this is the test-side observable for the blocked-issue transition.
func (s *Store) TaskBlocked(ws, taskID string) bool { return s.taskRuns.TaskBlocked(ws, taskID) }

// TaskRunEvents returns the TaskRunEventStore.
func (s *Store) TaskRunEvents() execution.TaskRunEventStore { return s.taskEvents }

// Outbox returns the OutboxStore.
func (s *Store) Outbox() execution.OutboxStore { return s.outbox }

// Awaits returns the AwaitStore (chunk AW4). The await index shares the
// trigger-event journal's lock so register-and-check is atomic with event
// appends; see awaitStore.
func (s *Store) Awaits() execution.AwaitStore { return s.awaits }

// Workers returns the WorkerStore.
func (s *Store) Workers() execution.WorkerStore { return s.workers }

// Roles returns the RoleStore.
func (s *Store) Roles() agents.RoleRecordStore { return s.roles }

// Close is a no-op — memory has no resources to release.
func (s *Store) Close() error { return nil }
