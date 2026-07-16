package cmdstore

// Traced wrappers for the control-plane stores (nodes, sessions, artifacts,
// leases, commands, inbox messages, workers), mirroring
// internal/store/control_plane_store.go. Shared span helpers live in
// store_tracing.go.

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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

// ReadContent forwards to the inner store when it can read artifact bytes back, so a
// serve node can surface a transcript artifact another node uploaded (the cross-node
// transcript_ref path). Without this, the wrapper would hide ArtifactContentReader and
// the read would fall back to the (node-local) artifact URI. Returns ErrNotFound when
// the inner store cannot read content, so callers fall back to the URI as before.
func (t *tracedArtifactStore) ReadContent(ctx context.Context, ws, artifactID string) ([]byte, error) {
	return traced(ctx, "Artifacts", "ReadContent", func(ctx context.Context) ([]byte, error) {
		reader, ok := t.inner.(store.ArtifactContentReader)
		if !ok {
			return nil, domain.ErrNotFound
		}
		return reader.ReadContent(ctx, ws, artifactID)
	},
		attribute.String("loom.workspace", ws),
	)
}

// The tracing wrapper must keep satisfying ArtifactContentReader, else cross-node
// transcript reads silently regress to the node-local URI fallback.
var _ store.ArtifactContentReader = (*tracedArtifactStore)(nil)

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

// --- WorkerStore ---

type tracedWorkerStore struct{ inner store.WorkerStore }

func (t *tracedWorkerStore) Heartbeat(ctx context.Context, ws, workerID string) error {
	ctx, span := startStoreSpan(ctx, "Workers", "Heartbeat",
		attribute.String("loom.workspace", ws),
	)
	err := t.inner.Heartbeat(ctx, ws, workerID)
	finish(span, err)
	return err
}

func (t *tracedWorkerStore) Deregister(ctx context.Context, ws, workerID string) error {
	ctx, span := startStoreSpan(ctx, "Workers", "Deregister",
		attribute.String("loom.workspace", ws),
	)
	err := t.inner.Deregister(ctx, ws, workerID)
	finish(span, err)
	return err
}
