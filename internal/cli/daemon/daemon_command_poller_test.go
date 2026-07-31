package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPollAgentCommands_RetriesAckBeforeAdvancingWithFreshContext(t *testing.T) {
	origTimeout := agentCommandPollTimeout
	origRetryDelay := agentCommandRetryDelay
	agentCommandPollTimeout = 20 * time.Millisecond
	agentCommandRetryDelay = time.Millisecond
	t.Cleanup(func() {
		agentCommandPollTimeout = origTimeout
		agentCommandRetryDelay = origRetryDelay
	})

	cmdStore := &blockingAgentCommandStore{}
	d := &Daemon{
		store: commandPollerTestStore{commands: cmdStore},
		sup: &supervisor.Supervisor{
			WorkspaceID: "ws-1",
			NodeID:      "node-1",
			Shutdown:    make(chan struct{}),
			Agents:      []*supervisor.AgentProcess{commandPollerTestAgent("worker-1")},
		},
	}

	d.pollAgentCommands()
	d.sup.Wg.Wait()

	if cmdStore.secondAckExpired {
		t.Fatal("second command inherited an expired context from the first command")
	}
	if cmdStore.ackCalls != 3 {
		t.Fatalf("ack calls = %d, want failed first ack, retry, then second command", cmdStore.ackCalls)
	}
	if got, want := cmdStore.ackOrder, []string{"cmd-1", "cmd-1", "cmd-2"}; !slices.Equal(got, want) {
		t.Fatalf("ack order = %v, want %v", got, want)
	}
}

func TestHandleAgentCommand_UsesFreshCompletionContext(t *testing.T) {
	origTimeout := agentCommandPollTimeout
	agentCommandPollTimeout = 20 * time.Millisecond
	t.Cleanup(func() { agentCommandPollTimeout = origTimeout })

	cmdStore := &completionContextAgentCommandStore{}
	d := &Daemon{
		store: commandPollerTestStore{commands: cmdStore},
		sup: &supervisor.Supervisor{
			WorkspaceID: "ws-1",
			NodeID:      "node-1",
			Shutdown:    make(chan struct{}),
			Agents:      []*supervisor.AgentProcess{commandPollerTestAgent("worker-1")},
		},
	}

	d.handleAgentCommand(&domain.AgentCommand{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-slow",
		TargetNodeID:  "node-1",
		Type:          "unsupported",
		Status:        domain.AgentCommandQueued,
		TargetAgentID: "worker-1",
	})

	if cmdStore.completeContextExpired {
		t.Fatal("command completion inherited the expired acknowledgement context")
	}
	if cmdStore.completeCalls != 1 {
		t.Fatalf("complete calls = %d, want 1", cmdStore.completeCalls)
	}
}

func TestPollAgentCommands_DoesNotBlockOtherAgents(t *testing.T) {
	cmdStore := newDispatchTestAgentCommandStore([]*domain.AgentCommand{
		queuedAgentCommand("cmd-slow", "worker-slow"),
		queuedAgentCommand("cmd-fast", "worker-fast"),
	})
	cmdStore.blockCommandID = "cmd-slow"
	d := newCommandPollerTestDaemon(cmdStore)
	t.Cleanup(cmdStore.unblockAck)

	runAgentCommandPoll(t, d, cmdStore.unblockAck)

	waitForCommandSignals(t, cmdStore.started, "cmd-slow", "cmd-fast")
	cmdStore.unblockAck()
	d.sup.Wg.Wait()
}

func TestPollAgentCommands_PreservesPerAgentOrder(t *testing.T) {
	cmdStore := newDispatchTestAgentCommandStore([]*domain.AgentCommand{
		queuedAgentCommand("cmd-first", "worker-1"),
		queuedAgentCommand("cmd-second", "worker-1"),
	})
	cmdStore.blockCommandID = "cmd-first"
	d := newCommandPollerTestDaemon(cmdStore)
	t.Cleanup(cmdStore.unblockAck)

	runAgentCommandPoll(t, d, cmdStore.unblockAck)

	waitForCommandSignal(t, cmdStore.started, "cmd-first")
	select {
	case commandID := <-cmdStore.started:
		t.Fatalf("command %q started before the first command completed", commandID)
	case <-time.After(50 * time.Millisecond):
	}
	cmdStore.unblockAck()
	waitForCommandSignal(t, cmdStore.started, "cmd-second")
	d.sup.Wg.Wait()
}

func TestPollAgentCommands_DurableFIFOBlocksSecondDaemonUntilPredecessorTerminal(t *testing.T) {
	originalRetryDelay := agentCommandRetryDelay
	agentCommandRetryDelay = time.Millisecond
	t.Cleanup(func() { agentCommandRetryDelay = originalRetryDelay })

	ctx := context.Background()
	st := memstore.New()
	first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-first-daemon",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-first",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create first command: %v", err)
	}
	second, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-second-daemon",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-first",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create second command: %v", err)
	}
	lease, ack := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-first", "node-first")
	if _, err := st.AgentCommands().Ack(ctx, "ws-1", first.CommandID, ack); err != nil {
		t.Fatalf("ack first command: %v", err)
	}

	commands := &ackObservedAgentCommandStore{
		AgentCommandStore: st.AgentCommands(),
		ackAttempts:       make(chan agentCommandAckObservation, 64),
		completed:         make(chan string, 1),
	}
	secondDaemon := newCommandPollerTestDaemon(commands)
	secondDaemon.sup.NodeID = "node-first"
	secondDaemon.sup.Agents = []*supervisor.AgentProcess{commandPollerAgentFromLease(lease)}
	secondDaemon.dispatchAgentCommand(second)

	select {
	case attempt := <-commands.ackAttempts:
		if attempt.commandID != second.CommandID || !errors.Is(attempt.err, domain.ErrInvalidTransition) {
			t.Fatalf("second command first Ack attempt = %+v, want FIFO ErrInvalidTransition", attempt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second daemon's blocked Ack attempt")
	}
	persistedSecond, err := st.AgentCommands().Get(ctx, "ws-1", second.CommandID)
	if err != nil {
		t.Fatalf("get blocked second command: %v", err)
	}
	if persistedSecond.Status != domain.AgentCommandQueued {
		t.Fatalf("second status before first terminal = %q, want queued", persistedSecond.Status)
	}
	select {
	case completedID := <-commands.completed:
		t.Fatalf("command %q executed before its predecessor became terminal", completedID)
	default:
	}

	if _, err := st.AgentCommands().Complete(
		ctx,
		"ws-1",
		first.CommandID,
		commandCompletionFromAck(ack, domain.AgentCommandSucceeded),
	); err != nil {
		t.Fatalf("complete first command: %v", err)
	}

	ackDeadline := time.NewTimer(time.Second)
	defer ackDeadline.Stop()
	acked := false
	for !acked {
		select {
		case attempt := <-commands.ackAttempts:
			if attempt.commandID == second.CommandID && attempt.err == nil {
				acked = true
			}
		case <-ackDeadline.C:
			t.Fatal("timed out waiting for second Ack after predecessor became terminal")
		}
	}
	select {
	case completedID := <-commands.completed:
		if completedID != second.CommandID {
			t.Fatalf("completed command = %q, want %q", completedID, second.CommandID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second command execution")
	}
	secondDaemon.sup.Wg.Wait()
	persistedSecond, err = st.AgentCommands().Get(ctx, "ws-1", second.CommandID)
	if err != nil {
		t.Fatalf("get completed second command: %v", err)
	}
	if persistedSecond.Status != domain.AgentCommandFailed {
		t.Fatalf("second status after first terminal = %q, want failed from unsupported execution", persistedSecond.Status)
	}
}

func TestPollAgentCommands_ConsumesTerminalStaleSnapshotBeforeAdvancing(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-cancelled",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create first command: %v", err)
	}
	lease, ack := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")
	first, err = st.AgentCommands().Ack(ctx, "ws-1", first.CommandID, ack)
	if err != nil {
		t.Fatalf("ack first command: %v", err)
	}
	first, err = st.AgentCommands().Complete(
		ctx,
		"ws-1",
		first.CommandID,
		commandCompletionFromAck(ack, domain.AgentCommandCancelled),
	)
	if err != nil {
		t.Fatalf("cancel first command: %v", err)
	}
	second, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-next",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create second command: %v", err)
	}

	stale := *first
	stale.Status = domain.AgentCommandQueued
	d := newCommandPollerTestDaemon(st.AgentCommands())
	d.sup.Agents = []*supervisor.AgentProcess{commandPollerAgentFromLease(lease)}
	d.dispatchAgentCommand(&stale)
	d.dispatchAgentCommand(second)
	d.sup.Wg.Wait()

	gotFirst, err := st.AgentCommands().Get(ctx, "ws-1", first.CommandID)
	if err != nil {
		t.Fatalf("get first command: %v", err)
	}
	if gotFirst.Status != domain.AgentCommandCancelled {
		t.Fatalf("first status = %q, want cancelled", gotFirst.Status)
	}
	gotSecond, err := st.AgentCommands().Get(ctx, "ws-1", second.CommandID)
	if err != nil {
		t.Fatalf("get second command: %v", err)
	}
	if gotSecond.Status != domain.AgentCommandFailed {
		t.Fatalf("second status = %q, want failed after advancing past stale row", gotSecond.Status)
	}
}

