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
		agentInboxMessages:   &tracedAgentInboxMessageStore{inner: inner.AgentInboxMessages()},
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
	agentInboxMessages   *tracedAgentInboxMessageStore
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

// tracedErr is traced for error-only methods (Delete and friends).
func tracedErr(ctx context.Context, sub, method string, fn func(context.Context) error, attrs ...attribute.KeyValue) error {
	ctx, span := startStoreSpan(ctx, sub, method, attrs...)
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

// --- WorkspaceStore ---

type tracedWorkspaceStore struct{ inner store.WorkspaceStore }

func (t *tracedWorkspaceStore) Create(ctx context.Context, in store.WorkspaceCreate) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "Create", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.Key),
	)
}

func (t *tracedWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "Get", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.Get(ctx, key)
	},
		attribute.String("loom.workspace", key),
	)
}

func (t *tracedWorkspaceStore) GetByName(ctx context.Context, name string) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "GetByName", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.GetByName(ctx, name)
	})
}

func (t *tracedWorkspaceStore) List(ctx context.Context) ([]*domain.Workspace, error) {
	return tracedList(ctx, "Workspaces", "List", func(ctx context.Context) ([]*domain.Workspace, error) {
		return t.inner.List(ctx)
	})
}

func (t *tracedWorkspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "Update", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.Update(ctx, key, patch)
	},
		attribute.String("loom.workspace", key),
	)
}

func (t *tracedWorkspaceStore) Delete(ctx context.Context, key string) error {
	return tracedErr(ctx, "Workspaces", "Delete", func(ctx context.Context) error {
		return t.inner.Delete(ctx, key)
	},
		attribute.String("loom.workspace", key),
	)
}

// --- RepoStore ---

type tracedRepoStore struct{ inner store.RepoStore }

