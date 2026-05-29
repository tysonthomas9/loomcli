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
	"time"

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
		roles:                &tracedRoleStore{inner: inner.Roles()},
		daemon:               &tracedDaemonStore{inner: inner.Daemon()},
		defVersions:          inner.DefinitionVersions(),
		workflowDefs:         inner.WorkflowDefinitions(),
		workflowRuns:         inner.WorkflowRuns(),
		taskRuns:             inner.TaskRuns(),
		runEvents:            inner.RunEvents(),
		runtimes:             inner.RuntimeProfiles(),
		routes:               inner.RouteBindings(),
		triggers:             inner.TriggerBindings(),
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
	roles                *tracedRoleStore
	daemon               *tracedDaemonStore
	defVersions          store.DefinitionVersionStore
	workflowDefs         store.WorkflowDefinitionStore
	workflowRuns         store.WorkflowRunStore
	taskRuns             store.TaskRunStore
	runEvents            store.RunEventStore
	runtimes             store.RuntimeProfileStore
	routes               store.RouteBindingStore
	triggers             store.TriggerBindingStore
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
func (t *tracedStore) Roles() store.RoleStore           { return t.roles }
func (t *tracedStore) Daemon() store.DaemonProfileStore { return t.daemon }
func (t *tracedStore) DefinitionVersions() store.DefinitionVersionStore {
	return t.defVersions
}
func (t *tracedStore) WorkflowDefinitions() store.WorkflowDefinitionStore {
	return t.workflowDefs
}
func (t *tracedStore) WorkflowRuns() store.WorkflowRunStore { return t.workflowRuns }
func (t *tracedStore) TaskRuns() store.TaskRunStore         { return t.taskRuns }
func (t *tracedStore) RunEvents() store.RunEventStore       { return t.runEvents }
func (t *tracedStore) RuntimeProfiles() store.RuntimeProfileStore {
	return t.runtimes
}
func (t *tracedStore) RouteBindings() store.RouteBindingStore { return t.routes }
func (t *tracedStore) TriggerBindings() store.TriggerBindingStore {
	return t.triggers
}

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

// --- WorkspaceStore ---

type tracedWorkspaceStore struct{ inner store.WorkspaceStore }