func TestPollAgentCommands_LostAckResponseResumesBeforeAdvancing(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-ack-response-lost",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create first command: %v", err)
	}
	second, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-after-lost-ack",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create second command: %v", err)
	}
	commands := &lostAckResponseAgentCommandStore{
		AgentCommandStore: st.AgentCommands(),
		lostCommandID:     first.CommandID,
	}
	lease, _ := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")
	d := newCommandPollerTestDaemon(commands)
	d.store = lifecycleObservedStore{
		Store:    st,
		agents:   st.Agents(),
		commands: commands,
	}
	d.sup.Agents = []*supervisor.AgentProcess{commandPollerAgentFromLease(lease)}
	d.dispatchAgentCommand(first)
	d.dispatchAgentCommand(second)
	d.sup.Wg.Wait()

	if got, want := commands.completeOrder, []string{first.CommandID, second.CommandID}; !slices.Equal(got, want) {
		t.Fatalf("completion order = %v, want %v", got, want)
	}
}

func TestPollAgentCommands_AckGenerationAdvanceDoesNotInvertFIFO(t *testing.T) {
	for _, loseResponse := range []bool{false, true} {
		name := "returned"
		if loseResponse {
			name = "lost"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Agents().Create(ctx, store.AgentCreate{
				WorkspaceKey: "ws-1",
				Name:         "worker-1",
				RoleName:     "task",
				Mode:         domain.AgentModeService,
			}); err != nil {
				t.Fatalf("create agent: %v", err)
			}
			first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
				WorkspaceKey:  "ws-1",
				CommandID:     "cmd-ack-generation-advanced",
				TargetAgentID: "worker-1",
				Type:          "stop",
			})
			if err != nil {
				t.Fatalf("create first command: %v", err)
			}
			second, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
				WorkspaceKey:  "ws-1",
				CommandID:     "cmd-after-generation-advance",
				TargetAgentID: "worker-1",
				Type:          "stop",
			})
			if err != nil {
				t.Fatalf("create second command: %v", err)
			}
			commands := &acquireAfterAckAgentCommandStore{
				AgentCommandStore: st.AgentCommands(),
				ownership:         st.AgentOwnershipLeases(),
				commandID:         first.CommandID,
				acquire: store.AgentOwnershipLeaseAcquire{
					WorkspaceKey: "ws-1",
					AgentID:      "worker-1",
					NodeID:       "node-1",
					OwnerID:      "node-1",
					TTL:          time.Minute,
				},
				loseResponse: loseResponse,
			}
			cfg := makeDaemonConfig([]AgentEntry{{
				Worktree: "worker-1",
				Role:     "task",
				Mode:     domain.AgentModeService,
			}}, nil)
			d := &Daemon{
				config: cfg,
				store: lifecycleObservedStore{
					Store:    st,
					agents:   st.Agents(),
					commands: commands,
				},
				sup: &supervisor.Supervisor{
					WorkspaceID:   "ws-1",
					NodeID:        "node-1",
					ControlStore:  st,
					Shutdown:      make(chan struct{}),
					StoppedAgents: make(map[string]struct{}),
				},
			}
			d.sup.ConfigSnapshot = d.configSnapshot
			d.dispatchAgentCommand(first)
			d.dispatchAgentCommand(second)
			d.sup.Wg.Wait()

			if got, want := commands.ackOrderSnapshot(), []string{first.CommandID, second.CommandID}; !slices.Equal(got, want) {
				t.Fatalf("Ack order = %v, want %v", got, want)
			}
			if got, want := commands.completeOrderSnapshot(), []string{first.CommandID, second.CommandID}; !slices.Equal(got, want) {
				t.Fatalf("Complete order = %v, want %v", got, want)
			}
			for _, commandID := range []string{first.CommandID, second.CommandID} {
				command, err := st.AgentCommands().Get(ctx, "ws-1", commandID)
				if err != nil {
					t.Fatalf("get %s: %v", commandID, err)
				}
				if command.Status != domain.AgentCommandSucceeded {
					t.Fatalf("%s status = %q, want succeeded", commandID, command.Status)
				}
			}
		})
	}
}

func TestHandleAgentCommandCompletesWithoutCompatibilityProjection(t *testing.T) {
	ctx := context.Background()
	st, command, lease := acknowledgedPersistentStartFixture(t)
	calls := &lifecycleCallRecorder{}
	wrapped := lifecycleObservedStore{
		Store: st,
		agents: &recordingAgentStore{
			AgentStore: st.Agents(),
			calls:      calls,
		},
		commands: &recordingAgentCommandStore{
			AgentCommandStore: st.AgentCommands(),
			calls:             calls,
		},
	}
	d := acknowledgedPersistentStartDaemon(wrapped, lease, nil)

	if consumed := d.handleAgentCommand(command); !consumed {
		t.Fatal("acknowledged command was not consumed")
	}
	if got, want := calls.snapshot(), []string{"complete-command"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle write order = %v, want %v", got, want)
	}
	persisted, err := st.AgentCommands().Get(ctx, "ws-1", command.CommandID)
	if err != nil {
		t.Fatalf("get completed command: %v", err)
	}
	if persisted.Status != domain.AgentCommandSucceeded {
		t.Fatalf("command status = %q, want succeeded after projection", persisted.Status)
	}
}

func TestHandleAgentCommandDoesNotCallRetiredCompatibilityProjection(t *testing.T) {
	ctx := context.Background()
	st, command, lease := acknowledgedPersistentStartFixture(t)
	calls := &lifecycleCallRecorder{}
	wrapped := lifecycleObservedStore{
		Store: st,
		agents: &recordingAgentStore{
			AgentStore: st.Agents(),
			calls:      calls,
			fail:       errors.New("retired compatibility projection was called"),
		},
		commands: &recordingAgentCommandStore{
			AgentCommandStore: st.AgentCommands(),
			calls:             calls,
		},
	}
	d := acknowledgedPersistentStartDaemon(wrapped, lease, nil)

	if consumed := d.handleAgentCommand(command); !consumed {
		t.Fatal("acknowledged command was not consumed")
	}
	if got, want := calls.snapshot(), []string{"complete-command"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle writes = %v, want command completion without compatibility projection", got)
	}
	persisted, err := st.AgentCommands().Get(ctx, "ws-1", command.CommandID)
	if err != nil {
		t.Fatalf("get completed command: %v", err)
	}
	if persisted.Status != domain.AgentCommandSucceeded {
		t.Fatalf("command status = %q, want succeeded without compatibility projection", persisted.Status)
	}
}

func TestPollAgentCommands_RetriesCompletionBeforeAdvancingTargetQueue(t *testing.T) {
	origRetryDelay := agentCommandRetryDelay
	agentCommandRetryDelay = 100 * time.Millisecond
	t.Cleanup(func() { agentCommandRetryDelay = origRetryDelay })

	cmdStore := newDispatchTestAgentCommandStore([]*domain.AgentCommand{
		queuedAgentCommand("cmd-first", "worker-1"),
		queuedAgentCommand("cmd-second", "worker-1"),
	})
	cmdStore.completeFailures["cmd-first"] = 1
	d := newCommandPollerTestDaemon(cmdStore)

	runAgentCommandPoll(t, d, cmdStore.unblockAck)
	waitForCondition(t, func() bool {
		return cmdStore.completeCount("cmd-first") == 1
	}, "first failed completion attempt")
	if got := cmdStore.ackCount("cmd-second"); got != 0 {
		t.Fatalf("second command ack calls during first completion retry = %d, want 0", got)
	}

	d.sup.Wg.Wait()
	if got := cmdStore.completeCount("cmd-first"); got != 2 {
		t.Fatalf("first command completion calls = %d, want transient failure plus retry", got)
	}
	if got := cmdStore.ackCount("cmd-second"); got != 1 {
		t.Fatalf("second command ack calls = %d, want 1 after first completion persisted", got)
	}
}

func TestPollAgentCommands_IncompleteAckedHeadRetriesBeforeSuccessor(t *testing.T) {
	originalRetryDelay := agentCommandRetryDelay
	agentCommandRetryDelay = time.Millisecond
	t.Cleanup(func() { agentCommandRetryDelay = originalRetryDelay })

	ctx := context.Background()
	st := memstore.New()
	first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-incomplete-acked-head",
		TargetAgentID: "worker-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create first command: %v", err)
	}
	second, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-after-incomplete-head",
		TargetAgentID: "worker-1",
		Type:          "unsupported",
	})
	if err != nil {
		t.Fatalf("create second command: %v", err)
	}
	lease, _ := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")
	commands := &firstCompletionOwnershipConflictStore{
		AgentCommandStore: st.AgentCommands(),
		commandID:         first.CommandID,
	}
	ownership := &firstAcquireUnavailableStore{
		AgentOwnershipLeaseStore: st.AgentOwnershipLeases(),
		failures:                 1,
	}
	wrapped := commandOwnershipStoreOverride{
		Store:     st,
		commands:  commands,
		ownership: ownership,
	}
	d := newCommandPollerTestDaemon(commands)
	d.store = wrapped
	d.sup.ControlStore = wrapped
	d.sup.Agents = []*supervisor.AgentProcess{commandPollerAgentFromLease(lease)}
	d.sup.ConfigSnapshot = d.configSnapshot
	d.dispatchAgentCommand(first)
	d.dispatchAgentCommand(second)
	d.sup.Wg.Wait()

	if got, want := commands.ackOrderSnapshot(), []string{first.CommandID, second.CommandID}; !slices.Equal(got, want) {
		t.Fatalf("Ack order = %v, want %v", got, want)
	}
	if got, want := commands.completeOrderSnapshot(), []string{first.CommandID, second.CommandID}; !slices.Equal(got, want) {
		t.Fatalf("Complete order = %v, want %v", got, want)
	}
	if commands.conflicts != 1 || ownership.failed != 1 {
		t.Fatalf("ownership interruption counts = complete:%d acquire:%d, want 1/1", commands.conflicts, ownership.failed)
	}
	for _, commandID := range []string{first.CommandID, second.CommandID} {
		command, err := st.AgentCommands().Get(ctx, "ws-1", commandID)
		if err != nil {
			t.Fatalf("get %s: %v", commandID, err)
		}
		if command.Status != domain.AgentCommandFailed {
			t.Fatalf("%s status = %q, want failed terminal", commandID, command.Status)
		}
	}
}

