package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPollAgentCommands_UsesFreshContextPerCommand(t *testing.T) {
	origTimeout := agentCommandPollTimeout
	agentCommandPollTimeout = 20 * time.Millisecond
	t.Cleanup(func() { agentCommandPollTimeout = origTimeout })

	cmdStore := &blockingAgentCommandStore{}
	d := &Daemon{
		store: commandPollerTestStore{commands: cmdStore},
		sup: &supervisor.Supervisor{
			WorkspaceID: "ws-1",
			NodeID:      "node-1",
			Shutdown:    make(chan struct{}),
		},
	}

	d.pollAgentCommands()

	if cmdStore.secondAckExpired {
		t.Fatal("second command inherited an expired context from the first command")
	}
	if cmdStore.ackCalls != 2 {
		t.Fatalf("ack calls = %d, want 2", cmdStore.ackCalls)
	}
}

type blockingAgentCommandStore struct {
	mu               sync.Mutex
	ackCalls         int
	secondAckExpired bool
}

func (s *blockingAgentCommandStore) Create(context.Context, store.AgentCommandCreate) (*domain.AgentCommand, error) {
	return nil, nil
}

func (s *blockingAgentCommandStore) Get(context.Context, string, string) (*domain.AgentCommand, error) {
	return nil, nil
}

func (s *blockingAgentCommandStore) List(context.Context, string, store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	return []*domain.AgentCommand{
		{WorkspaceKey: "ws-1", CommandID: "cmd-1", TargetNodeID: "node-1", Type: "unsupported", Status: domain.AgentCommandQueued},
		{WorkspaceKey: "ws-1", CommandID: "cmd-2", TargetNodeID: "node-1", Type: "unsupported", Status: domain.AgentCommandQueued},
	}, nil
}

func (s *blockingAgentCommandStore) Ack(ctx context.Context, _, commandID string) (*domain.AgentCommand, error) {
	s.mu.Lock()
	s.ackCalls++
	call := s.ackCalls
	s.mu.Unlock()

	if call == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	s.mu.Lock()
	s.secondAckExpired = ctx.Err() != nil
	s.mu.Unlock()
	return &domain.AgentCommand{WorkspaceKey: "ws-1", CommandID: commandID, Status: domain.AgentCommandAcked}, nil
}

func (s *blockingAgentCommandStore) Complete(context.Context, string, string, store.AgentCommandComplete) (*domain.AgentCommand, error) {
	return &domain.AgentCommand{WorkspaceKey: "ws-1", Status: domain.AgentCommandSucceeded}, nil
}

type commandPollerTestStore struct {
	commands store.AgentCommandStore
}

func (s commandPollerTestStore) Workspaces() store.WorkspaceStore { return nil }
func (s commandPollerTestStore) Repos() store.RepoStore           { return nil }
func (s commandPollerTestStore) Agents() store.AgentStore         { return nil }
func (s commandPollerTestStore) Nodes() store.NodeStore           { return nil }
func (s commandPollerTestStore) AgentSessions() store.AgentSessionStore {
	return nil
}
func (s commandPollerTestStore) TerminalSessions() store.TerminalSessionStore {
	return nil
}
func (s commandPollerTestStore) Artifacts() store.ArtifactStore { return nil }
func (s commandPollerTestStore) AgentLeases() store.AgentLeaseStore {
	return nil
}
func (s commandPollerTestStore) AgentOwnershipLeases() store.AgentOwnershipLeaseStore {
	return nil
}
func (s commandPollerTestStore) AgentCommands() store.AgentCommandStore {
	return s.commands
}
func (s commandPollerTestStore) Workers() store.WorkerStore       { return nil }
func (s commandPollerTestStore) Roles() store.RoleStore           { return nil }
func (s commandPollerTestStore) Daemon() store.DaemonProfileStore { return nil }
func (s commandPollerTestStore) Close() error                     { return nil }
