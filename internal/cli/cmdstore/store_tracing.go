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
		drivers:              &tracedDriverStore{inner: inner.Drivers()},
		driverVersions:       &tracedDriverVersionStore{inner: inner.DriverVersions()},
		workerProfiles:       &tracedWorkerProfileStore{inner: inner.WorkerProfiles()},
		agentServices:        &tracedAgentServiceStore{inner: inner.AgentServices()},
		triggerBindings:      &tracedTriggerBindingStore{inner: inner.TriggerBindings()},
		driverRuns:           &tracedDriverRunStore{inner: inner.DriverRuns()},
		driverSteps:          &tracedDriverStepStore{inner: inner.DriverSteps()},
		taskRuns:             &tracedTaskRunStore{inner: inner.TaskRuns()},
		roles:                &tracedRoleStore{inner: inner.Roles()},
		daemon:               &tracedDaemonStore{inner: inner.Daemon()},
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
	drivers              *tracedDriverStore
	driverVersions       *tracedDriverVersionStore
	workerProfiles       *tracedWorkerProfileStore
	agentServices        *tracedAgentServiceStore
	triggerBindings      *tracedTriggerBindingStore
	driverRuns           *tracedDriverRunStore
	driverSteps          *tracedDriverStepStore
	taskRuns             *tracedTaskRunStore
	roles                *tracedRoleStore
	daemon               *tracedDaemonStore
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
func (t *tracedStore) DriverRuns() store.DriverRunStore { return t.driverRuns }
func (t *tracedStore) DriverSteps() store.DriverStepStore {
	return t.driverSteps
}
func (t *tracedStore) TaskRuns() store.TaskRunStore     { return t.taskRuns }
func (t *tracedStore) Roles() store.RoleStore           { return t.roles }
func (t *tracedStore) Daemon() store.DaemonProfileStore { return t.daemon }

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