func TestPollAgentCommands_ResumesAcknowledgedCommandWithoutSecondAck(t *testing.T) {
	cmd := queuedAgentCommand("cmd-resume", "worker-1")
	cmd.Status = domain.AgentCommandAcked
	cmd.AckedBy = "node-1"
	cmdStore := newDispatchTestAgentCommandStore([]*domain.AgentCommand{cmd})
	d := newCommandPollerTestDaemon(cmdStore)

	runAgentCommandPoll(t, d, cmdStore.unblockAck)
	d.sup.Wg.Wait()

	if got := cmdStore.ackCount("cmd-resume"); got != 0 {
		t.Fatalf("recovered command ack calls = %d, want 0", got)
	}
	if got := cmdStore.completeCount("cmd-resume"); got != 1 {
		t.Fatalf("recovered command completion calls = %d, want 1", got)
	}
}

func TestPollAgentCommands_AckedRestartWithNewerGenerationDoesNotRestartTwice(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "ws-1", Name: "Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws-1",
		Name:         "worker-1",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "restart-recovery",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-1",
		Type:          "restart",
	})
	if err != nil {
		t.Fatalf("create restart command: %v", err)
	}
	_, ack := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")
	command, err = st.AgentCommands().Ack(ctx, "ws-1", command.CommandID, ack)
	if err != nil {
		t.Fatalf("ack restart command: %v", err)
	}
	generation := &supervisor.AgentProcess{
		Entry:                 config.AgentEntry{Worktree: "worker-1", Role: "task"},
		LifecycleGenerationAt: command.AckedAt.Add(time.Second),
		StopCh:                make(chan struct{}),
		Done:                  make(chan struct{}),
	}
	cfg := makeDaemonConfig([]AgentEntry{generation.Entry}, nil)
	d := &Daemon{
		config: cfg,
		store:  st,
		sup: &supervisor.Supervisor{
			ConfigSnapshot: func() *config.DaemonConfig { return cfg },
			WorkspaceID:    "ws-1",
			NodeID:         "node-1",
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			Agents:         []*supervisor.AgentProcess{generation},
			ControlStore:   st,
		},
	}

	if !d.handleAgentCommand(command) {
		t.Fatal("acked restart was not consumed")
	}
	completed, err := st.AgentCommands().Get(ctx, "ws-1", command.CommandID)
	if err != nil {
		t.Fatalf("get completed command: %v", err)
	}
	if completed.Status != domain.AgentCommandSucceeded {
		t.Fatalf("restart status = %q, want succeeded", completed.Status)
	}
	if len(d.sup.Agents) != 1 || d.sup.Agents[0] != generation {
		t.Fatalf("runtime generation changed during recovery: %+v", d.sup.Agents)
	}
}

func TestPollAgentCommands_AckedRestartWithoutNewerGenerationFailsClosed(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "ws-1", Name: "Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws-1",
		Name:         "worker-1",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "restart-ambiguous",
		TargetAgentID: "worker-1",
		TargetNodeID:  "node-1",
		Type:          "restart",
	})
	if err != nil {
		t.Fatalf("create restart command: %v", err)
	}
	_, ack := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")
	command, err = st.AgentCommands().Ack(ctx, "ws-1", command.CommandID, ack)
	if err != nil {
		t.Fatalf("ack restart command: %v", err)
	}
	generation := &supervisor.AgentProcess{
		Entry:                 config.AgentEntry{Worktree: "worker-1", Role: "task"},
		LifecycleGenerationAt: command.AckedAt.Add(-time.Second),
		StopCh:                make(chan struct{}),
		Done:                  make(chan struct{}),
	}
	cfg := makeDaemonConfig([]AgentEntry{generation.Entry}, nil)
	d := &Daemon{
		config: cfg,
		store:  st,
		sup: &supervisor.Supervisor{
			ConfigSnapshot: func() *config.DaemonConfig { return cfg },
			WorkspaceID:    "ws-1",
			NodeID:         "node-1",
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			Agents:         []*supervisor.AgentProcess{generation},
			ControlStore:   st,
		},
	}

	if !d.handleAgentCommand(command) {
		t.Fatal("ambiguous acked restart was not consumed")
	}
	completed, err := st.AgentCommands().Get(ctx, "ws-1", command.CommandID)
	if err != nil {
		t.Fatalf("get completed command: %v", err)
	}
	if completed.Status != domain.AgentCommandFailed ||
		completed.ErrorClass != "ambiguous_restart_recovery" {
		t.Fatalf("restart completion = %+v, want failed ambiguous recovery", completed)
	}
	if len(d.sup.Agents) != 1 || d.sup.Agents[0] != generation {
		t.Fatalf("runtime generation changed during ambiguous recovery: %+v", d.sup.Agents)
	}
}

func TestAcknowledgedAgentCommandSatisfied_FencesEphemeralTaskGeneration(t *testing.T) {
	agent := &supervisor.AgentProcess{
		Entry: config.AgentEntry{
			Worktree: "ephemeral-worker",
			Role:     "task",
			Mode:     domain.AgentModeEphemeral,
		},
		RequestedTaskID: "TASK-1",
	}
	d := &Daemon{sup: &supervisor.Supervisor{
		Agents: []*supervisor.AgentProcess{agent},
	}}

	satisfied, errClass := d.acknowledgedAgentCommandSatisfied(&domain.AgentCommand{
		Type:          "start",
		TargetAgentID: "ephemeral-worker",
		Payload:       map[string]string{"task_id": "TASK-1"},
	})
	if !satisfied || errClass != "" {
		t.Fatalf("matching task recovery = %v/%q, want satisfied", satisfied, errClass)
	}
	satisfied, errClass = d.acknowledgedAgentCommandSatisfied(&domain.AgentCommand{
		Type:          "start",
		TargetAgentID: "ephemeral-worker",
		Payload:       map[string]string{"task_id": "TASK-2"},
	})
	if satisfied || errClass != "ambiguous_ephemeral_start_recovery" {
		t.Fatalf("mismatched task recovery = %v/%q, want fenced ambiguity", satisfied, errClass)
	}
}

func TestAcknowledgedAgentCommandSatisfied_DoesNotRewriteExistingYield(t *testing.T) {
	worktree := t.TempDir()
	requestedAt := time.Now().Add(-time.Minute).UTC()
	if err := supervisor.WriteYieldFile(worktree, &supervisor.YieldRequest{
		Reason:      "manual_stop",
		RequestedAt: requestedAt,
		RequestedBy: "daemon",
	}); err != nil {
		t.Fatalf("write yield file: %v", err)
	}
	agent := &supervisor.AgentProcess{
		Entry:        config.AgentEntry{Worktree: "worker-1", Role: "task"},
		WorktreePath: worktree,
		Pid:          12345,
	}
	d := &Daemon{sup: &supervisor.Supervisor{
		Agents: []*supervisor.AgentProcess{agent},
	}}

	satisfied, errClass := d.acknowledgedAgentCommandSatisfied(&domain.AgentCommand{
		Type:          "yield",
		TargetAgentID: "worker-1",
	})
	if !satisfied || errClass != "" {
		t.Fatalf("yield recovery = %v/%q, want existing marker satisfied", satisfied, errClass)
	}
	got, err := supervisor.ReadYieldFile(worktree)
	if err != nil {
		t.Fatalf("read yield file: %v", err)
	}
	if got == nil || !got.RequestedAt.Equal(requestedAt) {
		t.Fatalf("yield marker requested_at = %+v, want unchanged %v", got, requestedAt)
	}
}

