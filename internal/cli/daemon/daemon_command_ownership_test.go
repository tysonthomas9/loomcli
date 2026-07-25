package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPrepareAgentCommand_TwoDaemonsOnlyAggregateOwnerCanAck(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-contended",
		TargetAgentID: "worker-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	lease, _ := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")

	daemons := []*Daemon{
		{
			store: commandPollerTestStore{commands: st.AgentCommands()},
			sup: &supervisor.Supervisor{
				WorkspaceID: "ws-1",
				NodeID:      "node-1",
				Shutdown:    make(chan struct{}),
				Agents:      []*supervisor.AgentProcess{commandPollerAgentFromLease(lease)},
			},
		},
		{
			store: commandPollerTestStore{commands: st.AgentCommands()},
			sup: &supervisor.Supervisor{
				WorkspaceID: "ws-1",
				NodeID:      "node-2",
				Shutdown:    make(chan struct{}),
			},
		},
	}
	type result struct {
		node       string
		command    *domain.AgentCommand
		recovering bool
		consumed   bool
	}
	start := make(chan struct{})
	results := make(chan result, len(daemons))
	var wg sync.WaitGroup
	for _, daemon := range daemons {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			snapshot := *command
			prepared, recovering, consumed, authority := daemon.prepareAgentCommand(&snapshot)
			authority.release()
			results <- result{
				node:       daemon.sup.NodeID,
				command:    prepared,
				recovering: recovering,
				consumed:   consumed,
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	losers := 0
	for got := range results {
		if got.command != nil {
			winners++
			if got.recovering || got.consumed {
				t.Fatalf("winner %q result = %+v, want fresh executable command", got.node, got)
			}
			if got.command.TargetNodeID != got.node || got.command.AckedBy != got.node {
				t.Fatalf("winner command = %+v, want target/owner %q", got.command, got.node)
			}
			continue
		}
		losers++
		if got.recovering || got.consumed {
			t.Fatalf("non-owner %q result = %+v, want retryable refusal before Ack", got.node, got)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners/losers = %d/%d, want 1/1", winners, losers)
	}
}

func TestPrepareAgentCommand_RemoteLiveHeadRemainsBeforeSuccessor(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-remote-live-head",
		TargetAgentID: "worker-1",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create first command: %v", err)
	}
	second, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-after-remote-live-head",
		TargetAgentID: "worker-1",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create second command: %v", err)
	}
	remote, err := st.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "ws-1",
		AgentID:      "worker-1",
		NodeID:       "node-remote",
		OwnerID:      "owner-remote",
		TTL:          time.Minute,
	})
	if err != nil {
		t.Fatalf("acquire remote ownership: %v", err)
	}
	cfg := makeDaemonConfig([]AgentEntry{{
		Worktree: "worker-1",
		Role:     "task",
		Mode:     domain.AgentModeService,
	}}, nil)
	d := &Daemon{
		config: cfg,
		store:  st,
		sup: &supervisor.Supervisor{
			WorkspaceID:   "ws-1",
			NodeID:        "node-1",
			ControlStore:  st,
			Shutdown:      make(chan struct{}),
			StoppedAgents: map[string]struct{}{"worker-1": {}},
		},
	}
	d.sup.ConfigSnapshot = d.configSnapshot

	prepared, recovering, consumed, authority := d.prepareAgentCommand(first)
	authority.release()
	if prepared != nil || recovering || consumed {
		t.Fatalf("remote-owned head prepare = command:%+v recovering:%v consumed:%v, want retryable head", prepared, recovering, consumed)
	}
	if _, err := st.AgentOwnershipLeases().Release(ctx, "ws-1", "worker-1", remote.Token); err != nil {
		t.Fatalf("release remote ownership: %v", err)
	}
	prepared, recovering, consumed, authority = d.prepareAgentCommand(first)
	authority.release()
	if prepared == nil || recovering || consumed {
		t.Fatalf("first prepare after release = command:%+v recovering:%v consumed:%v", prepared, recovering, consumed)
	}
	if _, err := st.AgentCommands().Complete(ctx, "ws-1", first.CommandID, store.AgentCommandComplete{
		NodeID:  "node-1",
		OwnerID: "node-1",
		Status:  domain.AgentCommandSucceeded,
	}); err != nil {
		t.Fatalf("complete first command: %v", err)
	}
	prepared, recovering, consumed, authority = d.prepareAgentCommand(second)
	defer authority.release()
	if prepared == nil || recovering || consumed {
		t.Fatalf("successor prepare = command:%+v recovering:%v consumed:%v", prepared, recovering, consumed)
	}
}

