// Package store defines repository interfaces for loom's persistent state.
//
// Implementations live in sibling packages (notably internal/infra/fleetdb
// for the production HTTP-backed store). Tests use in-memory fakes.
//
// All methods take ctx context.Context as the first argument and return
// domain types. Errors wrap the sentinels in package domain
// (domain.ErrNotFound, domain.ErrAlreadyExists, etc.) so callers can
// match via errors.Is regardless of the underlying implementation.
package store

import "io"

// Store is the composite repository interface — the dependency callers
// actually take. It groups the entity-specific sub-stores so a single
// constructor call wires up everything at once.
//
// Implementations are expected to be safe for concurrent use across
// goroutines. Close releases any underlying resources (HTTP connection
// pools, subprocess handles); idempotent.
type Store interface {
	Workspaces() WorkspaceStore
	Repos() RepoStore
	Agents() AgentStore
	Nodes() NodeStore
	AgentSessions() AgentSessionStore
	SessionEvals() SessionEvalStore
	TerminalSessions() TerminalSessionStore
	Artifacts() ArtifactStore
	AgentLeases() AgentLeaseStore
	AgentOwnershipLeases() AgentOwnershipLeaseStore
	AgentCommands() AgentCommandStore
	AgentInboxMessages() AgentInboxMessageStore
	Drivers() DriverStore
	DriverVersions() DriverVersionStore
	WorkerProfiles() WorkerProfileStore
	AgentServices() AgentServiceStore
	TriggerBindings() TriggerBindingStore
	TriggerEvents() TriggerEventStore
	TriggerDeliveries() TriggerDeliveryStore
	TriggerRoutes() TriggerRouteDispatcher
	DriverRuns() DriverRunStore
	DriverSteps() DriverStepStore
	TaskRuns() TaskRunStore
	TaskRunEvents() TaskRunEventStore
	Outbox() OutboxStore
	Awaits() AwaitStore
	Connectors() ConnectorStore
	ConnectorGrants() ConnectorGrantStore
	ConnectorCalls() ConnectorAuditStore
	Workers() WorkerStore
	Roles() RoleStore
	Daemon() DaemonProfileStore
	io.Closer
}