func (t *tracedRepoStore) Create(ctx context.Context, in store.RepoCreate) (*domain.Repo, error) {
	return traced(ctx, "Repos", "Create", func(ctx context.Context) (*domain.Repo, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedRepoStore) Get(ctx context.Context, ws, name string) (*domain.Repo, error) {
	return traced(ctx, "Repos", "Get", func(ctx context.Context) (*domain.Repo, error) {
		return t.inner.Get(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRepoStore) List(ctx context.Context, ws string) ([]*domain.Repo, error) {
	return tracedList(ctx, "Repos", "List", func(ctx context.Context) ([]*domain.Repo, error) {
		return t.inner.List(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRepoStore) Update(ctx context.Context, ws, name string, patch store.RepoUpdate) (*domain.Repo, error) {
	return traced(ctx, "Repos", "Update", func(ctx context.Context) (*domain.Repo, error) {
		return t.inner.Update(ctx, ws, name, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRepoStore) Delete(ctx context.Context, ws, name string) error {
	return tracedErr(ctx, "Repos", "Delete", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- AgentStore ---

type tracedAgentStore struct{ inner store.AgentStore }

func (t *tracedAgentStore) Create(ctx context.Context, in store.AgentCreate) (*domain.Agent, error) {
	return traced(ctx, "Agents", "Create", func(ctx context.Context) (*domain.Agent, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.agent", in.Name),
	)
}

func (t *tracedAgentStore) Get(ctx context.Context, ws, name string) (*domain.Agent, error) {
	return traced(ctx, "Agents", "Get", func(ctx context.Context) (*domain.Agent, error) {
		return t.inner.Get(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
}

func (t *tracedAgentStore) List(ctx context.Context, ws string) ([]*domain.Agent, error) {
	return tracedList(ctx, "Agents", "List", func(ctx context.Context) ([]*domain.Agent, error) {
		return t.inner.List(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentStore) Update(ctx context.Context, ws, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	return traced(ctx, "Agents", "Update", func(ctx context.Context) (*domain.Agent, error) {
		return t.inner.Update(ctx, ws, name, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
}

func (t *tracedAgentStore) Delete(ctx context.Context, ws, name string) error {
	return tracedErr(ctx, "Agents", "Delete", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
}

// --- RoleStore ---

type tracedRoleStore struct{ inner store.RoleStore }

func (t *tracedRoleStore) Create(ctx context.Context, in store.RoleCreate) (*domain.Role, error) {
	return traced(ctx, "Roles", "Create", func(ctx context.Context) (*domain.Role, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.role", in.Name),
	)
}

func (t *tracedRoleStore) Get(ctx context.Context, ws, name string) (*domain.Role, error) {
	return traced(ctx, "Roles", "Get", func(ctx context.Context) (*domain.Role, error) {
		return t.inner.Get(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
}

func (t *tracedRoleStore) List(ctx context.Context, ws string) ([]*domain.Role, error) {
	return tracedList(ctx, "Roles", "List", func(ctx context.Context) ([]*domain.Role, error) {
		return t.inner.List(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRoleStore) Update(ctx context.Context, ws, name string, patch store.RoleUpdate) (*domain.Role, error) {
	return traced(ctx, "Roles", "Update", func(ctx context.Context) (*domain.Role, error) {
		return t.inner.Update(ctx, ws, name, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
}

func (t *tracedRoleStore) Delete(ctx context.Context, ws, name string) error {
	return tracedErr(ctx, "Roles", "Delete", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
}

// --- NodeStore ---

type tracedNodeStore struct{ inner store.NodeStore }

func (t *tracedNodeStore) Create(ctx context.Context, in store.NodeCreate) (*domain.Node, error) {
	return traced(ctx, "Nodes", "Create", func(ctx context.Context) (*domain.Node, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedNodeStore) Get(ctx context.Context, ws, nodeID string) (*domain.Node, error) {
	return traced(ctx, "Nodes", "Get", func(ctx context.Context) (*domain.Node, error) {
		return t.inner.Get(ctx, ws, nodeID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedNodeStore) List(ctx context.Context, ws string) ([]*domain.Node, error) {
	return tracedList(ctx, "Nodes", "List", func(ctx context.Context) ([]*domain.Node, error) {
		return t.inner.List(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedNodeStore) Heartbeat(ctx context.Context, ws, nodeID string, ttl time.Duration) (*domain.Node, error) {
	return traced(ctx, "Nodes", "Heartbeat", func(ctx context.Context) (*domain.Node, error) {
		return t.inner.Heartbeat(ctx, ws, nodeID, ttl)
	},
		attribute.String("loom.workspace", ws),
		attribute.Int64("ttl_ms", ttl.Milliseconds()),
	)
}

func (t *tracedNodeStore) Update(ctx context.Context, ws, nodeID string, patch store.NodeUpdate) (*domain.Node, error) {
	return traced(ctx, "Nodes", "Update", func(ctx context.Context) (*domain.Node, error) {
		return t.inner.Update(ctx, ws, nodeID, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- AgentSessionStore ---

type tracedAgentSessionStore struct{ inner store.AgentSessionStore }

func (t *tracedAgentSessionStore) Create(ctx context.Context, in store.AgentSessionCreate) (*domain.AgentSession, error) {
	return traced(ctx, "AgentSessions", "Create", func(ctx context.Context) (*domain.AgentSession, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedAgentSessionStore) Get(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	return traced(ctx, "AgentSessions", "Get", func(ctx context.Context) (*domain.AgentSession, error) {
		return t.inner.Get(ctx, ws, sessionID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentSessionStore) List(ctx context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	return tracedList(ctx, "AgentSessions", "List", func(ctx context.Context) ([]*domain.AgentSession, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentSessionStore) Heartbeat(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	return traced(ctx, "AgentSessions", "Heartbeat", func(ctx context.Context) (*domain.AgentSession, error) {
		return t.inner.Heartbeat(ctx, ws, sessionID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentSessionStore) Update(ctx context.Context, ws, sessionID string, patch store.AgentSessionUpdate) (*domain.AgentSession, error) {
	return traced(ctx, "AgentSessions", "Update", func(ctx context.Context) (*domain.AgentSession, error) {
		return t.inner.Update(ctx, ws, sessionID, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- TerminalSessionStore ---

type tracedTerminalSessionStore struct{ inner store.TerminalSessionStore }

func (t *tracedTerminalSessionStore) Create(ctx context.Context, in store.TerminalSessionCreate) (*domain.TerminalSession, error) {
	return traced(ctx, "TerminalSessions", "Create", func(ctx context.Context) (*domain.TerminalSession, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedTerminalSessionStore) Get(ctx context.Context, ws, terminalID string) (*domain.TerminalSession, error) {
	return traced(ctx, "TerminalSessions", "Get", func(ctx context.Context) (*domain.TerminalSession, error) {
		return t.inner.Get(ctx, ws, terminalID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTerminalSessionStore) List(ctx context.Context, ws string, filter store.TerminalSessionFilter) ([]*domain.TerminalSession, error) {
	return tracedList(ctx, "TerminalSessions", "List", func(ctx context.Context) ([]*domain.TerminalSession, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTerminalSessionStore) Update(ctx context.Context, ws, terminalID string, patch store.TerminalSessionUpdate) (*domain.TerminalSession, error) {
	return traced(ctx, "TerminalSessions", "Update", func(ctx context.Context) (*domain.TerminalSession, error) {
		return t.inner.Update(ctx, ws, terminalID, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- ArtifactStore ---

type tracedArtifactStore struct{ inner store.ArtifactStore }

func (t *tracedArtifactStore) Create(ctx context.Context, in store.ArtifactCreate) (*domain.Artifact, error) {
	return traced(ctx, "Artifacts", "Create", func(ctx context.Context) (*domain.Artifact, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedArtifactStore) Get(ctx context.Context, ws, artifactID string) (*domain.Artifact, error) {
	return traced(ctx, "Artifacts", "Get", func(ctx context.Context) (*domain.Artifact, error) {
		return t.inner.Get(ctx, ws, artifactID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedArtifactStore) List(ctx context.Context, ws string, filter store.ArtifactFilter) ([]*domain.Artifact, error) {
	return tracedList(ctx, "Artifacts", "List", func(ctx context.Context) ([]*domain.Artifact, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedArtifactStore) UploadContent(ctx context.Context, ws, artifactID string, upload store.ArtifactContentUpload) (*domain.Artifact, error) {
	return traced(ctx, "Artifacts", "UploadContent", func(ctx context.Context) (*domain.Artifact, error) {
		return t.inner.UploadContent(ctx, ws, artifactID, upload)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedArtifactStore) Finalize(ctx context.Context, ws, artifactID string, finalize store.ArtifactFinalize) (*domain.Artifact, error) {
	return traced(ctx, "Artifacts", "Finalize", func(ctx context.Context) (*domain.Artifact, error) {
		return t.inner.Finalize(ctx, ws, artifactID, finalize)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedArtifactStore) Update(ctx context.Context, ws, artifactID string, patch store.ArtifactUpdate) (*domain.Artifact, error) {
	return traced(ctx, "Artifacts", "Update", func(ctx context.Context) (*domain.Artifact, error) {
		return t.inner.Update(ctx, ws, artifactID, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- AgentLeaseStore ---

type tracedAgentLeaseStore struct{ inner store.AgentLeaseStore }

func (t *tracedAgentLeaseStore) Create(ctx context.Context, in store.AgentLeaseCreate) (*domain.AgentLease, error) {
	return traced(ctx, "AgentLeases", "Create", func(ctx context.Context) (*domain.AgentLease, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedAgentLeaseStore) Get(ctx context.Context, ws, leaseID string) (*domain.AgentLease, error) {
	return traced(ctx, "AgentLeases", "Get", func(ctx context.Context) (*domain.AgentLease, error) {
		return t.inner.Get(ctx, ws, leaseID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentLeaseStore) List(ctx context.Context, ws string, filter store.AgentLeaseFilter) ([]*domain.AgentLease, error) {
	return tracedList(ctx, "AgentLeases", "List", func(ctx context.Context) ([]*domain.AgentLease, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentLeaseStore) Heartbeat(ctx context.Context, ws, leaseID, token string, ttl time.Duration) (*domain.AgentLease, error) {
	return traced(ctx, "AgentLeases", "Heartbeat", func(ctx context.Context) (*domain.AgentLease, error) {
		return t.inner.Heartbeat(ctx, ws, leaseID, token, ttl)
	},
		attribute.String("loom.workspace", ws),
		attribute.Int64("ttl_ms", ttl.Milliseconds()),
	)
}

func (t *tracedAgentLeaseStore) Release(ctx context.Context, ws, leaseID, token string) (*domain.AgentLease, error) {
	return traced(ctx, "AgentLeases", "Release", func(ctx context.Context) (*domain.AgentLease, error) {
		return t.inner.Release(ctx, ws, leaseID, token)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- AgentOwnershipLeaseStore ---

type tracedAgentOwnershipLeaseStore struct {
	inner store.AgentOwnershipLeaseStore
}

func (t *tracedAgentOwnershipLeaseStore) Acquire(ctx context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	return traced(ctx, "AgentOwnershipLeases", "Acquire", func(ctx context.Context) (*domain.AgentOwnershipLease, error) {
		return t.inner.Acquire(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedAgentOwnershipLeaseStore) Get(ctx context.Context, ws, agentID string) (*domain.AgentOwnershipLease, error) {
	return traced(ctx, "AgentOwnershipLeases", "Get", func(ctx context.Context) (*domain.AgentOwnershipLease, error) {
		return t.inner.Get(ctx, ws, agentID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentOwnershipLeaseStore) List(ctx context.Context, ws string, filter store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	return tracedList(ctx, "AgentOwnershipLeases", "List", func(ctx context.Context) ([]*domain.AgentOwnershipLease, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentOwnershipLeaseStore) Heartbeat(ctx context.Context, ws, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error) {
	return traced(ctx, "AgentOwnershipLeases", "Heartbeat", func(ctx context.Context) (*domain.AgentOwnershipLease, error) {
		return t.inner.Heartbeat(ctx, ws, agentID, token, ttl)
	},
		attribute.String("loom.workspace", ws),
		attribute.Int64("ttl_ms", ttl.Milliseconds()),
	)
}

func (t *tracedAgentOwnershipLeaseStore) Release(ctx context.Context, ws, agentID, token string) (*domain.AgentOwnershipLease, error) {
	return traced(ctx, "AgentOwnershipLeases", "Release", func(ctx context.Context) (*domain.AgentOwnershipLease, error) {
		return t.inner.Release(ctx, ws, agentID, token)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- AgentCommandStore ---

type tracedAgentCommandStore struct{ inner store.AgentCommandStore }

func (t *tracedAgentCommandStore) Create(ctx context.Context, in store.AgentCommandCreate) (*domain.AgentCommand, error) {
	return traced(ctx, "AgentCommands", "Create", func(ctx context.Context) (*domain.AgentCommand, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedAgentCommandStore) Get(ctx context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	return traced(ctx, "AgentCommands", "Get", func(ctx context.Context) (*domain.AgentCommand, error) {
		return t.inner.Get(ctx, ws, commandID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentCommandStore) List(ctx context.Context, ws string, filter store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	return tracedList(ctx, "AgentCommands", "List", func(ctx context.Context) ([]*domain.AgentCommand, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentCommandStore) Ack(ctx context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	return traced(ctx, "AgentCommands", "Ack", func(ctx context.Context) (*domain.AgentCommand, error) {
		return t.inner.Ack(ctx, ws, commandID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentCommandStore) Complete(ctx context.Context, ws, commandID string, update store.AgentCommandComplete) (*domain.AgentCommand, error) {
	return traced(ctx, "AgentCommands", "Complete", func(ctx context.Context) (*domain.AgentCommand, error) {
		return t.inner.Complete(ctx, ws, commandID, update)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- AgentInboxMessageStore ---

type tracedAgentInboxMessageStore struct{ inner store.AgentInboxMessageStore }

func (t *tracedAgentInboxMessageStore) Create(ctx context.Context, in store.AgentInboxMessageCreate) (*domain.AgentInboxMessage, error) {
	return traced(ctx, "AgentInboxMessages", "Create", func(ctx context.Context) (*domain.AgentInboxMessage, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedAgentInboxMessageStore) Get(ctx context.Context, ws, inboxMessageID string) (*domain.AgentInboxMessage, error) {
	return traced(ctx, "AgentInboxMessages", "Get", func(ctx context.Context) (*domain.AgentInboxMessage, error) {
		return t.inner.Get(ctx, ws, inboxMessageID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentInboxMessageStore) List(ctx context.Context, ws string, filter store.AgentInboxMessageFilter) ([]*domain.AgentInboxMessage, error) {
	return tracedList(ctx, "AgentInboxMessages", "List", func(ctx context.Context) ([]*domain.AgentInboxMessage, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentInboxMessageStore) ClaimNext(ctx context.Context, in store.AgentInboxMessageClaim) (*domain.AgentInboxMessage, error) {
	return traced(ctx, "AgentInboxMessages", "ClaimNext", func(ctx context.Context) (*domain.AgentInboxMessage, error) {
		return t.inner.ClaimNext(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedAgentInboxMessageStore) Complete(ctx context.Context, ws, inboxMessageID string, update store.AgentInboxMessageComplete) (*domain.AgentInboxMessage, error) {
	return traced(ctx, "AgentInboxMessages", "Complete", func(ctx context.Context) (*domain.AgentInboxMessage, error) {
		return t.inner.Complete(ctx, ws, inboxMessageID, update)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- DriverStore ---

type tracedDriverStore struct{ inner store.DriverStore }

func (t *tracedDriverStore) Create(ctx context.Context, in store.DriverCreate) (*domain.Driver, error) {
	return traced(ctx, "Drivers", "Create", func(ctx context.Context) (*domain.Driver, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedDriverStore) Get(ctx context.Context, ws, driverID string) (*domain.Driver, error) {
	return traced(ctx, "Drivers", "Get", func(ctx context.Context) (*domain.Driver, error) {
		return t.inner.Get(ctx, ws, driverID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStore) List(ctx context.Context, ws string, filter store.DriverFilter) ([]*domain.Driver, error) {
	return tracedList(ctx, "Drivers", "List", func(ctx context.Context) ([]*domain.Driver, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStore) Update(ctx context.Context, ws, driverID string, patch store.DriverUpdate) (*domain.Driver, error) {
	return traced(ctx, "Drivers", "Update", func(ctx context.Context) (*domain.Driver, error) {
		return t.inner.Update(ctx, ws, driverID, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- DriverVersionStore ---

type tracedDriverVersionStore struct{ inner store.DriverVersionStore }

func (t *tracedDriverVersionStore) Create(ctx context.Context, in store.DriverVersionCreate) (*domain.DriverVersion, error) {
	return traced(ctx, "DriverVersions", "Create", func(ctx context.Context) (*domain.DriverVersion, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedDriverVersionStore) Get(ctx context.Context, ws, versionID string) (*domain.DriverVersion, error) {
	return traced(ctx, "DriverVersions", "Get", func(ctx context.Context) (*domain.DriverVersion, error) {
		return t.inner.Get(ctx, ws, versionID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverVersionStore) List(ctx context.Context, ws string, filter store.DriverVersionFilter) ([]*domain.DriverVersion, error) {
	return tracedList(ctx, "DriverVersions", "List", func(ctx context.Context) ([]*domain.DriverVersion, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- WorkerProfileStore ---

type tracedWorkerProfileStore struct{ inner store.WorkerProfileStore }

func (t *tracedWorkerProfileStore) Create(ctx context.Context, in store.WorkerProfileCreate) (*domain.WorkerProfile, error) {
	return traced(ctx, "WorkerProfiles", "Create", func(ctx context.Context) (*domain.WorkerProfile, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.profile", in.ProfileID),
	)
}

func (t *tracedWorkerProfileStore) Get(ctx context.Context, ws, profileID string) (*domain.WorkerProfile, error) {
	return traced(ctx, "WorkerProfiles", "Get", func(ctx context.Context) (*domain.WorkerProfile, error) {
		return t.inner.Get(ctx, ws, profileID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
}

func (t *tracedWorkerProfileStore) List(ctx context.Context, ws string, filter store.WorkerProfileFilter) ([]*domain.WorkerProfile, error) {
	return tracedList(ctx, "WorkerProfiles", "List", func(ctx context.Context) ([]*domain.WorkerProfile, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedWorkerProfileStore) Update(ctx context.Context, ws, profileID string, patch store.WorkerProfileUpdate) (*domain.WorkerProfile, error) {
	return traced(ctx, "WorkerProfiles", "Update", func(ctx context.Context) (*domain.WorkerProfile, error) {
		return t.inner.Update(ctx, ws, profileID, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
}

func (t *tracedWorkerProfileStore) Delete(ctx context.Context, ws, profileID string) error {
	return tracedErr(ctx, "WorkerProfiles", "Delete", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, profileID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.profile", profileID),
	)
}

// --- AgentServiceStore ---

type tracedAgentServiceStore struct{ inner store.AgentServiceStore }

func (t *tracedAgentServiceStore) Create(ctx context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
	return traced(ctx, "AgentServices", "Create", func(ctx context.Context) (*domain.AgentService, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.service", in.ServiceID),
	)
}

func (t *tracedAgentServiceStore) Get(ctx context.Context, ws, serviceID string) (*domain.AgentService, error) {
	return traced(ctx, "AgentServices", "Get", func(ctx context.Context) (*domain.AgentService, error) {
		return t.inner.Get(ctx, ws, serviceID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
}

func (t *tracedAgentServiceStore) List(ctx context.Context, ws string, filter store.AgentServiceFilter) ([]*domain.AgentService, error) {
	return tracedList(ctx, "AgentServices", "List", func(ctx context.Context) ([]*domain.AgentService, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentServiceStore) Update(ctx context.Context, ws, serviceID string, patch store.AgentServiceUpdate) (*domain.AgentService, error) {
	return traced(ctx, "AgentServices", "Update", func(ctx context.Context) (*domain.AgentService, error) {
		return t.inner.Update(ctx, ws, serviceID, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
}

func (t *tracedAgentServiceStore) Delete(ctx context.Context, ws, serviceID string) error {
	return tracedErr(ctx, "AgentServices", "Delete", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, serviceID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.service", serviceID),
	)
}

// --- TriggerBindingStore ---

type tracedTriggerBindingStore struct{ inner store.TriggerBindingStore }

func (t *tracedTriggerBindingStore) Create(ctx context.Context, in store.TriggerBindingCreate) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "Create", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.binding", in.BindingID),
		attribute.String("loom.route_key", in.RouteKey),
	)
}

func (t *tracedTriggerBindingStore) Get(ctx context.Context, ws, bindingID string) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "Get", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.Get(ctx, ws, bindingID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
}

func (t *tracedTriggerBindingStore) GetByRouteKey(ctx context.Context, ws, routeKey string) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "GetByRouteKey", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.GetByRouteKey(ctx, ws, routeKey)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.route_key", routeKey),
	)
}

func (t *tracedTriggerBindingStore) List(ctx context.Context, ws string, filter store.TriggerBindingFilter) ([]*domain.TriggerBinding, error) {
	return tracedList(ctx, "TriggerBindings", "List", func(ctx context.Context) ([]*domain.TriggerBinding, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTriggerBindingStore) Update(ctx context.Context, ws, bindingID string, patch store.TriggerBindingUpdate) (*domain.TriggerBinding, error) {
	return traced(ctx, "TriggerBindings", "Update", func(ctx context.Context) (*domain.TriggerBinding, error) {
		return t.inner.Update(ctx, ws, bindingID, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
}

// --- DriverRunStore ---

type tracedDriverRunStore struct{ inner store.DriverRunStore }

func (t *tracedDriverRunStore) Create(ctx context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Create", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedDriverRunStore) CreateEpic(ctx context.Context, ws, epicID string, in store.EpicRunCreate) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "CreateEpic", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.CreateEpic(ctx, ws, epicID, in)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.epic", epicID),
	)
}

func (t *tracedDriverRunStore) Get(ctx context.Context, ws, runID string) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Get", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Get(ctx, ws, runID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverRunStore) List(ctx context.Context, ws string, filter store.DriverRunFilter) ([]*domain.DriverRun, error) {
	return tracedList(ctx, "DriverRuns", "List", func(ctx context.Context) ([]*domain.DriverRun, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverRunStore) Events(ctx context.Context, ws, runID, after string, limit int) (*domain.PlatformEventsPage, error) {
	ctx, span := startStoreSpan(ctx, "DriverRuns", "Events",
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run", runID),
	)
	reader, ok := t.inner.(store.DriverRunEventsReader)
	if !ok {
		finish(span, store.ErrDriverRunEventsUnavailable)
		return nil, store.ErrDriverRunEventsUnavailable
	}
	out, err := reader.Events(ctx, ws, runID, after, limit)
	if err == nil && out != nil {
		span.SetAttributes(attribute.Int("result.count", len(out.Events)))
	}
	finish(span, err)
	return out, err
}

func (t *tracedDriverRunStore) Claim(ctx context.Context, ws, runID, nodeID, leaseID string) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Claim", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Claim(ctx, ws, runID, nodeID, leaseID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverRunStore) Heartbeat(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Heartbeat", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Heartbeat(ctx, ws, runID, nodeID, leaseID, fencingToken)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverRunStore) Finish(ctx context.Context, ws, runID string, finishRun store.DriverRunFinish) (*domain.DriverRun, error) {
	return traced(ctx, "DriverRuns", "Finish", func(ctx context.Context) (*domain.DriverRun, error) {
		return t.inner.Finish(ctx, ws, runID, finishRun)
	},
		attribute.String("loom.workspace", ws),
	)
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
	return traced(ctx, "DriverSteps", "Create", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.driver_run_id", in.DriverRunID),
	)
}

func (t *tracedDriverStepStore) CreateForRun(ctx context.Context, ws, runID string, in store.DriverStepCreate) (*domain.DriverStep, error) {
	return traced(ctx, "DriverSteps", "CreateForRun", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.CreateForRun(ctx, ws, runID, in)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
}

func (t *tracedDriverStepStore) Get(ctx context.Context, ws, stepID string) (*domain.DriverStep, error) {
	return traced(ctx, "DriverSteps", "Get", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.Get(ctx, ws, stepID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStepStore) List(ctx context.Context, ws string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	return tracedList(ctx, "DriverSteps", "List", func(ctx context.Context) ([]*domain.DriverStep, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDriverStepStore) ListForRun(ctx context.Context, ws, runID string, filter store.DriverStepFilter) ([]*domain.DriverStep, error) {
	return tracedList(ctx, "DriverSteps", "ListForRun", func(ctx context.Context) ([]*domain.DriverStep, error) {
		return t.inner.ListForRun(ctx, ws, runID, filter)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.driver_run_id", runID),
	)
}

func (t *tracedDriverStepStore) Update(ctx context.Context, ws, stepID string, update store.DriverStepUpdate) (*domain.DriverStep, error) {
	return traced(ctx, "DriverSteps", "Update", func(ctx context.Context) (*domain.DriverStep, error) {
		return t.inner.Update(ctx, ws, stepID, update)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- TaskRunStore ---

type tracedTaskRunStore struct{ inner store.TaskRunStore }

func (t *tracedTaskRunStore) Create(ctx context.Context, in store.TaskRunCreate) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Create", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedTaskRunStore) ClaimQueued(ctx context.Context, ws string, claim store.TaskRunClaim) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "ClaimQueued", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.ClaimQueued(ctx, ws, claim)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Get(ctx context.Context, ws, taskRunID string) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Get", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Get(ctx, ws, taskRunID)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) List(ctx context.Context, ws string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	return tracedList(ctx, "TaskRuns", "List", func(ctx context.Context) ([]*domain.TaskRun, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Heartbeat(ctx context.Context, ws, taskRunID string, heartbeat store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Heartbeat", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Heartbeat(ctx, ws, taskRunID, heartbeat)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Finish(ctx context.Context, ws, taskRunID string, taskFinish store.TaskRunFinish) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Finish", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Finish(ctx, ws, taskRunID, taskFinish)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) Complete(ctx context.Context, ws, taskRunID string, taskComplete store.TaskRunComplete) (*domain.TaskRun, error) {
	return traced(ctx, "TaskRuns", "Complete", func(ctx context.Context) (*domain.TaskRun, error) {
		return t.inner.Complete(ctx, ws, taskRunID, taskComplete)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) AppendLog(ctx context.Context, ws, taskRunID string, appendLog store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	return traced(ctx, "TaskRuns", "AppendLog", func(ctx context.Context) (*domain.TaskRunLogEntry, error) {
		return t.inner.AppendLog(ctx, ws, taskRunID, appendLog)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedTaskRunStore) ListLogs(ctx context.Context, ws, taskRunID string, filter store.TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error) {
	return tracedList(ctx, "TaskRuns", "ListLogs", func(ctx context.Context) ([]*domain.TaskRunLogEntry, error) {
		return t.inner.ListLogs(ctx, ws, taskRunID, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- DaemonProfileStore ---

type tracedDaemonStore struct{ inner store.DaemonProfileStore }

func (t *tracedDaemonStore) Get(ctx context.Context, ws string) (*domain.DaemonProfile, error) {
	return traced(ctx, "Daemon", "Get", func(ctx context.Context) (*domain.DaemonProfile, error) {
		return t.inner.Get(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedDaemonStore) Upsert(ctx context.Context, profile *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	ws := ""
	if profile != nil {
		ws = profile.WorkspaceKey
	}
	return traced(ctx, "Daemon", "Upsert", func(ctx context.Context) (*domain.DaemonProfile, error) {
		return t.inner.Upsert(ctx, profile)
	},
		attribute.String("loom.workspace", ws),
	)
}
