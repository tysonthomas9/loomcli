package cmdstore

// Store tracing decorator.
//
// This file lives in cmdstore (rather than internal/cli/) so that
// cmdstore.OpenStore can call WrapStoreWithTracing without an import
// cycle (package cli imports cmdstore; cmdstore cannot import cli).
// Callers in package cli, internal/cli/config, and internal/cli/serve
// can all reference the exported symbol from this package.

import (
	"context"
	"errors"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// storeTracerName is the instrumentation library name reported on every
// store-service span. Stable so dashboards filtering on it don't break.
const storeTracerName = "github.com/tysonthomas9/loomcli/internal/cli/store"

// WrapStoreWithTracing returns a tracing-decorated store.Store. Every
// sub-store accessor returns a wrapper that emits a span per method named
// `service.Store.<SubStore>.<Method>`. Sub-store wrappers are constructed
// once at wrap time and reused, so accessor calls are zero-allocation.
//
// nil-safe: passing nil returns nil so callers can wrap unconditionally.
func WrapStoreWithTracing(inner store.Store) store.Store {
	if inner == nil {
		return nil
	}
	return &tracedStore{
		inner:                inner,
		workspaces:           &tracedWorkspaceStore{inner: inner.Workspaces()},
		repos:                &tracedRepoStore{inner: inner.Repos()},
		agents:               &tracedAgentStore{inner: inner.Agents()},
		nodes:                &tracedNodeStore{inner: inner.Nodes()},
		agentSessions:        &tracedAgentSessionStore{inner: inner.AgentSessions()},
		terminalSessions:     &tracedTerminalSessionStore{inner: inner.TerminalSessions()},
		artifacts:            &tracedArtifactStore{inner: inner.Artifacts()},
		agentLeases:          &tracedAgentLeaseStore{inner: inner.AgentLeases()},
		agentOwnershipLeases: &tracedAgentOwnershipLeaseStore{inner: inner.AgentOwnershipLeases()},
		agentCommands:        &tracedAgentCommandStore{inner: inner.AgentCommands()},
		agentInboxMessages:   &tracedAgentInboxMessageStore{inner: inner.AgentInboxMessages()},
		drivers:              &tracedDriverStore{inner: inner.Drivers()},
		driverVersions:       &tracedDriverVersionStore{inner: inner.DriverVersions()},
		workerProfiles:       &tracedWorkerProfileStore{inner: inner.WorkerProfiles()},
		agentServices:        &tracedAgentServiceStore{inner: inner.AgentServices()},
		triggerBindings:      &tracedTriggerBindingStore{inner: inner.TriggerBindings()},
		driverRuns:           &tracedDriverRunStore{inner: inner.DriverRuns()},
		driverSteps:          &tracedDriverStepStore{inner: inner.DriverSteps()},
		taskRuns:             &tracedTaskRunStore{inner: inner.TaskRuns()},
		taskRunEvents:        &tracedTaskRunEventStore{inner: inner.TaskRunEvents()},
		outbox:               &tracedOutboxStore{inner: inner.Outbox()},
		awaits:               &tracedAwaitStore{inner: inner.Awaits()},
		workers:              &tracedWorkerStore{inner: inner.Workers()},
		roles:                &tracedRoleStore{inner: inner.Roles()},
		daemon:               &tracedDaemonStore{inner: inner.Daemon()},
		connectors:           &tracedConnectorStore{inner: inner.Connectors()},
		connectorGrants:      &tracedConnectorGrantStore{inner: inner.ConnectorGrants()},
		connectorCalls:       &tracedConnectorAuditStore{inner: inner.ConnectorCalls()},
	}
}

type tracedStore struct {
	inner                store.Store
	workspaces           *tracedWorkspaceStore
	repos                *tracedRepoStore
	agents               *tracedAgentStore
	nodes                *tracedNodeStore
	agentSessions        *tracedAgentSessionStore
	terminalSessions     *tracedTerminalSessionStore
	artifacts            *tracedArtifactStore
	agentLeases          *tracedAgentLeaseStore
	agentOwnershipLeases *tracedAgentOwnershipLeaseStore
	agentCommands        *tracedAgentCommandStore
	agentInboxMessages   *tracedAgentInboxMessageStore
	drivers              *tracedDriverStore
	driverVersions       *tracedDriverVersionStore
	workerProfiles       *tracedWorkerProfileStore
	agentServices        *tracedAgentServiceStore
	triggerBindings      *tracedTriggerBindingStore
	driverRuns           *tracedDriverRunStore
	driverSteps          *tracedDriverStepStore
	taskRuns             *tracedTaskRunStore
	taskRunEvents        *tracedTaskRunEventStore
	outbox               *tracedOutboxStore
	awaits               *tracedAwaitStore
	workers              *tracedWorkerStore
	roles                *tracedRoleStore
	daemon               *tracedDaemonStore
	connectors           *tracedConnectorStore
	connectorGrants      *tracedConnectorGrantStore
	connectorCalls       *tracedConnectorAuditStore
}

func (t *tracedStore) Workspaces() store.WorkspaceStore       { return t.workspaces }
func (t *tracedStore) Repos() store.RepoStore                 { return t.repos }
func (t *tracedStore) Agents() store.AgentStore               { return t.agents }
func (t *tracedStore) Nodes() store.NodeStore                 { return t.nodes }
func (t *tracedStore) AgentSessions() store.AgentSessionStore { return t.agentSessions }
func (t *tracedStore) TerminalSessions() store.TerminalSessionStore {
	return t.terminalSessions
}
func (t *tracedStore) Artifacts() store.ArtifactStore { return t.artifacts }
func (t *tracedStore) AgentLeases() store.AgentLeaseStore {
	return t.agentLeases
}
func (t *tracedStore) AgentOwnershipLeases() store.AgentOwnershipLeaseStore {
	return t.agentOwnershipLeases
}
func (t *tracedStore) AgentCommands() store.AgentCommandStore {
	return t.agentCommands
}
func (t *tracedStore) AgentInboxMessages() store.AgentInboxMessageStore {
	return t.agentInboxMessages
}
func (t *tracedStore) Drivers() store.DriverStore { return t.drivers }
func (t *tracedStore) DriverVersions() store.DriverVersionStore {
	return t.driverVersions
}
func (t *tracedStore) WorkerProfiles() store.WorkerProfileStore {
	return t.workerProfiles
}
func (t *tracedStore) AgentServices() store.AgentServiceStore {
	return t.agentServices
}
func (t *tracedStore) TriggerBindings() store.TriggerBindingStore {
	return t.triggerBindings
}
func (t *tracedStore) TriggerEvents() store.TriggerEventStore {
	return t.inner.TriggerEvents()
}
func (t *tracedStore) TriggerDeliveries() store.TriggerDeliveryStore {
	return t.inner.TriggerDeliveries()
}
func (t *tracedStore) TriggerRoutes() store.TriggerRouteDispatcher {
	return t.inner.TriggerRoutes()
}
func (t *tracedStore) DriverRuns() store.DriverRunStore { return t.driverRuns }
func (t *tracedStore) DriverSteps() store.DriverStepStore {
	return t.driverSteps
}
func (t *tracedStore) TaskRuns() store.TaskRunStore { return t.taskRuns }
func (t *tracedStore) TaskRunEvents() store.TaskRunEventStore {
	return t.taskRunEvents
}
func (t *tracedStore) Outbox() store.OutboxStore {
	return t.outbox
}
func (t *tracedStore) Workers() store.WorkerStore       { return t.workers }
func (t *tracedStore) Roles() store.RoleStore           { return t.roles }
func (t *tracedStore) Daemon() store.DaemonProfileStore { return t.daemon }

// Skills, workspace files, materialization leases, and SkillPacks pass through untraced, as the
// trigger sub-stores above do. Skill CRUD is operator-initiated and the lease
// wraps a single local projection; wrap them when there is a question spans
// would answer.
func (t *tracedStore) Skills() store.SkillStore { return t.inner.Skills() }

func (t *tracedStore) WorkspaceFiles() store.WorkspaceFileStore { return t.inner.WorkspaceFiles() }

func (t *tracedStore) SkillMaterializationLeases() store.SkillMaterializationLeaseStore {
	return t.inner.SkillMaterializationLeases()
}

func (t *tracedStore) SkillPacks() store.SkillPackStore { return t.inner.SkillPacks() }

// Awaits returns the traced await wrapper (chunk AW5): spans per call with
// workspace / instance / run / pattern attributes.
func (t *tracedStore) Awaits() store.AwaitStore { return t.awaits }

func (t *tracedStore) Close() error {
	if c, ok := t.inner.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// startSpan opens a span named `service.Store.<sub>.<method>`. Caller-
// supplied attrs are appended verbatim. The trace contract forbids
// PII / free-form text — callers should pass IDs, names, counts, and
// structured tags only.
func startStoreSpan(ctx context.Context, sub, method string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := tracing.Tracer(storeTracerName)
	return tracer.Start(ctx, "service.Store."+sub+"."+method,
		trace.WithAttributes(attrs...),
	)
}

// finish closes a span. context.Canceled keeps status unset (per §7);
// other errors get RecordError + a low-cardinality status reason.
func finish(span trace.Span, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		span.RecordError(err)
		span.SetStatus(codes.Error, storeErrReason(err))
	}
	span.End()
}

// traced wraps fn in a `service.Store.<sub>.<method>` span. fn receives
// the span context; its error (minus context.Canceled) is recorded on the
// span. Every traced sub-store method below funnels through this (or one
// of the variants), so the span lifecycle lives in exactly one place.
func traced[T any](ctx context.Context, sub, method string, fn func(context.Context) (T, error), attrs ...attribute.KeyValue) (T, error) {
	ctx, span := startStoreSpan(ctx, sub, method, attrs...)
	out, err := fn(ctx)
	finish(span, err)
	return out, err
}

// tracedList is traced for slice-returning methods; on success it also
// records the result length as `result.count`.
func tracedList[T any](ctx context.Context, sub, method string, fn func(context.Context) ([]T, error), attrs ...attribute.KeyValue) ([]T, error) {
	ctx, span := startStoreSpan(ctx, sub, method, attrs...)
	out, err := fn(ctx)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

// tracedErr is traced for error-only Delete methods.
func tracedErr(ctx context.Context, sub string, fn func(context.Context) error, attrs ...attribute.KeyValue) error {
	ctx, span := startStoreSpan(ctx, sub, "Delete", attrs...)
	err := fn(ctx)
	finish(span, err)
	return err
}

// storeErrReason maps a domain sentinel to a fixed-set status reason so
// span status stays low-cardinality. Returns "unknown" for opaque errors.
func storeErrReason(err error) string {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, domain.ErrInvalid):
		return "invalid"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	}
	return "unknown"
}