func TestProoflessStoppedCommandConvergesBeforeQueuedStart(t *testing.T) {
	for _, commandType := range []string{"stop", "yield", "restart"} {
		t.Run(commandType, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Agents().Create(ctx, store.AgentCreate{
				WorkspaceKey: "ws-1",
				Name:         "worker-1",
				RoleName:     "task",
				Mode:         domain.AgentModeService,
				DesiredState: domain.AgentDesiredStopped,
			}); err != nil {
				t.Fatalf("create agent: %v", err)
			}
			first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
				WorkspaceKey:  "ws-1",
				CommandID:     "cmd-stopped-" + commandType,
				TargetAgentID: "worker-1",
				Type:          commandType,
			})
			if err != nil {
				t.Fatalf("create %s command: %v", commandType, err)
			}
			first, err = st.AgentCommands().Ack(ctx, "ws-1", first.CommandID, store.AgentCommandAck{
				NodeID:  "node-1",
				OwnerID: "node-1",
			})
			if err != nil {
				t.Fatalf("proofless Ack %s: %v", commandType, err)
			}
			next, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
				WorkspaceKey:  "ws-1",
				CommandID:     "cmd-start-after-" + commandType,
				TargetAgentID: "worker-1",
				Type:          "start",
			})
			if err != nil {
				t.Fatalf("create successor Start: %v", err)
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
			if terminal := d.handleAgentCommand(first); !terminal {
				t.Fatalf("%s did not terminally converge", commandType)
			}
			persistedFirst, err := st.AgentCommands().Get(ctx, "ws-1", first.CommandID)
			if err != nil {
				t.Fatalf("get first command: %v", err)
			}
			wantStatus := domain.AgentCommandFailed
			if commandType == "stop" {
				wantStatus = domain.AgentCommandSucceeded
			}
			if persistedFirst.Status != wantStatus {
				t.Fatalf("%s status = %q, want %q", commandType, persistedFirst.Status, wantStatus)
			}
			prepared, recovering, consumed, authority := d.prepareAgentCommand(next)
			defer authority.release()
			if prepared == nil || recovering || consumed {
				t.Fatalf("successor Start prepare = command:%+v recovering:%v consumed:%v", prepared, recovering, consumed)
			}
			if prepared.Status != domain.AgentCommandAcked ||
				prepared.OwnershipLeaseID != "" ||
				prepared.OwnershipFencingToken != 0 {
				t.Fatalf("successor Start Ack = %+v, want proofless Ack after absence convergence", prepared)
			}
		})
	}
}

func TestProoflessFailureReacquiresAfterSameOwnerGenerationAdvance(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws-1",
		Name:         "worker-1",
		RoleName:     "task",
		Mode:         domain.AgentModeService,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-proofless-failure-advanced",
		TargetAgentID: "worker-1",
		Type:          "yield",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	commands := &acquireBeforeCompleteAgentCommandStore{
		AgentCommandStore: st.AgentCommands(),
		ownership:         st.AgentOwnershipLeases(),
		commandID:         command.CommandID,
		acquire: store.AgentOwnershipLeaseAcquire{
			WorkspaceKey: "ws-1",
			AgentID:      "worker-1",
			NodeID:       "node-1",
			OwnerID:      "node-1",
			TTL:          time.Minute,
		},
	}
	cfg := makeDaemonConfig([]AgentEntry{{
		Worktree: "worker-1",
		Role:     "task",
		Mode:     domain.AgentModeService,
	}}, nil)
	d := &Daemon{
		config: cfg,
		store: lifecycleObservedStore{
			Store:    st,
			agents:   st.Agents(),
			commands: commands,
		},
		sup: &supervisor.Supervisor{
			WorkspaceID:   "ws-1",
			NodeID:        "node-1",
			ControlStore:  st,
			Shutdown:      make(chan struct{}),
			StoppedAgents: map[string]struct{}{"worker-1": {}},
		},
	}
	d.sup.ConfigSnapshot = d.configSnapshot

	if terminal := d.handleAgentCommand(command); !terminal {
		t.Fatal("proofless failure did not converge after same-owner generation advance")
	}
	persisted, err := st.AgentCommands().Get(ctx, "ws-1", command.CommandID)
	if err != nil {
		t.Fatalf("get command: %v", err)
	}
	if persisted.Status != domain.AgentCommandFailed ||
		persisted.OwnershipLeaseID == "" ||
		persisted.OwnershipFencingToken == 0 {
		t.Fatalf("completed command = %+v, want failed with advanced generation", persisted)
	}
	if commands.completeCalls != 2 {
		t.Fatalf("Complete calls = %d, want conflict then fenced retry", commands.completeCalls)
	}
}

func TestListPollableAgentCommands_TerminalHistoryDoesNotStarveQueuedRows(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	_, ack := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")
	for i := range 60 {
		command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "ws-1",
			CommandID:     fmt.Sprintf("terminal-%02d", i),
			TargetAgentID: "worker-1",
			Type:          "stop",
		})
		if err != nil {
			t.Fatalf("create terminal command %d: %v", i, err)
		}
		if _, err := st.AgentCommands().Ack(ctx, "ws-1", command.CommandID, ack); err != nil {
			t.Fatalf("ack terminal command %d: %v", i, err)
		}
		if _, err := st.AgentCommands().Complete(
			ctx,
			"ws-1",
			command.CommandID,
			commandCompletionFromAck(ack, domain.AgentCommandSucceeded),
		); err != nil {
			t.Fatalf("complete terminal command %d: %v", i, err)
		}
	}
	queued, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "queued-after-history",
		TargetAgentID: "worker-1",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create queued command: %v", err)
	}

	d := newCommandPollerTestDaemon(st.AgentCommands())
	commands, err := d.listPollableAgentCommands(ctx)
	if err != nil {
		t.Fatalf("list pollable commands: %v", err)
	}
	if len(commands) != 1 || commands[0].CommandID != queued.CommandID {
		t.Fatalf("pollable commands = %+v, want only %q", commands, queued.CommandID)
	}
}

func TestListPollableAgentCommands_ForeignNodePagesDoNotStarveLocalRows(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for i := range 60 {
		if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "ws-1",
			CommandID:     fmt.Sprintf("foreign-%02d", i),
			TargetAgentID: "worker-foreign",
			TargetNodeID:  "node-offline",
			Type:          "stop",
		}); err != nil {
			t.Fatalf("create foreign command %d: %v", i, err)
		}
	}
	local, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "local-after-foreign-pages",
		TargetAgentID: "worker-local",
		TargetNodeID:  "node-1",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create local command: %v", err)
	}

	d := newCommandPollerTestDaemon(st.AgentCommands())
	commands, err := d.listPollableAgentCommands(ctx)
	if err != nil {
		t.Fatalf("list pollable commands: %v", err)
	}
	if len(commands) != 1 || commands[0].CommandID != local.CommandID {
		t.Fatalf("pollable commands = %+v, want only %q", commands, local.CommandID)
	}
}

func TestListPollableAgentCommands_RemoteLiveOwnershipDoesNotConsumeGlobalCap(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for i := range 60 {
		agentID := fmt.Sprintf("worker-remote-%02d", i)
		if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "ws-1",
			CommandID:     fmt.Sprintf("remote-live-%02d", i),
			TargetAgentID: agentID,
			Type:          "stop",
		}); err != nil {
			t.Fatalf("create remote-live command %d: %v", i, err)
		}
		if _, err := st.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
			WorkspaceKey: "ws-1",
			AgentID:      agentID,
			NodeID:       "node-remote",
			OwnerID:      "owner-remote",
			TTL:          time.Minute,
		}); err != nil {
			t.Fatalf("acquire remote ownership %d: %v", i, err)
		}
	}
	local, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "local-after-remote-live-backlog",
		TargetAgentID: "worker-local",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create local command: %v", err)
	}

	d := newCommandPollerTestDaemon(st.AgentCommands())
	d.store = st
	commands, err := d.listPollableAgentCommands(ctx)
	if err != nil {
		t.Fatalf("list pollable commands: %v", err)
	}
	if len(commands) != 1 || commands[0].CommandID != local.CommandID {
		t.Fatalf("pollable commands = %+v, want only %q", commands, local.CommandID)
	}
}

func TestAgentCommandOwnershipSnapshotTrustsServerEffectiveActiveStatus(t *testing.T) {
	ownership := &recordingOwnershipListStore{
		leases: []*domain.AgentOwnershipLease{{
			AgentID:   "worker-remote",
			OwnerID:   "owner-remote",
			NodeID:    "node-remote",
			Status:    domain.AgentLeaseActive,
			ExpiresAt: time.Now().UTC().Add(-24 * time.Hour),
		}},
	}
	d := newCommandPollerTestDaemon(&dispatchTestAgentCommandStore{})
	d.store = commandPollerTestStore{
		commands:  d.store.AgentCommands(),
		ownership: ownership,
	}
	snapshot, err := d.agentCommandOwnershipSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ownership snapshot: %v", err)
	}
	if holder, ok := snapshot["worker-remote"]; !ok ||
		holder.ownerID != "owner-remote" ||
		holder.nodeID != "node-remote" {
		t.Fatalf("server-active lease missing from snapshot: %+v", snapshot)
	}
	if ownership.filter.Status != domain.AgentLeaseActive {
		t.Fatalf("ownership List status filter = %q, want active", ownership.filter.Status)
	}
}

func TestListPollableAgentCommands_ForeignAckedPagesDoNotStarveLocalRecovery(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for i := range 60 {
		command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "ws-1",
			CommandID:     fmt.Sprintf("foreign-acked-%02d", i),
			TargetAgentID: fmt.Sprintf("worker-foreign-%02d", i),
			TargetNodeID:  "node-offline",
			Type:          "start",
		})
		if err != nil {
			t.Fatalf("create foreign command %d: %v", i, err)
		}
		if _, err := st.AgentCommands().Ack(ctx, "ws-1", command.CommandID, store.AgentCommandAck{
			NodeID:  "node-offline",
			OwnerID: "node-offline",
		}); err != nil {
			t.Fatalf("ack foreign command %d: %v", i, err)
		}
	}
	local, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "local-acked-after-foreign-pages",
		TargetAgentID: "worker-local",
		TargetNodeID:  "node-1",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create local command: %v", err)
	}
	local, err = st.AgentCommands().Ack(ctx, "ws-1", local.CommandID, store.AgentCommandAck{
		NodeID:  "node-1",
		OwnerID: "node-1",
	})
	if err != nil {
		t.Fatalf("ack local command: %v", err)
	}

	d := newCommandPollerTestDaemon(st.AgentCommands())
	commands, err := d.listPollableAgentCommands(ctx)
	if err != nil {
		t.Fatalf("list pollable commands: %v", err)
	}
	if len(commands) != 1 || commands[0].CommandID != local.CommandID {
		t.Fatalf("pollable commands = %+v, want only %q", commands, local.CommandID)
	}
}