func (t *tracedArtifactStore) UploadContent(ctx context.Context, ws, artifactID string, upload store.ArtifactContentUpload) (*domain.Artifact, error) {
	ctx, span := startStoreSpan(ctx, "Artifacts", "UploadContent",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.UploadContent(ctx, ws, artifactID, upload)
	finish(span, err)
	return out, err
}

func (t *tracedArtifactStore) Finalize(ctx context.Context, ws, artifactID string, finalize store.ArtifactFinalize) (*domain.Artifact, error) {
	ctx, span := startStoreSpan(ctx, "Artifacts", "Finalize",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Finalize(ctx, ws, artifactID, finalize)
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

// --- DriverStore ---

type tracedDriverStore struct{ inner store.DriverStore }

func (t *tracedDriverStore) Create(ctx context.Context, in store.DriverCreate) (*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStore) Get(ctx context.Context, ws, driverID string) (*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, driverID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStore) List(ctx context.Context, ws string, filter store.DriverFilter) ([]*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverStore) Update(ctx context.Context, ws, driverID string, patch store.DriverUpdate) (*domain.Driver, error) {
	ctx, span := startStoreSpan(ctx, "Drivers", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, driverID, patch)
	finish(span, err)
	return out, err
}

// --- DriverVersionStore ---

type tracedDriverVersionStore struct{ inner store.DriverVersionStore }

func (t *tracedDriverVersionStore) Create(ctx context.Context, in store.DriverVersionCreate) (*domain.DriverVersion, error) {
	ctx, span := startStoreSpan(ctx, "DriverVersions", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverVersionStore) Get(ctx context.Context, ws, versionID string) (*domain.DriverVersion, error) {
	ctx, span := startStoreSpan(ctx, "DriverVersions", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, versionID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverVersionStore) List(ctx context.Context, ws string, filter store.DriverVersionFilter) ([]*domain.DriverVersion, error) {
	ctx, span := startStoreSpan(ctx, "DriverVersions", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

// --- WorkerProfileStore ---

type tracedWorkerProfileStore struct{ inner store.WorkerProfileStore }

func (t *tracedWorkerProfileStore) Create(ctx context.Context, in store.WorkerProfileCreate) (*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.profile", in.ProfileID),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) Get(ctx context.Context, ws, profileID string) (*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
	out, err := t.inner.Get(ctx, ws, profileID)
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) List(ctx context.Context, ws string, filter store.WorkerProfileFilter) ([]*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) Update(ctx context.Context, ws, profileID string, patch store.WorkerProfileUpdate) (*domain.WorkerProfile, error) {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
	out, err := t.inner.Update(ctx, ws, profileID, patch)
	finish(span, err)
	return out, err
}

func (t *tracedWorkerProfileStore) Delete(ctx context.Context, ws, profileID string) error {
	ctx, span := startStoreSpan(ctx, "WorkerProfiles", "Delete",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
	err := t.inner.Delete(ctx, ws, profileID)
	finish(span, err)
	return err
}

// --- AgentServiceStore ---

type tracedAgentServiceStore struct{ inner store.AgentServiceStore }

func (t *tracedAgentServiceStore) Create(ctx context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.service", in.ServiceID),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) Get(ctx context.Context, ws, serviceID string) (*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
	out, err := t.inner.Get(ctx, ws, serviceID)
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) List(ctx context.Context, ws string, filter store.AgentServiceFilter) ([]*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) Update(ctx context.Context, ws, serviceID string, patch store.AgentServiceUpdate) (*domain.AgentService, error) {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
	out, err := t.inner.Update(ctx, ws, serviceID, patch)
	finish(span, err)
	return out, err
}

func (t *tracedAgentServiceStore) Delete(ctx context.Context, ws, serviceID string) error {
	ctx, span := startStoreSpan(ctx, "AgentServices", "Delete",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
	err := t.inner.Delete(ctx, ws, serviceID)
	finish(span, err)
	return err
}

// --- TriggerBindingStore ---

type tracedTriggerBindingStore struct{ inner store.TriggerBindingStore }

func (t *tracedTriggerBindingStore) Create(ctx context.Context, in store.TriggerBindingCreate) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.binding", in.BindingID),
		attribute.String("loom.route_key", in.RouteKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "Get",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
	out, err := t.inner.Get(ctx, ws, bindingID)
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) GetByRouteKey(ctx context.Context, ws, routeKey string) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "GetByRouteKey",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.route_key", routeKey),
	)
	out, err := t.inner.GetByRouteKey(ctx, ws, routeKey)
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) List(ctx context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedTriggerBindingStore) Update(ctx context.Context, ws, bindingID string, patch store.TriggerBindingUpdate) (*domain.TriggerBinding, error) {
	ctx, span := startStoreSpan(ctx, "TriggerBindings", "Update",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
	out, err := t.inner.Update(ctx, ws, bindingID, patch)
	finish(span, err)
	return out, err
}

// --- DriverRunStore ---

type tracedDriverRunStore struct{ inner store.DriverRunStore }

func (t *tracedDriverRunStore) Create(ctx context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) CreateEpic(ctx context.Context, ws, epicID string, in store.EpicRunCreate) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "CreateEpic",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.epic", epicID),
	)
	out, err := t.inner.CreateEpic(ctx, ws, epicID, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Get(ctx context.Context, ws, runID string) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, runID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) List(ctx context.Context, ws string, filter store.DriverRunFilter) ([]*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Claim(ctx context.Context, ws, runID, nodeID, leaseID string) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Claim",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Claim(ctx, ws, runID, nodeID, leaseID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Heartbeat(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Heartbeat",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Heartbeat(ctx, ws, runID, nodeID, leaseID, fencingToken)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Finish(ctx context.Context, ws, runID string, finishRun store.DriverRunFinish) (*domain.DriverRun, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Finish",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Finish(ctx, ws, runID, finishRun)
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) RecoverStale(ctx context.Context, ws string, recover store.StaleDriverRunRecovery) (*store.StaleDriverRunRecoveryResult, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "RecoverStale",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.RecoverStale(ctx, ws, recover)
	if err == nil && out != nil {
		span.SetAttributes(
			attribute.Int("loom.driver_runs.recovered", out.Recovered),
			attribute.Int("loom.driver_runs.skipped_fresh", out.SkippedFresh),
		)
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) RecoverStaleTaskRuns(ctx context.Context, ws, runID string, recover store.StaleTaskRunRecovery) (*store.StaleTaskRunRecoveryResult, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "RecoverStaleTaskRuns",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run", runID),
	)
	out, err := t.inner.RecoverStaleTaskRuns(ctx, ws, runID, recover)
	if err == nil && out != nil {
		span.SetAttributes(
			attribute.Int("loom.task_runs.recovered", out.Recovered),
			attribute.Int("loom.tasks.released", out.Released),
		)
	}
	finish(span, err)
	return out, err
}

// --- DriverStepStore ---

type tracedDriverStepStore struct{ inner store.DriverStepStore }

func (t *tracedDriverStepStore) Create(ctx context.Context, in store.DriverStepCreate) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.driver_run_id", in.DriverRunID),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) CreateForRun(ctx context.Context, ws, runID string, in store.DriverStepCreate) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "CreateForRun",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
	out, err := t.inner.CreateForRun(ctx, ws, runID, in)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) Get(ctx context.Context, ws, stepID string) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, stepID)
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) List(ctx context.Context, ws string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) ListForRun(ctx context.Context, ws, runID string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "ListForRun",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
	out, err := t.inner.ListForRun(ctx, ws, runID, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverStepStore) Update(ctx context.Context, ws, stepID string, update store.DriverStepUpdate) (*domain.DriverStep, error) {
	ctx, span := startStoreSpan(ctx, "DriverSteps", "Update",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Update(ctx, ws, stepID, update)
	finish(span, err)
	return out, err
}

// --- TaskRunStore ---

type tracedTaskRunStore struct{ inner store.TaskRunStore }

func (t *tracedTaskRunStore) Create(ctx context.Context, in store.TaskRunCreate) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Create",
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
	out, err := t.inner.Create(ctx, in)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) ClaimQueued(ctx context.Context, ws string, claim store.TaskRunClaim) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "ClaimQueued",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.ClaimQueued(ctx, ws, claim)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Get(ctx context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Get",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Get(ctx, ws, taskRunID)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) List(ctx context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "List",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.List(ctx, ws, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Heartbeat(ctx context.Context, ws, taskRunID string, heartbeat store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Heartbeat",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Heartbeat(ctx, ws, taskRunID, heartbeat)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Finish(ctx context.Context, ws, taskRunID string, taskFinish store.TaskRunFinish) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Finish",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Finish(ctx, ws, taskRunID, taskFinish)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) Complete(ctx context.Context, ws, taskRunID string, taskComplete store.TaskRunComplete) (*domain.TaskRun, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "Complete",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.Complete(ctx, ws, taskRunID, taskComplete)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) AppendLog(ctx context.Context, ws, taskRunID string, appendLog store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "AppendLog",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.AppendLog(ctx, ws, taskRunID, appendLog)
	finish(span, err)
	return out, err
}

func (t *tracedTaskRunStore) ListLogs(ctx context.Context, ws, taskRunID string, filter store.TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error) {
	ctx, span := startStoreSpan(ctx, "TaskRuns", "ListLogs",
		attribute.String("loom.workspace", ws),
	)
	out, err := t.inner.ListLogs(ctx, ws, taskRunID, filter)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
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