func TestAcknowledgedCommandRecoversAcrossProcessNodeReplacement(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	projectDir := t.TempDir()
	oldSupervisor := &supervisor.Supervisor{
		ProjectDir:  projectDir,
		WorkspaceID: "ws-1",
		NodeID:      "node-old",
		Shutdown:    make(chan struct{}),
	}
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-before-restart",
		TargetAgentID: "worker-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	ownerID := mustCommandOwnerID(t, oldSupervisor)
	_, ack := acquireCommandOwnership(t, st, "ws-1", "worker-1", oldSupervisor.NodeID, ownerID)
	command, err = st.AgentCommands().Ack(
		ctx,
		"ws-1",
		command.CommandID,
		ack,
	)
	if err != nil {
		t.Fatalf("ack command on old process: %v", err)
	}

	replacement := &Daemon{
		config: makeDaemonConfig([]AgentEntry{{
			Worktree: "worker-1",
			Role:     "task",
		}}, nil),
		store: commandPollerTestStore{commands: st.AgentCommands()},
		sup: &supervisor.Supervisor{
			ProjectDir:   projectDir,
			WorkspaceID:  "ws-1",
			NodeID:       "node-new",
			Shutdown:     make(chan struct{}),
			ControlStore: st,
			Agents: []*supervisor.AgentProcess{{
				Entry:                 config.AgentEntry{Worktree: "worker-1", Role: "task"},
				LifecycleGenerationAt: time.Now().UTC(),
				OwnershipAcquired:     make(chan struct{}),
			}},
		},
	}
	replacement.sup.ConfigSnapshot = replacement.configSnapshot
	if mustCommandOwnerID(t, replacement.sup) != mustCommandOwnerID(t, oldSupervisor) {
		t.Fatal("replacement did not load stable supervisor command owner")
	}
	pollable, err := replacement.listPollableAgentCommands(ctx)
	if err != nil {
		t.Fatalf("list pollable commands: %v", err)
	}
	if len(pollable) != 1 || pollable[0].CommandID != command.CommandID {
		t.Fatalf("replacement pollable commands = %+v, want acknowledged old-node command", pollable)
	}
	prepared, recovering, consumed, authority := replacement.prepareAgentCommand(pollable[0])
	defer authority.release()
	if prepared == nil || !recovering || consumed {
		t.Fatalf("replacement prepare = command:%+v recovering:%v consumed:%v, want resumable recovery", prepared, recovering, consumed)
	}
	if prepared.TargetNodeID != "node-old" ||
		prepared.AckedBy != mustCommandOwnerID(t, replacement.sup) {
		t.Fatalf("recovered binding = target:%q owner:%q", prepared.TargetNodeID, prepared.AckedBy)
	}
}

func TestAcknowledgedCommandConvergesAfterAgentConfigDeletion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-config-deleted",
		TargetAgentID: "worker-removed",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	_, ack := acquireCommandOwnership(t, st, "ws-1", "worker-removed", "node-1", "node-1")
	command, err = st.AgentCommands().Ack(ctx, "ws-1", command.CommandID, ack)
	if err != nil {
		t.Fatalf("Ack command: %v", err)
	}
	d := &Daemon{
		config: makeDaemonConfig(nil, nil),
		store:  st,
		sup: &supervisor.Supervisor{
			WorkspaceID:   "ws-1",
			NodeID:        "node-1",
			ControlStore:  st,
			Shutdown:      make(chan struct{}),
			StoppedAgents: make(map[string]struct{}),
		},
	}
	d.sup.ConfigSnapshot = d.configSnapshot

	if terminal := d.handleAgentCommand(command); !terminal {
		t.Fatal("Acked command did not converge after target config deletion")
	}
	persisted, err := st.AgentCommands().Get(ctx, "ws-1", command.CommandID)
	if err != nil {
		t.Fatalf("get completed command: %v", err)
	}
	if persisted.Status != domain.AgentCommandFailed {
		t.Fatalf("command status = %q, want failed terminal convergence", persisted.Status)
	}
}

func TestQueuedCommandIsNotAcknowledgedWithoutDurableOwnerIdentity(t *testing.T) {
	command := &domain.AgentCommand{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-owner-unavailable",
		TargetAgentID: "worker-1",
		Type:          "stop",
		Status:        domain.AgentCommandQueued,
	}
	commands := newDispatchTestAgentCommandStore([]*domain.AgentCommand{command})
	d := &Daemon{
		store: commandPollerTestStore{commands: commands},
		sup: &supervisor.Supervisor{
			ProjectDir:  t.TempDir() + "/missing-project",
			WorkspaceID: "ws-1",
			NodeID:      "process-node-must-not-be-owner",
			Shutdown:    make(chan struct{}),
		},
	}

	prepared, recovering, consumed, authority := d.prepareAgentCommand(command)
	defer authority.release()
	if prepared != nil || recovering || consumed {
		t.Fatalf("prepare = command:%+v recovering:%v consumed:%v, want retryable refusal", prepared, recovering, consumed)
	}
	if got := commands.ackCount(command.CommandID); got != 0 {
		t.Fatalf("Ack calls = %d, want zero when durable owner identity is unavailable", got)
	}
}

func TestAcknowledgedPersistentStartRequiresRuntimeNotStopped(t *testing.T) {
	const agentID = "worker-1"
	d := &Daemon{
		sup: &supervisor.Supervisor{
			Agents: []*supervisor.AgentProcess{{
				Entry: config.AgentEntry{
					Worktree: agentID,
					Mode:     domain.AgentModeService,
				},
			}},
			StoppedAgents: map[string]struct{}{agentID: {}},
		},
	}
	command := &domain.AgentCommand{
		TargetAgentID: agentID,
		Type:          "start",
	}

	if satisfied, errClass := d.acknowledgedStartSatisfied(command); satisfied || errClass != "" {
		t.Fatalf("registered-but-stopped persistent Start = satisfied:%v error_class:%q, want executable recovery", satisfied, errClass)
	}

	delete(d.sup.StoppedAgents, agentID)
	if satisfied, errClass := d.acknowledgedStartSatisfied(command); !satisfied || errClass != "" {
		t.Fatalf("already-running persistent Start = satisfied:%v error_class:%q, want idempotently satisfied", satisfied, errClass)
	}
}