func TestListPollableAgentCommands_TrimsMergedStatusesByGlobalCursor(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for i := range 51 {
		if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "ws-1",
			CommandID:     fmt.Sprintf("queued-%02d", i),
			TargetAgentID: "worker-1",
			Type:          "stop",
		}); err != nil {
			t.Fatalf("create queued command %d: %v", i, err)
		}
	}
	acked, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "acked-later",
		TargetAgentID: "worker-acked",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create acked command: %v", err)
	}
	if _, err := st.AgentCommands().Ack(ctx, "ws-1", acked.CommandID, store.AgentCommandAck{
		NodeID:  "node-1",
		OwnerID: "node-1",
	}); err != nil {
		t.Fatalf("ack command: %v", err)
	}

	d := newCommandPollerTestDaemon(st.AgentCommands())
	commands, err := d.listPollableAgentCommands(ctx)
	if err != nil {
		t.Fatalf("list pollable commands: %v", err)
	}
	if len(commands) != 50 {
		t.Fatalf("pollable commands = %d, want bounded global page of 50", len(commands))
	}
	for i, cmd := range commands {
		if got, want := cmd.Cursor, int64(i+1); got != want {
			t.Fatalf("command[%d] cursor = %d, want %d", i, got, want)
		}
	}
}

func TestListPollableAgentCommands_PendingBacklogDoesNotStarveAnotherAgent(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	stuck, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "stuck-projection",
		TargetAgentID: "worker-stuck",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create stuck command: %v", err)
	}
	_, ack := acquireCommandOwnership(t, st, "ws-1", "worker-stuck", "node-1", "node-1")
	stuck, err = st.AgentCommands().Ack(ctx, "ws-1", stuck.CommandID, ack)
	if err != nil {
		t.Fatalf("ack stuck command: %v", err)
	}

	pending := []agentCommandKey{{
		workspaceKey: stuck.WorkspaceKey,
		commandID:    stuck.CommandID,
	}}
	for i := range 49 {
		command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "ws-1",
			CommandID:     fmt.Sprintf("same-target-pending-%02d", i),
			TargetAgentID: "worker-stuck",
			Type:          "stop",
		})
		if err != nil {
			t.Fatalf("create same-target pending command %d: %v", i, err)
		}
		pending = append(pending, agentCommandKey{
			workspaceKey: command.WorkspaceKey,
			commandID:    command.CommandID,
		})
	}
	other, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "other-agent-after-pending-cap",
		TargetAgentID: "worker-other",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create other-agent command: %v", err)
	}

	d := newCommandPollerTestDaemon(st.AgentCommands())
	d.agentCommandDispatcher.mu.Lock()
	d.agentCommandDispatcher.pending = make(map[agentCommandKey]struct{}, len(pending))
	for _, key := range pending {
		d.agentCommandDispatcher.pending[key] = struct{}{}
	}
	d.agentCommandDispatcher.mu.Unlock()

	commands, err := d.listPollableAgentCommands(ctx)
	if err != nil {
		t.Fatalf("list pollable commands: %v", err)
	}
	if len(commands) != 1 || commands[0].CommandID != other.CommandID {
		t.Fatalf("pollable commands = %+v, want only unrelated command %q", commands, other.CommandID)
	}
}

func TestPollAgentCommands_DeduplicatesQueuedAndRunningAcrossPollCycles(t *testing.T) {
	cmdStore := newDispatchTestAgentCommandStore([]*domain.AgentCommand{
		queuedAgentCommand("cmd-blocking", "worker-1"),
		queuedAgentCommand("cmd-1", "worker-1"),
	})
	cmdStore.blockCommandID = "cmd-blocking"
	d := newCommandPollerTestDaemon(cmdStore)
	t.Cleanup(cmdStore.unblockAck)

	runAgentCommandPoll(t, d, cmdStore.unblockAck)
	waitForCommandSignal(t, cmdStore.started, "cmd-blocking")
	runAgentCommandPoll(t, d, cmdStore.unblockAck)

	if got := cmdStore.ackCount("cmd-blocking"); got != 1 {
		t.Fatalf("ack calls for running command = %d, want 1", got)
	}
	if got := cmdStore.ackCount("cmd-1"); got != 0 {
		t.Fatalf("ack calls for queued command = %d, want 0", got)
	}
	cmdStore.unblockAck()
	waitForCommandSignal(t, cmdStore.started, "cmd-1")
	d.sup.Wg.Wait()
	if got := cmdStore.ackCount("cmd-blocking"); got != 1 {
		t.Fatalf("total ack calls for running command = %d, want 1", got)
	}
	if got := cmdStore.ackCount("cmd-1"); got != 1 {
		t.Fatalf("total ack calls for queued command = %d, want 1", got)
	}
}

func TestSupervisorStop_WaitsForAgentCommandWorkers(t *testing.T) {
	cmdStore := newDispatchTestAgentCommandStore(nil)
	cmdStore.blockCompleteCommandID = "cmd-1"
	d := newCommandPollerTestDaemon(cmdStore)
	t.Cleanup(cmdStore.unblockComplete)

	d.dispatchAgentCommand(queuedAgentCommand("cmd-1", "worker-1"))
	d.dispatchAgentCommand(queuedAgentCommand("cmd-after-shutdown", "worker-1"))
	waitForCommandSignal(t, cmdStore.completeStarted, "cmd-1")

	stopDone := make(chan struct{})
	go func() {
		d.sup.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("Supervisor.Stop returned before the command worker completed")
	case <-time.After(50 * time.Millisecond):
	}

	cmdStore.unblockComplete()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Supervisor.Stop did not return after the command worker completed")
	}
	if got := cmdStore.ackCount("cmd-after-shutdown"); got != 0 {
		t.Fatalf("queued command ack calls after shutdown = %d, want 0", got)
	}
}

func TestPollableAgentCommandStatus(t *testing.T) {
	if pollableAgentCommandStatus("") {
		t.Fatal("empty agent command status should not exist after queued became the durable default")
	}
	if !pollableAgentCommandStatus(domain.AgentCommandQueued) {
		t.Fatal("queued agent command status should be pollable")
	}
	if !pollableAgentCommandStatus(domain.AgentCommandAcked) {
		t.Fatal("acked agent command status should be recoverable after daemon interruption")
	}
	if !pollableAgentCommandStatus(domain.AgentCommandRunning) {
		t.Fatal("running agent command status should be recoverable after daemon interruption")
	}
	if pollableAgentCommandStatus(domain.AgentCommandSucceeded) {
		t.Fatal("succeeded agent command status should not be re-polled")
	}
}

func TestReassertAgentCommandLifecycle_Stop(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:  "ws-1",
		Name: "Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws-1",
		Name:         "worker-1",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredDraining,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	d := &Daemon{
		config: makeDaemonConfig([]AgentEntry{{
			Worktree:     "worker-1",
			Role:         "task",
			DesiredState: domain.AgentDesiredDraining,
		}}, nil),
		store: st,
		sup: &supervisor.Supervisor{
			WorkspaceID:   "ws-1",
			Shutdown:      make(chan struct{}),
			StoppedAgents: make(map[string]struct{}),
		},
	}

	d.reassertAgentCommandLifecycle(&domain.AgentCommand{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-stop",
		TargetAgentID: "worker-1",
		Type:          "stop",
	}, DaemonControlResponse{Success: true})

	agent, err := st.Agents().Get(ctx, "ws-1", "worker-1")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.State != domain.AgentStateIdle || agent.DesiredState != domain.AgentDesiredDraining {
		t.Fatalf(
			"compatibility projection = %s/%s, want runtime-owned idle and unchanged intent draining",
			agent.State,
			agent.DesiredState,
		)
	}
	entry, ok := d.findAgentEntry("worker-1")
	if !ok || entry.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("config agent = %#v, %v; want desired stopped", entry, ok)
	}
}

func TestReassertAgentCommandLifecycle_DoesNotWriteCompatibilityRuntimeProjection(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "ws-1", Name: "Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws-1",
		Name:         "worker-1",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredDraining,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	flakyAgents := &transientUpdateAgentStore{
		AgentStore: st.Agents(),
		failures:   1,
	}
	wrapped := agentStoreOverride{
		Store:  st,
		agents: flakyAgents,
	}
	cfg := makeDaemonConfig([]AgentEntry{{
		Worktree:     "worker-1",
		Role:         "task",
		DesiredState: domain.AgentDesiredDraining,
	}}, nil)
	d := &Daemon{
		config: cfg,
		store:  wrapped,
		sup: &supervisor.Supervisor{
			WorkspaceID:   "ws-1",
			Shutdown:      make(chan struct{}),
			StoppedAgents: make(map[string]struct{}),
		},
	}

	d.reassertAgentCommandLifecycle(&domain.AgentCommand{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-stop",
		TargetAgentID: "worker-1",
		Type:          "stop",
	}, DaemonControlResponse{Success: true})

	if flakyAgents.calls != 0 {
		t.Fatalf("compatibility projection update calls = %d, want none", flakyAgents.calls)
	}
	agent, err := st.Agents().Get(ctx, "ws-1", "worker-1")
	if err != nil {
		t.Fatalf("get projected agent: %v", err)
	}
	if agent.State != domain.AgentStateIdle || agent.DesiredState != domain.AgentDesiredDraining {
		t.Fatalf(
			"compatibility projection = %s/%s, want runtime-owned idle and unchanged intent draining",
			agent.State,
			agent.DesiredState,
		)
	}
	entry, ok := d.findAgentEntry("worker-1")
	if !ok || entry.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("daemon config agent = %#v, %v; want desired stopped", entry, ok)
	}
}

