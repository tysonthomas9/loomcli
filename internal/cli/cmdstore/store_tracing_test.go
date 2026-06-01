package cmdstore

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestWrapStoreWithTracing_Smoke exercises every traced substore method so
// the span-decorator paths are reached. We don't assert behavior — the
// underlying memstore already has its own coverage; this is a guard
// against the tracing wrapper drifting from the store.Store interface and
// against any panic in the wrapper itself.
//
// Errors from the inner store are intentionally ignored: most methods
// either create resources we then read, or are called on resources the
// preceding step created. A few are called on missing keys to also
// exercise the "record error" branch in the wrapper.
func TestWrapStoreWithTracing_Smoke(t *testing.T) {
	inner := memstore.New()
	wrapped := WrapStoreWithTracing(inner)
	if wrapped == nil {
		t.Fatal("WrapStoreWithTracing returned nil for non-nil input")
	}

	// Accessors return their cached sub-store; should be non-nil and stable.
	if wrapped.Workspaces() == nil || wrapped.Workspaces() != wrapped.Workspaces() {
		t.Error("Workspaces accessor unstable")
	}
	_ = wrapped.Repos()
	_ = wrapped.Agents()
	_ = wrapped.Nodes()
	_ = wrapped.AgentSessions()
	_ = wrapped.AgentSessionOperations()
	_ = wrapped.TerminalSessions()
	_ = wrapped.Artifacts()
	_ = wrapped.AgentLeases()
	_ = wrapped.AgentOwnershipLeases()
	_ = wrapped.AgentCommands()
	_ = wrapped.Roles()
	_ = wrapped.Daemon()

	ctx := context.Background()
	ws := wrapped.Workspaces()
	if _, err := ws.Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test", DefaultBranch: "main"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	_, _ = ws.Get(ctx, "TEST")
	_, _ = ws.GetByName(ctx, "test")
	_, _ = ws.List(ctx)
	_, _ = ws.Update(ctx, "TEST", store.WorkspaceUpdate{})
	// Error path on a missing key.
	_, _ = ws.Get(ctx, "missing")

	repos := wrapped.Repos()
	if _, err := repos.Create(ctx, store.RepoCreate{WorkspaceKey: "TEST", Name: "repo", DefaultBranch: "main", SourceRepoID: "repo"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	_, _ = repos.Get(ctx, "TEST", "repo")
	_, _ = repos.List(ctx, "TEST")
	_, _ = repos.Update(ctx, "TEST", "repo", store.RepoUpdate{})

	agents := wrapped.Agents()
	_, _ = agents.Create(ctx, store.AgentCreate{WorkspaceKey: "TEST", Name: "agent", RoleName: "worker"})
	_, _ = agents.Get(ctx, "TEST", "agent")
	_, _ = agents.List(ctx, "TEST")
	_, _ = agents.Update(ctx, "TEST", "agent", store.AgentUpdate{})

	roles := wrapped.Roles()
	_, _ = roles.Create(ctx, store.RoleCreate{WorkspaceKey: "TEST", Name: "worker"})
	_, _ = roles.Get(ctx, "TEST", "worker")
	_, _ = roles.List(ctx, "TEST")
	_, _ = roles.Update(ctx, "TEST", "worker", store.RoleUpdate{})

	nodes := wrapped.Nodes()
	_, _ = nodes.Create(ctx, store.NodeCreate{WorkspaceKey: "TEST", NodeID: "node-1", OwnerActor: "test", RuntimeProvider: domain.RuntimeProviderLocal, TTL: time.Minute})
	_, _ = nodes.Get(ctx, "TEST", "node-1")
	_, _ = nodes.List(ctx, "TEST")
	_, _ = nodes.Heartbeat(ctx, "TEST", "node-1", time.Minute)

	sessions := wrapped.AgentSessions()
	_, _ = sessions.Create(ctx, store.AgentSessionCreate{WorkspaceKey: "TEST", SessionID: "sess-1", AgentID: "agent"})
	_, _ = sessions.Get(ctx, "TEST", "sess-1")
	_, _ = sessions.List(ctx, "TEST", store.AgentSessionFilter{})
	_, _ = sessions.Heartbeat(ctx, "TEST", "sess-1")
	_, _ = sessions.Update(ctx, "TEST", "sess-1", store.AgentSessionUpdate{})

	sessionOps := wrapped.AgentSessionOperations()
	_, _ = sessionOps.Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey: "TEST",
		OperationID:  "op-1",
		SessionID:    "sess-1",
		AgentID:      "agent",
		Kind:         "prompt",
		Status:       domain.AgentSessionOperationCompleted,
	})
	_, _ = sessionOps.Get(ctx, "TEST", "op-1")
	_, _ = sessionOps.List(ctx, "TEST", store.AgentSessionOperationFilter{SessionID: "sess-1"})

	toolCalls := wrapped.AgentSessionToolCalls()
	_, _ = toolCalls.Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey: "TEST",
		CallID:       "call-1",
		OperationID:  "op-1",
		SessionID:    "sess-1",
		AgentID:      "agent",
		Name:         "lookup",
		Status:       "completed",
	})
	_, _ = toolCalls.Get(ctx, "TEST", "call-1")
	_, _ = toolCalls.List(ctx, "TEST", store.AgentSessionToolCallFilter{OperationID: "op-1"})

	terms := wrapped.TerminalSessions()
	_, _ = terms.Create(ctx, store.TerminalSessionCreate{WorkspaceKey: "TEST", TerminalID: "term-1", AgentID: "agent"})
	_, _ = terms.Get(ctx, "TEST", "term-1")
	_, _ = terms.List(ctx, "TEST", store.TerminalSessionFilter{})
	_, _ = terms.Update(ctx, "TEST", "term-1", store.TerminalSessionUpdate{})

	artifacts := wrapped.Artifacts()
	_, _ = artifacts.Create(ctx, store.ArtifactCreate{WorkspaceKey: "TEST", ArtifactID: "art-1", Type: "log"})
	_, _ = artifacts.Get(ctx, "TEST", "art-1")
	_, _ = artifacts.List(ctx, "TEST", store.ArtifactFilter{})
	_, _ = artifacts.Update(ctx, "TEST", "art-1", store.ArtifactUpdate{})

	leases := wrapped.AgentLeases()
	_, _ = leases.Create(ctx, store.AgentLeaseCreate{WorkspaceKey: "TEST", SessionID: "sess-1", LeaseID: "lease-1", AgentID: "agent", NodeID: "node-1", TTL: time.Minute})
	_, _ = leases.Get(ctx, "TEST", "lease-1")
	_, _ = leases.List(ctx, "TEST", store.AgentLeaseFilter{})
	_, _ = leases.Heartbeat(ctx, "TEST", "lease-1", "tok", time.Minute)
	_, _ = leases.Release(ctx, "TEST", "lease-1", "tok")

	owns := wrapped.AgentOwnershipLeases()
	_, _ = owns.Acquire(ctx, store.AgentOwnershipLeaseAcquire{WorkspaceKey: "TEST", AgentID: "agent", LeaseID: "ownlease-1", OwnerID: "owner", NodeID: "node-1", TTL: time.Minute})
	_, _ = owns.Get(ctx, "TEST", "agent")
	_, _ = owns.List(ctx, "TEST", store.AgentOwnershipLeaseFilter{})
	_, _ = owns.Heartbeat(ctx, "TEST", "agent", "tok", time.Minute)
	_, _ = owns.Release(ctx, "TEST", "agent", "tok")

	cmds := wrapped.AgentCommands()
	_, _ = cmds.Create(ctx, store.AgentCommandCreate{WorkspaceKey: "TEST", CommandID: "cmd-1", TargetAgentID: "agent", Type: "noop"})
	_, _ = cmds.Get(ctx, "TEST", "cmd-1")
	_, _ = cmds.List(ctx, "TEST", store.AgentCommandFilter{})
	_, _ = cmds.Ack(ctx, "TEST", "cmd-1")
	_, _ = cmds.Complete(ctx, "TEST", "cmd-1", store.AgentCommandComplete{Status: domain.AgentCommandSucceeded})

	daemon := wrapped.Daemon()
	_, _ = daemon.Get(ctx, "TEST")
	_, _ = daemon.Upsert(ctx, &domain.DaemonProfile{WorkspaceKey: "TEST"})

	// Cleanup paths.
	_ = roles.Delete(ctx, "TEST", "worker")
	_ = agents.Delete(ctx, "TEST", "agent")
	_ = repos.Delete(ctx, "TEST", "repo")
	_ = ws.Delete(ctx, "TEST")

	if err := wrapped.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestWrapStoreWithTracing_Nil(t *testing.T) {
	if got := WrapStoreWithTracing(nil); got != nil {
		t.Errorf("WrapStoreWithTracing(nil) = %v, want nil", got)
	}
}