func (t *tracedWorkspaceStore) Create(ctx context.Context, in store.WorkspaceCreate) (*domain.Workspace, error) {
	ctx, span := startStoreSpan(ctx, "Workspaces", "Create",
		attribute.String("loom.workspace", in.Key),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	ctx, span := startStoreSpan(ctx, "Workspaces", "Get",
		attribute.String("loom.workspace", key),
	)
	out, err := t.inner.Get(ctx, key)
	finish(span, err)
	return out, err
}

func (t *tracedWorkspaceStore) GetByName(ctx context.Context, name string) (*domain.Workspace, error) {
	ctx, span := startStoreSpan(ctx, "Workspaces", "GetByName")
	out, err := t.inner.GetByName(ctx, name)
	finish(span, err)
	return out, err
}

func (t *tracedWorkspaceStore) List(ctx context.Context) ([]*domain.Workspace, error) {
	ctx, span := startStoreSpan(ctx, "Workspaces", "List")
	out, err := t.inner.List(ctx)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedWorkspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	ctx, span := startStoreSpan(ctx, "Workspaces", "Update",
		attribute.String("loom.workspace", key),
	)
	out, err := t.inner.Update(ctx, key, patch)
	finish(span, err)
	return out, err
}

func (t *tracedWorkspaceStore) Delete(ctx context.Context, key string) error {
	ctx, span := startStoreSpan(ctx, "Workspaces", "Delete",
		attribute.String("loom.workspace", key),
	)
	err := t.inner.Delete(ctx, key)
	finish(span, err)
	return err
}

// --- RepoStore ---

type tracedRepoStore struct{ inner store.RepoStore }

func (t *tracedRepoStore) Create(ctx context.Context, in store.RepoCreate) (*domain.Repo, error) {
	ctx, span := startStoreSpan(ctx, "Repos", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedRepoStore) Get(ctx context.Context, ws, name string) (*domain.Repo, error) {
	ctx, span := startStoreSpan(ctx, "Repos", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, name)
	finish(span, err)
	return out, err
}

func (t *tracedRepoStore) List(ctx context.Context, ws string) ([]*domain.Repo, error) {
	ctx, span := startStoreSpan(ctx, "Repos", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedRepoStore) Update(ctx context.Context, ws, name string, patch store.RepoUpdate) (*domain.Repo, error) {
	ctx, span := startStoreSpan(ctx, "Repos", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, name, patch)
	finish(span, err)
	return out, err
}

func (t *tracedRepoStore) Delete(ctx context.Context, ws, name string) error {
	ctx, span := startStoreSpan(ctx, "Repos", "Delete",
		attribute.String("loom.workspace", ws),
	)
	err := t.inner.Delete(ctx, ws, name)
	finish(span, err)
	return err
}

// --- AgentStore ---

type tracedAgentStore struct{ inner store.AgentStore }

func (t *tracedAgentStore) Create(ctx context.Context, in store.AgentCreate) (*domain.Agent, error) {
	ctx, span := startStoreSpan(ctx, "Agents", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.agent", in.Name),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedAgentStore) Get(ctx context.Context, ws, name string) (*domain.Agent, error) {
	ctx, span := startStoreSpan(ctx, "Agents", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
	out, err := t.inner.Get(ctx, ws, name)
	finish(span, err)
	return out, err
}

func (t *tracedAgentStore) List(ctx context.Context, ws string) ([]*domain.Agent, error) {
	ctx, span := startStoreSpan(ctx, "Agents", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedAgentStore) Update(ctx context.Context, ws, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	ctx, span := startStoreSpan(ctx, "Agents", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
	out, err := t.inner.Update(ctx, ws, name, patch)
	finish(span, err)
	return out, err
}

func (t *tracedAgentStore) Delete(ctx context.Context, ws, name string) error {
	ctx, span := startStoreSpan(ctx, "Agents", "Delete",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
	err := t.inner.Delete(ctx, ws, name)
	finish(span, err)
	return err
}

// --- RoleStore ---

type tracedRoleStore struct{ inner store.RoleStore }

func (t *tracedRoleStore) Create(ctx context.Context, in store.RoleCreate) (*domain.Role, error) {
	ctx, span := startStoreSpan(ctx, "Roles", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.role", in.Name),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedRoleStore) Get(ctx context.Context, ws, name string) (*domain.Role, error) {
	ctx, span := startStoreSpan(ctx, "Roles", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
	out, err := t.inner.Get(ctx, ws, name)
	finish(span, err)
	return out, err
}

func (t *tracedRoleStore) List(ctx context.Context, ws string) ([]*domain.Role, error) {
	ctx, span := startStoreSpan(ctx, "Roles", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedRoleStore) Update(ctx context.Context, ws, name string, patch store.RoleUpdate) (*domain.Role, error) {
	ctx, span := startStoreSpan(ctx, "Roles", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
	out, err := t.inner.Update(ctx, ws, name, patch)
	finish(span, err)
	return out, err
}

func (t *tracedRoleStore) Delete(ctx context.Context, ws, name string) error {
	ctx, span := startStoreSpan(ctx, "Roles", "Delete",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
	err := t.inner.Delete(ctx, ws, name)
	finish(span, err)
	return err
}

// --- NodeStore ---

type tracedNodeStore struct{ inner store.NodeStore }

func (t *tracedNodeStore) Create(ctx context.Context, in store.NodeCreate) (*domain.Node, error) {
	ctx, span := startStoreSpan(ctx, "Nodes", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedNodeStore) Get(ctx context.Context, ws, nodeID string) (*domain.Node, error) {
	ctx, span := startStoreSpan(ctx, "Nodes", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, nodeID)
	finish(span, err)
	return out, err
}

func (t *tracedNodeStore) List(ctx context.Context, ws string) ([]*domain.Node, error) {
	ctx, span := startStoreSpan(ctx, "Nodes", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedNodeStore) Heartbeat(ctx context.Context, ws, nodeID string, ttl time.Duration) (*domain.Node, error) {
	ctx, span := startStoreSpan(ctx, "Nodes", "Heartbeat",
		attribute.String("loom.workspace", ws),
		attribute.Int64("ttl_ms", ttl.Milliseconds()),
	)
	out, err := t.inner.Heartbeat(ctx, ws, nodeID, ttl)
	finish(span, err)
	return out, err
}

func (t *tracedNodeStore) Update(ctx context.Context, ws, nodeID string, patch store.NodeUpdate) (*domain.Node, error) {
	ctx, span := startStoreSpan(ctx, "Nodes", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, nodeID, patch)
	finish(span, err)
	return out, err
}

// --- AgentSessionStore ---

type tracedAgentSessionStore struct{ inner store.AgentSessionStore }

func (t *tracedAgentSessionStore) Create(ctx context.Context, in store.AgentSessionCreate) (*domain.AgentSession, error) {
	ctx, span := startStoreSpan(ctx, "AgentSessions", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedAgentSessionStore) Get(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	ctx, span := startStoreSpan(ctx, "AgentSessions", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, sessionID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentSessionStore) List(ctx context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	ctx, span := startStoreSpan(ctx, "AgentSessions", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedAgentSessionStore) Heartbeat(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	ctx, span := startStoreSpan(ctx, "AgentSessions", "Heartbeat",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Heartbeat(ctx, ws, sessionID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentSessionStore) Update(ctx context.Context, ws, sessionID string, patch store.AgentSessionUpdate) (*domain.AgentSession, error) {
	ctx, span := startStoreSpan(ctx, "AgentSessions", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, sessionID, patch)
	finish(span, err)
	return out, err
}

// --- TerminalSessionStore ---

type tracedTerminalSessionStore struct{ inner store.TerminalSessionStore }

func (t *tracedTerminalSessionStore) Create(ctx context.Context, in store.TerminalSessionCreate) (*domain.TerminalSession, error) {
	ctx, span := startStoreSpan(ctx, "TerminalSessions", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedTerminalSessionStore) Get(ctx context.Context, ws, terminalID string) (*domain.TerminalSession, error) {
	ctx, span := startStoreSpan(ctx, "TerminalSessions", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, terminalID)
	finish(span, err)
	return out, err
}

func (t *tracedTerminalSessionStore) List(ctx context.Context, ws string, filter store.TerminalSessionFilter) ([]*domain.TerminalSession, error) {
	ctx, span := startStoreSpan(ctx, "TerminalSessions", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedTerminalSessionStore) Update(ctx context.Context, ws, terminalID string, patch store.TerminalSessionUpdate) (*domain.TerminalSession, error) {
	ctx, span := startStoreSpan(ctx, "TerminalSessions", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, terminalID, patch)
	finish(span, err)
	return out, err
}

// --- ArtifactStore ---

type tracedArtifactStore struct{ inner store.ArtifactStore }

func (t *tracedArtifactStore) Create(ctx context.Context, in store.ArtifactCreate) (*domain.Artifact, error) {
	ctx, span := startStoreSpan(ctx, "Artifacts", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedArtifactStore) Get(ctx context.Context, ws, artifactID string) (*domain.Artifact, error) {
	ctx, span := startStoreSpan(ctx, "Artifacts", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, artifactID)
	finish(span, err)
	return out, err
}

func (t *tracedArtifactStore) List(ctx context.Context, ws string, filter store.ArtifactFilter) ([]*domain.Artifact, error) {
	ctx, span := startStoreSpan(ctx, "Artifacts", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedArtifactStore) Update(ctx context.Context, ws, artifactID string, patch store.ArtifactUpdate) (*domain.Artifact, error) {
	ctx, span := startStoreSpan(ctx, "Artifacts", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, artifactID, patch)
	finish(span, err)
	return out, err
}

// --- AgentLeaseStore ---

type tracedAgentLeaseStore struct{ inner store.AgentLeaseStore }

func (t *tracedAgentLeaseStore) Create(ctx context.Context, in store.AgentLeaseCreate) (*domain.AgentLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentLeases", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedAgentLeaseStore) Get(ctx context.Context, ws, leaseID string) (*domain.AgentLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentLeases", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, leaseID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentLeaseStore) List(ctx context.Context, ws string, filter store.AgentLeaseFilter) ([]*domain.AgentLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentLeases", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedAgentLeaseStore) Heartbeat(ctx context.Context, ws, leaseID, token string, ttl time.Duration) (*domain.AgentLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentLeases", "Heartbeat",
		attribute.String("loom.workspace", ws),
		attribute.Int64("ttl_ms", ttl.Milliseconds()),
	)
	out, err := t.inner.Heartbeat(ctx, ws, leaseID, token, ttl)
	finish(span, err)
	return out, err
}

func (t *tracedAgentLeaseStore) Release(ctx context.Context, ws, leaseID, token string) (*domain.AgentLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentLeases", "Release",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Release(ctx, ws, leaseID, token)
	finish(span, err)
	return out, err
}

// --- AgentOwnershipLeaseStore ---

type tracedAgentOwnershipLeaseStore struct {
	inner store.AgentOwnershipLeaseStore
}

func (t *tracedAgentOwnershipLeaseStore) Acquire(ctx context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentOwnershipLeases", "Acquire",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Acquire(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedAgentOwnershipLeaseStore) Get(ctx context.Context, ws, agentID string) (*domain.AgentOwnershipLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentOwnershipLeases", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, agentID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentOwnershipLeaseStore) List(ctx context.Context, ws string, filter store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentOwnershipLeases", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedAgentOwnershipLeaseStore) Heartbeat(ctx context.Context, ws, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentOwnershipLeases", "Heartbeat",
		attribute.String("loom.workspace", ws),
		attribute.Int64("ttl_ms", ttl.Milliseconds()),
	)
	out, err := t.inner.Heartbeat(ctx, ws, agentID, token, ttl)
	finish(span, err)
	return out, err
}

func (t *tracedAgentOwnershipLeaseStore) Release(ctx context.Context, ws, agentID, token string) (*domain.AgentOwnershipLease, error) {
	ctx, span := startStoreSpan(ctx, "AgentOwnershipLeases", "Release",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Release(ctx, ws, agentID, token)
	finish(span, err)
	return out, err
}

// --- AgentCommandStore ---

type tracedAgentCommandStore struct{ inner store.AgentCommandStore }

func (t *tracedAgentCommandStore) Create(ctx context.Context, in store.AgentCommandCreate) (*domain.AgentCommand, error) {
	ctx, span := startStoreSpan(ctx, "AgentCommands", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedAgentCommandStore) Get(ctx context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	ctx, span := startStoreSpan(ctx, "AgentCommands", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, commandID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentCommandStore) List(ctx context.Context, ws string, filter store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	ctx, span := startStoreSpan(ctx, "AgentCommands", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedAgentCommandStore) Ack(ctx context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	ctx, span := startStoreSpan(ctx, "AgentCommands", "Ack",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Ack(ctx, ws, commandID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentCommandStore) Complete(ctx context.Context, ws, commandID string, update store.AgentCommandComplete) (*domain.AgentCommand, error) {
	ctx, span := startStoreSpan(ctx, "AgentCommands", "Complete",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Complete(ctx, ws, commandID, update)
	finish(span, err)
	return out, err
}

// --- DaemonProfileStore ---

type tracedDaemonStore struct{ inner store.DaemonProfileStore }

func (t *tracedDaemonStore) Get(ctx context.Context, ws string) (*domain.DaemonProfile, error) {
	ctx, span := startStoreSpan(ctx, "Daemon", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws)
	finish(span, err)
	return out, err
}

func (t *tracedDaemonStore) Upsert(ctx context.Context, profile *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	ws := ""
	if profile != nil {
		ws = profile.WorkspaceKey
	}
	ctx, span := startStoreSpan(ctx, "Daemon", "Upsert",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Upsert(ctx, profile)
	finish(span, err)
	return out, err
}