func queuedAgentCommand(commandID, agentID string) *domain.AgentCommand {
	return &domain.AgentCommand{
		WorkspaceKey:  "ws-1",
		CommandID:     commandID,
		TargetNodeID:  "node-1",
		Type:          "unsupported",
		Status:        domain.AgentCommandQueued,
		TargetAgentID: agentID,
	}
}

func newCommandPollerTestDaemon(commands store.AgentCommandStore) *Daemon {
	agents := make([]*supervisor.AgentProcess, 0, 6)
	for _, agentID := range []string{
		"worker-1",
		"worker-slow",
		"worker-fast",
		"worker-local",
		"worker-other",
		"worker-stuck",
	} {
		agents = append(agents, commandPollerTestAgent(agentID))
	}
	return &Daemon{
		store: commandPollerTestStore{commands: commands},
		sup: &supervisor.Supervisor{
			ConfigSnapshot: func() *config.DaemonConfig {
				return &config.DaemonConfig{}
			},
			WorkspaceID: "ws-1",
			NodeID:      "node-1",
			Shutdown:    make(chan struct{}),
			Agents:      agents,
		},
	}
}

func commandPollerTestAgent(agentID string) *supervisor.AgentProcess {
	return &supervisor.AgentProcess{
		Entry:                 config.AgentEntry{Worktree: agentID, Role: "task"},
		LifecycleGenerationAt: time.Now().UTC(),
		OwnershipLeaseID:      "lease-" + agentID,
		OwnershipOwnerID:      "node-1",
		OwnershipNodeID:       "node-1",
		OwnershipLeaseToken:   "token-" + agentID,
		OwnershipFencingToken: 1,
		OwnershipAcquired:     make(chan struct{}),
	}
}

func acquireCommandOwnership(
	t *testing.T,
	st store.Store,
	workspaceKey,
	agentID,
	nodeID,
	ownerID string,
) (*domain.AgentOwnershipLease, store.AgentCommandAck) {
	t.Helper()
	lease, err := st.AgentOwnershipLeases().Acquire(context.Background(), store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: workspaceKey,
		AgentID:      agentID,
		OwnerID:      ownerID,
		NodeID:       nodeID,
		TTL:          time.Minute,
	})
	if err != nil {
		t.Fatalf("acquire command ownership for %s: %v", agentID, err)
	}
	return lease, store.AgentCommandAck{
		NodeID:  nodeID,
		OwnerID: ownerID,
		AgentCommandOwnershipProof: store.AgentCommandOwnershipProof{
			OwnershipLeaseID:      lease.LeaseID,
			OwnershipToken:        lease.Token,
			OwnershipFencingToken: lease.FencingToken,
		},
	}
}

func commandCompletionFromAck(
	ack store.AgentCommandAck,
	status domain.AgentCommandStatus,
) store.AgentCommandComplete {
	return store.AgentCommandComplete{
		NodeID:                     ack.NodeID,
		OwnerID:                    ack.OwnerID,
		Status:                     status,
		AgentCommandOwnershipProof: ack.AgentCommandOwnershipProof,
	}
}

func commandPollerAgentFromLease(lease *domain.AgentOwnershipLease) *supervisor.AgentProcess {
	return &supervisor.AgentProcess{
		Entry:                 config.AgentEntry{Worktree: lease.AgentID, Role: "task"},
		LifecycleGenerationAt: time.Now().UTC(),
		OwnershipLeaseID:      lease.LeaseID,
		OwnershipOwnerID:      lease.OwnerID,
		OwnershipNodeID:       lease.NodeID,
		OwnershipLeaseToken:   lease.Token,
		OwnershipFencingToken: lease.FencingToken,
		OwnershipAcquired:     make(chan struct{}),
	}
}

func waitForCommandSignal(t *testing.T, signals <-chan string, want string) {
	t.Helper()
	select {
	case got := <-signals:
		if got != want {
			t.Fatalf("command signal = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command %q", want)
	}
}

func runAgentCommandPoll(t *testing.T, d *Daemon, unblock func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		d.pollAgentCommands()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		unblock()
		<-done
		t.Fatal("agent command poll blocked on command execution")
	}
}

func waitForCondition(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func waitForCommandSignals(t *testing.T, signals <-chan string, wants ...string) {
	t.Helper()
	remaining := make(map[string]struct{}, len(wants))
	for _, want := range wants {
		remaining[want] = struct{}{}
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for len(remaining) > 0 {
		select {
		case got := <-signals:
			if _, ok := remaining[got]; !ok {
				t.Fatalf("unexpected or duplicate command signal %q", got)
			}
			delete(remaining, got)
		case <-timer.C:
			t.Fatalf("timed out waiting for command signals: %v", remaining)
		}
	}
}

type dispatchTestAgentCommandStore struct {
	mu                     sync.Mutex
	commands               []*domain.AgentCommand
	ackCalls               map[string]int
	blockCommandID         string
	blockCompleteCommandID string
	started                chan string
	release                chan struct{}
	completeStarted        chan string
	completeRelease        chan struct{}
	completeCalls          map[string]int
	completeFailures       map[string]int
	releaseOnce            sync.Once
	completeReleaseOnce    sync.Once
}

func newDispatchTestAgentCommandStore(commands []*domain.AgentCommand) *dispatchTestAgentCommandStore {
	return &dispatchTestAgentCommandStore{
		commands:         commands,
		ackCalls:         make(map[string]int),
		started:          make(chan string, len(commands)+1),
		release:          make(chan struct{}),
		completeStarted:  make(chan string, len(commands)+1),
		completeRelease:  make(chan struct{}),
		completeCalls:    make(map[string]int),
		completeFailures: make(map[string]int),
	}
}

func (s *dispatchTestAgentCommandStore) Create(context.Context, store.AgentCommandCreate) (*domain.AgentCommand, error) {
	return nil, nil
}

func (s *dispatchTestAgentCommandStore) Get(context.Context, string, string) (*domain.AgentCommand, error) {
	return nil, nil
}

func (s *dispatchTestAgentCommandStore) List(context.Context, string, store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	return s.commands, nil
}

func (s *dispatchTestAgentCommandStore) Ack(
	_ context.Context,
	workspaceKey,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	s.mu.Lock()
	s.ackCalls[commandID]++
	var source *domain.AgentCommand
	for _, command := range s.commands {
		if command != nil && command.WorkspaceKey == workspaceKey && command.CommandID == commandID {
			source = command
			break
		}
	}
	s.mu.Unlock()
	s.started <- commandID
	if commandID == s.blockCommandID {
		<-s.release
	}
	acked := &domain.AgentCommand{
		WorkspaceKey:          workspaceKey,
		CommandID:             commandID,
		TargetNodeID:          ack.NodeID,
		AckedBy:               ack.OwnerID,
		Status:                domain.AgentCommandAcked,
		OwnershipLeaseID:      ack.OwnershipLeaseID,
		OwnershipFencingToken: ack.OwnershipFencingToken,
	}
	if source != nil {
		acked.Cursor = source.Cursor
		acked.TargetAgentID = source.TargetAgentID
		acked.SessionID = source.SessionID
		acked.Type = source.Type
		acked.Payload = source.Payload
	} else {
		// Some dispatcher tests inject a command directly without exercising List.
		// Preserve the command identity those fixtures use, matching the real store's
		// Ack response instead of returning an identity-less aggregate.
		acked.TargetAgentID = "worker-1"
		acked.Type = "unsupported"
	}
	return acked, nil
}

func (s *dispatchTestAgentCommandStore) Complete(_ context.Context, workspaceKey, commandID string, _ store.AgentCommandComplete) (*domain.AgentCommand, error) {
	s.mu.Lock()
	s.completeCalls[commandID]++
	if s.completeFailures[commandID] > 0 {
		s.completeFailures[commandID]--
		s.mu.Unlock()
		return nil, errors.New("transient completion failure")
	}
	s.mu.Unlock()
	if commandID == s.blockCompleteCommandID {
		s.completeStarted <- commandID
		<-s.completeRelease
	}
	return &domain.AgentCommand{
		WorkspaceKey: workspaceKey,
		CommandID:    commandID,
		Status:       domain.AgentCommandSucceeded,
	}, nil
}

func (s *dispatchTestAgentCommandStore) completeCount(commandID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeCalls[commandID]
}

func (s *dispatchTestAgentCommandStore) ackCount(commandID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ackCalls[commandID]
}

func (s *dispatchTestAgentCommandStore) unblockAck() {
	s.releaseOnce.Do(func() {
		close(s.release)
	})
}

func (s *dispatchTestAgentCommandStore) unblockComplete() {
	s.completeReleaseOnce.Do(func() {
		close(s.completeRelease)
	})
}

type completionContextAgentCommandStore struct {
	completeCalls          int
	completeContextExpired bool
}

func (s *completionContextAgentCommandStore) Create(context.Context, store.AgentCommandCreate) (*domain.AgentCommand, error) {
	return nil, nil
}

func (s *completionContextAgentCommandStore) Get(context.Context, string, string) (*domain.AgentCommand, error) {
	return nil, nil
}

func (s *completionContextAgentCommandStore) List(context.Context, string, store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	return nil, nil
}

func (s *completionContextAgentCommandStore) Ack(
	ctx context.Context,
	workspaceKey,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	<-ctx.Done()
	return &domain.AgentCommand{
		WorkspaceKey:          workspaceKey,
		CommandID:             commandID,
		TargetAgentID:         "worker-1",
		TargetNodeID:          ack.NodeID,
		Type:                  "unsupported",
		AckedBy:               ack.OwnerID,
		Status:                domain.AgentCommandAcked,
		OwnershipLeaseID:      ack.OwnershipLeaseID,
		OwnershipFencingToken: ack.OwnershipFencingToken,
	}, nil
}

func (s *completionContextAgentCommandStore) Complete(ctx context.Context, workspaceKey, commandID string, _ store.AgentCommandComplete) (*domain.AgentCommand, error) {
	s.completeCalls++
	s.completeContextExpired = ctx.Err() != nil
	return &domain.AgentCommand{
		WorkspaceKey: workspaceKey,
		CommandID:    commandID,
		Status:       domain.AgentCommandSucceeded,
	}, nil
}

type blockingAgentCommandStore struct {
	mu               sync.Mutex
	ackCalls         int
	ackOrder         []string
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
		{WorkspaceKey: "ws-1", CommandID: "cmd-1", TargetAgentID: "worker-1", TargetNodeID: "node-1", Type: "unsupported", Status: domain.AgentCommandQueued},
		{WorkspaceKey: "ws-1", CommandID: "cmd-2", TargetAgentID: "worker-1", TargetNodeID: "node-1", Type: "unsupported", Status: domain.AgentCommandQueued},
	}, nil
}

func (s *blockingAgentCommandStore) Ack(
	ctx context.Context,
	_ string,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	s.mu.Lock()
	s.ackCalls++
	s.ackOrder = append(s.ackOrder, commandID)
	call := s.ackCalls
	s.mu.Unlock()

	if call == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	s.mu.Lock()
	s.secondAckExpired = ctx.Err() != nil
	s.mu.Unlock()
	return &domain.AgentCommand{
		WorkspaceKey:          "ws-1",
		CommandID:             commandID,
		TargetAgentID:         "worker-1",
		TargetNodeID:          ack.NodeID,
		Type:                  "unsupported",
		AckedBy:               ack.OwnerID,
		Status:                domain.AgentCommandAcked,
		OwnershipLeaseID:      ack.OwnershipLeaseID,
		OwnershipFencingToken: ack.OwnershipFencingToken,
	}, nil
}

func (s *blockingAgentCommandStore) Complete(context.Context, string, string, store.AgentCommandComplete) (*domain.AgentCommand, error) {
	return &domain.AgentCommand{WorkspaceKey: "ws-1", Status: domain.AgentCommandSucceeded}, nil
}

type commandPollerTestStore struct {
	commands  store.AgentCommandStore
	ownership store.AgentOwnershipLeaseStore
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
	if s.ownership != nil {
		return s.ownership
	}
	return emptyAgentOwnershipLeaseStore{}
}
func (s commandPollerTestStore) AgentCommands() store.AgentCommandStore {
	return s.commands
}
func (s commandPollerTestStore) AgentInboxMessages() store.AgentInboxMessageStore {
	return nil
}
func (s commandPollerTestStore) Drivers() store.DriverStore { return nil }
func (s commandPollerTestStore) DriverVersions() store.DriverVersionStore {
	return nil
}
func (s commandPollerTestStore) WorkerProfiles() store.WorkerProfileStore {
	return nil
}
func (s commandPollerTestStore) AgentServices() store.AgentServiceStore {
	return nil
}
func (s commandPollerTestStore) TriggerBindings() store.TriggerBindingStore {
	return nil
}
func (s commandPollerTestStore) TriggerEvents() store.TriggerEventStore {
	return nil
}
func (s commandPollerTestStore) TriggerDeliveries() store.TriggerDeliveryStore {
	return nil
}
func (s commandPollerTestStore) TriggerRoutes() store.TriggerRouteDispatcher {
	return nil
}
func (s commandPollerTestStore) DriverRuns() store.DriverRunStore { return nil }
func (s commandPollerTestStore) DriverSteps() store.DriverStepStore {
	return nil
}
func (s commandPollerTestStore) TaskRuns() store.TaskRunStore { return nil }
func (s commandPollerTestStore) TaskRunEvents() store.TaskRunEventStore {
	return nil
}
func (s commandPollerTestStore) Outbox() store.OutboxStore                  { return nil }
func (s commandPollerTestStore) Awaits() store.AwaitStore                   { return nil }
func (s commandPollerTestStore) Connectors() store.ConnectorStore           { return nil }
func (s commandPollerTestStore) ConnectorGrants() store.ConnectorGrantStore { return nil }
func (s commandPollerTestStore) ConnectorCalls() store.ConnectorAuditStore  { return nil }
func (s commandPollerTestStore) Workers() store.WorkerStore                 { return nil }
func (s commandPollerTestStore) Roles() store.RoleStore                     { return nil }
func (s commandPollerTestStore) Daemon() store.DaemonProfileStore           { return nil }
func (s commandPollerTestStore) Close() error                               { return nil }

type emptyAgentOwnershipLeaseStore struct{}

func (emptyAgentOwnershipLeaseStore) Acquire(
	context.Context,
	store.AgentOwnershipLeaseAcquire,
) (*domain.AgentOwnershipLease, error) {
	return nil, domain.ErrNotFound
}

func (emptyAgentOwnershipLeaseStore) Get(
	context.Context,
	string,
	string,
) (*domain.AgentOwnershipLease, error) {
	return nil, domain.ErrNotFound
}

func (emptyAgentOwnershipLeaseStore) List(
	context.Context,
	string,
	store.AgentOwnershipLeaseFilter,
) ([]*domain.AgentOwnershipLease, error) {
	return nil, nil
}

func (emptyAgentOwnershipLeaseStore) Heartbeat(
	context.Context,
	string,
	string,
	string,
	time.Duration,
) (*domain.AgentOwnershipLease, error) {
	return nil, domain.ErrNotFound
}

func (emptyAgentOwnershipLeaseStore) Release(
	context.Context,
	string,
	string,
	string,
) (*domain.AgentOwnershipLease, error) {
	return nil, domain.ErrNotFound
}

type recordingOwnershipListStore struct {
	emptyAgentOwnershipLeaseStore
	leases []*domain.AgentOwnershipLease
	filter store.AgentOwnershipLeaseFilter
}

func (s *recordingOwnershipListStore) List(
	_ context.Context,
	_ string,
	filter store.AgentOwnershipLeaseFilter,
) ([]*domain.AgentOwnershipLease, error) {
	s.filter = filter
	return s.leases, nil
}

type lostAckResponseAgentCommandStore struct {
	store.AgentCommandStore
	mu            sync.Mutex
	lostCommandID string
	lost          bool
	completeOrder []string
}

type acquireAfterAckAgentCommandStore struct {
	store.AgentCommandStore
	ownership    store.AgentOwnershipLeaseStore
	commandID    string
	acquire      store.AgentOwnershipLeaseAcquire
	loseResponse bool

	mu            sync.Mutex
	advanced      bool
	ackOrder      []string
	completeOrder []string
}

type acquireBeforeCompleteAgentCommandStore struct {
	store.AgentCommandStore
	ownership store.AgentOwnershipLeaseStore
	commandID string
	acquire   store.AgentOwnershipLeaseAcquire

	mu            sync.Mutex
	advanced      bool
	completeCalls int
}

func (s *acquireBeforeCompleteAgentCommandStore) Complete(
	ctx context.Context,
	workspaceKey,
	commandID string,
	update store.AgentCommandComplete,
) (*domain.AgentCommand, error) {
	s.mu.Lock()
	s.completeCalls++
	advance := commandID == s.commandID && !s.advanced
	if advance {
		s.advanced = true
	}
	s.mu.Unlock()
	if advance {
		if _, err := s.ownership.Acquire(ctx, s.acquire); err != nil {
			return nil, fmt.Errorf("advance ownership before Complete: %w", err)
		}
	}
	return s.AgentCommandStore.Complete(ctx, workspaceKey, commandID, update)
}

func (s *acquireAfterAckAgentCommandStore) Ack(
	ctx context.Context,
	workspaceKey,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	command, err := s.AgentCommandStore.Ack(ctx, workspaceKey, commandID, ack)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.ackOrder = append(s.ackOrder, commandID)
	advance := commandID == s.commandID && !s.advanced
	if advance {
		s.advanced = true
	}
	s.mu.Unlock()
	if !advance {
		return command, nil
	}
	if _, err := s.ownership.Acquire(ctx, s.acquire); err != nil {
		return nil, fmt.Errorf("advance ownership after Ack: %w", err)
	}
	if s.loseResponse {
		return nil, errors.New("synthetic lost Ack response after ownership advance")
	}
	return s.AgentCommandStore.Get(ctx, workspaceKey, commandID)
}

func (s *acquireAfterAckAgentCommandStore) Complete(
	ctx context.Context,
	workspaceKey,
	commandID string,
	update store.AgentCommandComplete,
) (*domain.AgentCommand, error) {
	command, err := s.AgentCommandStore.Complete(ctx, workspaceKey, commandID, update)
	if err == nil {
		s.mu.Lock()
		s.completeOrder = append(s.completeOrder, commandID)
		s.mu.Unlock()
	}
	return command, err
}

func (s *acquireAfterAckAgentCommandStore) ackOrderSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ackOrder...)
}

func (s *acquireAfterAckAgentCommandStore) completeOrderSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.completeOrder...)
}

type agentCommandAckObservation struct {
	commandID string
	err       error
}

type ackObservedAgentCommandStore struct {
	store.AgentCommandStore
	ackAttempts chan agentCommandAckObservation
	completed   chan string
}

func (s *ackObservedAgentCommandStore) Ack(
	ctx context.Context,
	workspaceKey string,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	command, err := s.AgentCommandStore.Ack(
		ctx,
		workspaceKey,
		commandID,
		ack,
	)
	s.ackAttempts <- agentCommandAckObservation{commandID: commandID, err: err}
	return command, err
}

func (s *ackObservedAgentCommandStore) Complete(
	ctx context.Context,
	workspaceKey string,
	commandID string,
	update store.AgentCommandComplete,
) (*domain.AgentCommand, error) {
	command, err := s.AgentCommandStore.Complete(ctx, workspaceKey, commandID, update)
	if err == nil {
		s.completed <- commandID
	}
	return command, err
}

type agentStoreOverride struct {
	store.Store
	agents store.AgentStore
}

func (s agentStoreOverride) Agents() store.AgentStore {
	return s.agents
}

type lifecycleObservedStore struct {
	store.Store
	agents   store.AgentStore
	commands store.AgentCommandStore
}

func (s lifecycleObservedStore) Agents() store.AgentStore {
	return s.agents
}

func (s lifecycleObservedStore) AgentCommands() store.AgentCommandStore {
	return s.commands
}

type commandOwnershipStoreOverride struct {
	store.Store
	commands  store.AgentCommandStore
	ownership store.AgentOwnershipLeaseStore
}

func (s commandOwnershipStoreOverride) AgentCommands() store.AgentCommandStore {
	return s.commands
}

func (s commandOwnershipStoreOverride) AgentOwnershipLeases() store.AgentOwnershipLeaseStore {
	return s.ownership
}

type firstAcquireUnavailableStore struct {
	store.AgentOwnershipLeaseStore
	mu       sync.Mutex
	failures int
	failed   int
}

func (s *firstAcquireUnavailableStore) Acquire(
	ctx context.Context,
	in store.AgentOwnershipLeaseAcquire,
) (*domain.AgentOwnershipLease, error) {
	s.mu.Lock()
	if s.failures > 0 {
		s.failures--
		s.failed++
		s.mu.Unlock()
		return nil, errors.New("synthetic ownership acquire unavailable")
	}
	s.mu.Unlock()
	return s.AgentOwnershipLeaseStore.Acquire(ctx, in)
}

type firstCompletionOwnershipConflictStore struct {
	store.AgentCommandStore
	commandID string

	mu            sync.Mutex
	conflicted    bool
	conflicts     int
	ackOrder      []string
	completeOrder []string
}

func (s *firstCompletionOwnershipConflictStore) Ack(
	ctx context.Context,
	workspaceKey,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	command, err := s.AgentCommandStore.Ack(ctx, workspaceKey, commandID, ack)
	if err == nil {
		s.mu.Lock()
		s.ackOrder = append(s.ackOrder, commandID)
		s.mu.Unlock()
	}
	return command, err
}

func (s *firstCompletionOwnershipConflictStore) Complete(
	ctx context.Context,
	workspaceKey,
	commandID string,
	update store.AgentCommandComplete,
) (*domain.AgentCommand, error) {
	s.mu.Lock()
	if commandID == s.commandID && !s.conflicted {
		s.conflicted = true
		s.conflicts++
		s.mu.Unlock()
		return nil, domain.ErrNotOwner
	}
	s.mu.Unlock()
	command, err := s.AgentCommandStore.Complete(ctx, workspaceKey, commandID, update)
	if err == nil {
		s.mu.Lock()
		s.completeOrder = append(s.completeOrder, commandID)
		s.mu.Unlock()
	}
	return command, err
}

func (s *firstCompletionOwnershipConflictStore) ackOrderSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ackOrder...)
}

func (s *firstCompletionOwnershipConflictStore) completeOrderSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.completeOrder...)
}

type lifecycleCallRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *lifecycleCallRecorder) record(call string) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

func (r *lifecycleCallRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type recordingAgentStore struct {
	store.AgentStore
	calls          *lifecycleCallRecorder
	fail           error
	closeOnFailure chan struct{}
	closeOnce      sync.Once
}

func (s *recordingAgentStore) Update(
	ctx context.Context,
	workspaceKey string,
	name string,
	update store.AgentUpdate,
) (*domain.Agent, error) {
	s.calls.record("update-agent")
	if s.fail != nil {
		if s.closeOnFailure != nil {
			s.closeOnce.Do(func() { close(s.closeOnFailure) })
		}
		return nil, s.fail
	}
	return s.AgentStore.Update(ctx, workspaceKey, name, update)
}

type recordingAgentCommandStore struct {
	store.AgentCommandStore
	calls *lifecycleCallRecorder
}

func (s *recordingAgentCommandStore) Complete(
	ctx context.Context,
	workspaceKey string,
	commandID string,
	update store.AgentCommandComplete,
) (*domain.AgentCommand, error) {
	s.calls.record("complete-command")
	return s.AgentCommandStore.Complete(ctx, workspaceKey, commandID, update)
}

type transientUpdateAgentStore struct {
	store.AgentStore
	failures int
	calls    int
}

func (s *transientUpdateAgentStore) Update(
	ctx context.Context,
	workspaceKey string,
	name string,
	update store.AgentUpdate,
) (*domain.Agent, error) {
	s.calls++
	if s.failures > 0 {
		s.failures--
		return nil, errors.New("transient agent projection failure")
	}
	return s.AgentStore.Update(ctx, workspaceKey, name, update)
}

func (s *lostAckResponseAgentCommandStore) Ack(
	ctx context.Context,
	workspaceKey string,
	commandID string,
	ack store.AgentCommandAck,
) (*domain.AgentCommand, error) {
	acked, err := s.AgentCommandStore.Ack(ctx, workspaceKey, commandID, ack)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if commandID == s.lostCommandID && !s.lost {
		s.lost = true
		return nil, errors.New("synthetic lost ack response")
	}
	return acked, nil
}

func (s *lostAckResponseAgentCommandStore) Complete(
	ctx context.Context,
	workspaceKey string,
	commandID string,
	update store.AgentCommandComplete,
) (*domain.AgentCommand, error) {
	s.mu.Lock()
	s.completeOrder = append(s.completeOrder, commandID)
	s.mu.Unlock()
	return s.AgentCommandStore.Complete(ctx, workspaceKey, commandID, update)
}

func acknowledgedPersistentStartFixture(
	t *testing.T,
) (store.Store, *domain.AgentCommand, *domain.AgentOwnershipLease) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:  "ws-1",
		Name: "Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws-1",
		Name:         "worker-1",
		RoleName:     "task",
		Mode:         domain.AgentModeService,
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create persistent agent: %v", err)
	}
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws-1",
		CommandID:     "cmd-recovered-start",
		TargetAgentID: "worker-1",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create Start command: %v", err)
	}
	command, err = st.AgentCommands().Ack(
		ctx,
		command.WorkspaceKey,
		command.CommandID,
		store.AgentCommandAck{NodeID: "node-1", OwnerID: "node-1"},
	)
	if err != nil {
		t.Fatalf("ack Start command: %v", err)
	}
	lease, _ := acquireCommandOwnership(t, st, "ws-1", "worker-1", "node-1", "node-1")
	command, err = st.AgentCommands().Get(ctx, command.WorkspaceKey, command.CommandID)
	if err != nil {
		t.Fatalf("get Start command after ownership acquire: %v", err)
	}
	return st, command, lease
}

func mustCommandOwnerID(t *testing.T, sup *supervisor.Supervisor) string {
	t.Helper()
	ownerID, err := sup.CommandOwnerID()
	if err != nil {
		t.Fatalf("resolve command owner: %v", err)
	}
	return ownerID
}

func acknowledgedPersistentStartDaemon(
	st store.Store,
	lease *domain.AgentOwnershipLease,
	shutdown chan struct{},
) *Daemon {
	if shutdown == nil {
		shutdown = make(chan struct{})
	}
	entry := config.AgentEntry{
		Worktree: "worker-1",
		Role:     "task",
		Mode:     domain.AgentModeService,
	}
	d := &Daemon{
		config: makeDaemonConfig([]AgentEntry{entry}, nil),
		store:  st,
		sup: &supervisor.Supervisor{
			Agents: []*supervisor.AgentProcess{{
				Entry:                 entry,
				LifecycleGenerationAt: time.Now().UTC(),
				OwnershipLeaseID:      lease.LeaseID,
				OwnershipOwnerID:      lease.OwnerID,
				OwnershipNodeID:       lease.NodeID,
				OwnershipLeaseToken:   lease.Token,
				OwnershipFencingToken: lease.FencingToken,
				OwnershipAcquired:     make(chan struct{}),
			}},
			WorkspaceID:   "ws-1",
			NodeID:        "node-1",
			ControlStore:  st,
			Shutdown:      shutdown,
			StoppedAgents: make(map[string]struct{}),
		},
	}
	d.sup.ConfigSnapshot = d.configSnapshot
	return d
}
