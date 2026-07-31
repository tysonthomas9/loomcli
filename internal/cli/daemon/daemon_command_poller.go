package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	agentCommandPollTimeout             = 5 * time.Second
	agentCommandRetryDelay              = time.Second
	agentCommandRestartOwnershipTimeout = 30 * time.Second
)

type agentCommandKey struct {
	workspaceKey string
	commandID    string
}

type agentCommandTarget struct {
	workspaceKey string
	agentID      string
}

type agentCommandQueue struct {
	commands []*domain.AgentCommand
	running  bool
}

var recoverableAgentCommandStatuses = []domain.AgentCommandStatus{
	domain.AgentCommandQueued,
	domain.AgentCommandAcked,
	domain.AgentCommandRunning,
}

// agentCommandDispatcher serializes commands for one agent while allowing
// commands for different agents to run concurrently. pending spans both queued
// and executing commands so a still-queued FleetDB row cannot be dispatched
// again by a later poll cycle.
type agentCommandDispatcher struct {
	mu      sync.Mutex
	queues  map[agentCommandTarget]*agentCommandQueue
	pending map[agentCommandKey]struct{}
}

func (d *Daemon) startAgentCommandPoller() {
	if d.store == nil || d.sup.WorkspaceID == "" || d.store.AgentCommands() == nil {
		return
	}
	d.sup.Wg.Add(1)
	go func() {
		defer d.sup.Wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-d.sup.Shutdown:
				return
			case <-ticker.C:
				d.pollAgentCommands()
			}
		}
	}()
}

func (d *Daemon) pollAgentCommands() {
	cycleStart := time.Now()
	// Inherit the daemon's process-wide root span via cmdstore.RootContext()
	// so the cycle attaches to the daemon trace tree (not a detached root).
	ctx, span := startCommandPollSpan(cmdstore.RootContext())
	defer span.End()

	ctx, cancel := context.WithTimeout(ctx, agentCommandPollTimeout)
	defer cancel()
	cmds, err := d.listPollableAgentCommands(ctx)
	if err != nil {
		recordCommandPollErr(span, err)
		span.SetAttributes(
			attribute.Int("result.count", 0),
			attribute.Int64("cycle.duration_ms", time.Since(cycleStart).Milliseconds()),
		)
		slog.Warn("agent command poll failed", "err", err)
		return
	}
	span.SetAttributes(
		attribute.Int("result.count", len(cmds)),
		attribute.Int64("cycle.duration_ms", time.Since(cycleStart).Milliseconds()),
	)
	for _, cmd := range cmds {
		if cmd == nil || !pollableAgentCommandStatus(cmd.Status) {
			continue
		}
		d.dispatchAgentCommand(cmd)
	}
}

// listPollableAgentCommands exhausts every active-status page before sorting.
// Other-node commands therefore cannot hide this daemon's work, and commands
// from different statuses retain one global cursor order per target.
func (d *Daemon) listPollableAgentCommands(ctx context.Context) ([]*domain.AgentCommand, error) {
	ownership, err := d.agentCommandOwnershipSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("snapshot agent command ownership: %w", err)
	}
	// Exclude work already queued or executing in this process before either
	// the per-status or global result cap is applied. dispatchAgentCommand
	// still performs the authoritative under-lock dedupe, so a command added
	// after this snapshot cannot execute twice.
	pending := d.pendingAgentCommandSnapshot()
	active, err := listAgentCommandsByStatuses(
		ctx,
		d.store.AgentCommands(),
		d.sup.WorkspaceID,
		store.AgentCommandFilter{},
		recoverableAgentCommandStatuses,
		func(cmd *domain.AgentCommand) bool {
			if !d.agentCommandBelongsToThisDaemonWithOwnership(cmd, ownership) {
				return false
			}
			_, alreadyPending := pending[agentCommandKey{
				workspaceKey: cmd.WorkspaceKey,
				commandID:    cmd.CommandID,
			}]
			return !alreadyPending
		},
		50,
	)
	if err != nil {
		return nil, err
	}
	if len(active) > 50 {
		active = active[:50]
	}
	return active, nil
}

func (d *Daemon) pendingAgentCommandSnapshot() map[agentCommandKey]struct{} {
	if d == nil {
		return nil
	}
	dispatcher := &d.agentCommandDispatcher
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.pending) == 0 {
		return nil
	}
	pending := make(map[agentCommandKey]struct{}, len(dispatcher.pending))
	for key := range dispatcher.pending {
		pending[key] = struct{}{}
	}
	return pending
}

func listAgentCommandsByStatuses(
	ctx context.Context,
	commands store.AgentCommandStore,
	workspaceKey string,
	filter store.AgentCommandFilter,
	statuses []domain.AgentCommandStatus,
	include func(*domain.AgentCommand) bool,
	perStatusLimit int,
) ([]*domain.AgentCommand, error) {
	out := make([]*domain.AgentCommand, 0, 50)
	for _, status := range statuses {
		statusCommands, err := listAgentCommandStatus(
			ctx,
			commands,
			workspaceKey,
			filter,
			status,
			include,
			perStatusLimit,
		)
		if err != nil {
			return nil, err
		}
		out = append(out, statusCommands...)
	}
	sortAgentCommandsByCursor(out)
	return out, nil
}

func listAgentCommandStatus(
	ctx context.Context,
	commands store.AgentCommandStore,
	workspaceKey string,
	filter store.AgentCommandFilter,
	status domain.AgentCommandStatus,
	include func(*domain.AgentCommand) bool,
	resultLimit int,
) ([]*domain.AgentCommand, error) {
	const pageLimit = 50

	filter.Status = status
	filter.Limit = pageLimit
	out := make([]*domain.AgentCommand, 0, pageLimit)
	for {
		page, err := commands.List(ctx, workspaceKey, filter)
		if err != nil {
			return nil, err
		}
		nextCursor := filter.AfterCursor
		for _, cmd := range page {
			if cmd == nil {
				continue
			}
			nextCursor = max(nextCursor, cmd.Cursor)
			if cmd.Status == status && (include == nil || include(cmd)) {
				out = append(out, cmd)
			}
		}
		if resultLimit > 0 && len(out) >= resultLimit {
			return out[:resultLimit], nil
		}
		if len(page) < pageLimit {
			return out, nil
		}
		if nextCursor <= filter.AfterCursor {
			return nil, fmt.Errorf("agent command pagination did not advance for status %q", status)
		}
		filter.AfterCursor = nextCursor
	}
}

func sortAgentCommandsByCursor(commands []*domain.AgentCommand) {
	sort.SliceStable(commands, func(i, j int) bool {
		if commands[i].Cursor == commands[j].Cursor {
			return false
		}
		return commands[i].Cursor < commands[j].Cursor
	})
}

func (d *Daemon) dispatchAgentCommand(cmd *domain.AgentCommand) {
	if cmd == nil {
		return
	}
	select {
	case <-d.sup.Shutdown:
		return
	default:
	}

	key := agentCommandKey{workspaceKey: cmd.WorkspaceKey, commandID: cmd.CommandID}
	target := agentCommandTarget{workspaceKey: cmd.WorkspaceKey, agentID: cmd.TargetAgentID}
	dispatcher := &d.agentCommandDispatcher

	dispatcher.mu.Lock()
	if dispatcher.pending == nil {
		dispatcher.pending = make(map[agentCommandKey]struct{})
		dispatcher.queues = make(map[agentCommandTarget]*agentCommandQueue)
	}
	if _, exists := dispatcher.pending[key]; exists {
		dispatcher.mu.Unlock()
		return
	}
	dispatcher.pending[key] = struct{}{}

	queue := dispatcher.queues[target]
	if queue == nil {
		queue = &agentCommandQueue{}
		dispatcher.queues[target] = queue
	}
	queue.commands = append(queue.commands, cmd)
	if queue.running {
		dispatcher.mu.Unlock()
		return
	}
	queue.running = true

	// The poller itself keeps this WaitGroup non-zero while dispatch can occur,
	// so adding a target worker cannot race Stop's first zero-valued Wait.
	d.sup.Wg.Add(1)
	dispatcher.mu.Unlock()
	go d.runAgentCommandQueue(target)
}

func (d *Daemon) runAgentCommandQueue(target agentCommandTarget) {
	defer d.sup.Wg.Done()

	for {
		dispatcher := &d.agentCommandDispatcher
		dispatcher.mu.Lock()
		queue := dispatcher.queues[target]
		if queue == nil || len(queue.commands) == 0 {
			delete(dispatcher.queues, target)
			dispatcher.mu.Unlock()
			return
		}
		if daemonShutdownRequested(d.sup.Shutdown) {
			dispatcher.clearTargetLocked(target, nil)
			dispatcher.mu.Unlock()
			return
		}
		cmd := queue.commands[0]
		queue.commands[0] = nil
		queue.commands = queue.commands[1:]
		dispatcher.mu.Unlock()

		if daemonShutdownRequested(d.sup.Shutdown) {
			dispatcher.mu.Lock()
			dispatcher.clearTargetLocked(target, cmd)
			dispatcher.mu.Unlock()
			return
		}
		if !d.handleAgentCommand(cmd) {
			// Ack did not take effect, so this command remains the head of the
			// target's durable FIFO. Put it back before retrying; advancing to
			// the next command here could restart an agent before its preceding
			// Stop was ever accepted.
			if !d.retryAgentCommandAtHead(target, cmd) {
				return
			}
			continue
		}

		key := agentCommandKey{workspaceKey: cmd.WorkspaceKey, commandID: cmd.CommandID}
		dispatcher.mu.Lock()
		delete(dispatcher.pending, key)
		dispatcher.mu.Unlock()
	}
}

func (d *Daemon) retryAgentCommandAtHead(target agentCommandTarget, cmd *domain.AgentCommand) bool {
	dispatcher := &d.agentCommandDispatcher
	dispatcher.mu.Lock()
	queue := dispatcher.queues[target]
	if queue == nil {
		queue = &agentCommandQueue{running: true}
		dispatcher.queues[target] = queue
	}
	queue.commands = append(queue.commands, nil)
	copy(queue.commands[1:], queue.commands[:len(queue.commands)-1])
	queue.commands[0] = cmd
	dispatcher.mu.Unlock()

	timer := time.NewTimer(agentCommandRetryDelay)
	select {
	case <-d.sup.Shutdown:
		timer.Stop()
		dispatcher.mu.Lock()
		dispatcher.clearTargetLocked(target, nil)
		dispatcher.mu.Unlock()
		return false
	case <-timer.C:
		return true
	}
}

func (d *agentCommandDispatcher) clearTargetLocked(target agentCommandTarget, current *domain.AgentCommand) {
	if current != nil {
		delete(d.pending, agentCommandKey{
			workspaceKey: current.WorkspaceKey,
			commandID:    current.CommandID,
		})
	}
	if queue := d.queues[target]; queue != nil {
		for _, cmd := range queue.commands {
			if cmd == nil {
				continue
			}
			delete(d.pending, agentCommandKey{
				workspaceKey: cmd.WorkspaceKey,
				commandID:    cmd.CommandID,
			})
		}
	}
	delete(d.queues, target)
}

func daemonShutdownRequested(shutdown <-chan struct{}) bool {
	select {
	case <-shutdown:
		return true
	default:
		return false
	}
}

func pollableAgentCommandStatus(status domain.AgentCommandStatus) bool {
	switch status {
	case domain.AgentCommandQueued, domain.AgentCommandAcked, domain.AgentCommandRunning:
		return true
	default:
		return false
	}
}

func knownAgentLifecycleCommandType(commandType string) bool {
	switch commandType {
	case "start", "stop", "restart", "yield":
		return true
	default:
		return false
	}
}

// handleAgentCommand returns true only after the durable command is terminal
// (or another owner has conclusively consumed it). An Acked/Running command
// whose projection or terminal write is incomplete remains at the head of the
// local per-agent FIFO; recovery satisfaction prevents replaying an applied
// lifecycle side effect.
func (d *Daemon) handleAgentCommand(cmd *domain.AgentCommand) bool {
	cmd, recovering, consumed, authority := d.prepareAgentCommand(cmd)
	if consumed {
		return true
	}
	if cmd == nil {
		return false
	}
	defer authority.release()

	resp, recoveryErrorClass, replacement := d.executeAgentCommand(cmd, recovering, &authority)
	if !d.settleAgentCommandExecutionOwnership(
		cmd,
		recovering,
		replacement,
		&resp,
		&recoveryErrorClass,
		&authority,
	) {
		return false
	}

	// The local reconciler intent is synchronized before the durable command
	// becomes terminal. The legacy Agent projection is deliberately untouched;
	// Execution owns observed runtime state.
	d.reassertAgentCommandLifecycle(cmd, resp)
	update := agentCommandTerminalUpdate(resp, recoveryErrorClass)
	completed := d.completeAgentCommand(cmd, update, &authority)
	if completed {
		slog.Info("agent command completed",
			"command_id", cmd.CommandID,
			"type", cmd.Type,
			"target_agent_id", cmd.TargetAgentID,
			"status", update.Status,
			"result", update.Result)
	}
	return completed
}

func agentCommandTerminalUpdate(
	resp DaemonControlResponse,
	recoveryErrorClass string,
) store.AgentCommandComplete {
	if resp.Success {
		return store.AgentCommandComplete{
			Status: domain.AgentCommandSucceeded,
			Result: "ok",
		}
	}
	if recoveryErrorClass == "" {
		recoveryErrorClass = "control_error"
	}
	return store.AgentCommandComplete{
		Status:     domain.AgentCommandFailed,
		Result:     resp.Error,
		ErrorClass: recoveryErrorClass,
	}
}

func (d *Daemon) prepareAgentCommand(
	cmd *domain.AgentCommand,
) (*domain.AgentCommand, bool, bool, agentCommandAuthority) {
	var authority agentCommandAuthority
	recovering := cmd.Status == domain.AgentCommandAcked || cmd.Status == domain.AgentCommandRunning
	if recovering {
		prepared, resuming, consumed := d.prepareRecoveringAgentCommand(cmd, &authority)
		return prepared, resuming, consumed, authority
	}

	ackCtx, ackCancel := context.WithTimeout(context.Background(), agentCommandPollTimeout)
	ownerID, ownerErr := d.sup.CommandOwnerID()
	if ownerErr != nil {
		ackCancel()
		slog.Error("durable lifecycle command owner unavailable; refusing acknowledgement",
			"command_id", cmd.CommandID,
			"node_id", d.sup.NodeID,
			"err", ownerErr)
		return nil, false, false, authority
	}
	if !d.prepareQueuedAgentCommandAuthority(ackCtx, cmd, ownerID, &authority) {
		ackCancel()
		return nil, false, false, authority
	}
	prepared, resuming, consumed := d.ackPreparedAgentCommand(
		ackCtx,
		ackCancel,
		cmd,
		ownerID,
		&authority,
	)
	return prepared, resuming, consumed, authority
}

func (d *Daemon) ackPreparedAgentCommand(
	ctx context.Context,
	cancel context.CancelFunc,
	cmd *domain.AgentCommand,
	ownerID string,
	authority *agentCommandAuthority,
) (*domain.AgentCommand, bool, bool) {
	acked, err := d.store.AgentCommands().Ack(
		ctx,
		cmd.WorkspaceKey,
		cmd.CommandID,
		store.AgentCommandAck{
			NodeID:                     d.sup.NodeID,
			OwnerID:                    ownerID,
			AgentCommandOwnershipProof: authority.storeProof(),
		},
	)
	cancel()
	if err == nil {
		if acked != nil {
			cmd = acked
		}
		if !d.alignAgentCommandAuthority(cmd, authority) {
			slog.Error("agent command ack returned without claimant ownership; refusing execution",
				"command_id", cmd.CommandID,
				"target_node_id", cmd.TargetNodeID,
				"acked_by", cmd.AckedBy,
				"node_id", d.sup.NodeID)
			return nil, false, true
		}
		return cmd, false, false
	}

	current, consume := d.resolveAgentCommandAfterAckFailure(cmd, authority)
	if current != nil {
		return current, true, false
	}
	if !consume {
		slog.Warn("agent command ack failed", "command_id", cmd.CommandID, "err", err)
	}
	return nil, false, consume
}

// resolveAgentCommandAfterAckFailure distinguishes an uncommitted Ack from a
// lost Ack response. A durable Ack must resume at the current FIFO head;
// terminal or deleted rows are consumed without replay.
func (d *Daemon) resolveAgentCommandAfterAckFailure(
	cmd *domain.AgentCommand,
	authority *agentCommandAuthority,
) (*domain.AgentCommand, bool) {
	getCtx, getCancel := context.WithTimeout(context.Background(), agentCommandPollTimeout)
	current, err := d.store.AgentCommands().Get(getCtx, cmd.WorkspaceKey, cmd.CommandID)
	getCancel()
	if errors.Is(err, domain.ErrNotFound) {
		slog.Info("agent command disappeared before ack; dropping local dispatch",
			"command_id", cmd.CommandID)
		return nil, true
	}
	if err != nil || current == nil || current.Status == domain.AgentCommandQueued {
		return nil, false
	}
	if current.Status == domain.AgentCommandAcked || current.Status == domain.AgentCommandRunning {
		if !d.agentCommandClaimedByThisProcess(current) {
			slog.Info("agent command ack was won by another daemon; dropping local dispatch",
				"command_id", cmd.CommandID,
				"target_node_id", current.TargetNodeID,
				"acked_by", current.AckedBy,
				"node_id", d.sup.NodeID)
			return nil, true
		}
		if !d.alignAgentCommandAuthority(current, authority) {
			// Stable claimant identity matches this runtime, but the generation
			// advanced between Ack and response resolution. Keep this durable
			// FIFO head retryable until same-owner Acquire can align authority.
			return nil, false
		}
		slog.Info("agent command ack response was lost; resuming durable FIFO head",
			"command_id", cmd.CommandID,
			"status", current.Status)
		return current, false
	}
	slog.Info("agent command became terminal before ack; dropping local dispatch",
		"command_id", cmd.CommandID,
		"status", current.Status)
	return nil, true
}

func (d *Daemon) executeAgentCommand(
	cmd *domain.AgentCommand,
	recovering bool,
	authority *agentCommandAuthority,
) (DaemonControlResponse, string, *supervisor.AgentProcess) {
	if recovering {
		if satisfied, errClass := d.acknowledgedAgentCommandSatisfied(cmd); satisfied {
			return DaemonControlResponse{Success: true}, "", nil
		} else if errClass != "" {
			message := "acknowledged restart has no provably newer runtime generation; refusing duplicate restart"
			if errClass == "ambiguous_ephemeral_start_recovery" {
				message = "acknowledged ephemeral start does not match the live task generation; refusing duplicate task attempt"
			}
			return DaemonControlResponse{
				Error: message,
			}, errClass, nil
		}
	}

	var resp DaemonControlResponse
	var replacement *supervisor.AgentProcess
	switch cmd.Type {
	case "start":
		// A recovery-only transient lease must not share its bearer token with
		// the replacement AgentProcess: releasing the stale generation after a
		// same-owner Acquire would otherwise release the replacement too. The
		// Acked command itself fences other stable owners across this gap.
		authority.releaseTransientBeforeReplacement()
		resp, replacement = d.handleAgentControlStartCommand(
			cmd.TargetAgentID,
			cmd.Payload["task_id"],
			cmd.Payload["parent_session_id"],
		)
	case "stop":
		resp = d.handleAgentControlStop(cmd.TargetAgentID, cmd.Payload["force"] == "true")
	case "restart":
		authority.releaseTransientBeforeReplacement()
		resp, replacement = d.handleAgentControlRestartCommand(cmd.TargetAgentID)
	case "yield":
		resp = d.handleAgentControlYield(cmd.TargetAgentID)
	default:
		resp = DaemonControlResponse{Error: fmt.Sprintf("unsupported agent command type %q", cmd.Type)}
	}
	_ = authority // authority is threaded explicitly to keep raw proof scoped to this execution frame.
	return resp, "", replacement
}

func (d *Daemon) acknowledgedAgentCommandSatisfied(cmd *domain.AgentCommand) (bool, string) {
	switch cmd.Type {
	case "start":
		return d.acknowledgedStartSatisfied(cmd)
	case "stop":
		return d.isAgentStopped(cmd.TargetAgentID) || !d.isAgentRunning(cmd.TargetAgentID), ""
	case "yield":
		if cmd.OwnershipFencingToken == 0 && !d.isAgentRunning(cmd.TargetAgentID) {
			// A proofless Ack is an authoritative absence path. Yield cannot
			// succeed without proving a concrete owned generation, so recover
			// this as a normal control failure instead of treating absence as
			// an idempotent successful yield.
			return false, ""
		}
		return d.agentYieldAlreadyApplied(cmd.TargetAgentID), ""
	case "restart":
		if d.agentGenerationAfterCommand(cmd) {
			return true, ""
		}
		return false, "ambiguous_restart_recovery"
	default:
		return false, ""
	}
}

func (d *Daemon) acknowledgedStartSatisfied(cmd *domain.AgentCommand) (bool, string) {
	d.sup.AgentsMu.RLock()
	defer d.sup.AgentsMu.RUnlock()
	for _, agent := range d.sup.Agents {
		if agent.Entry.Worktree != cmd.TargetAgentID {
			continue
		}
		if agent.Entry.Mode != domain.AgentModeEphemeral {
			// A stopped marker and the runtime slice are one lifecycle state.
			// A replacement daemon can observe a registered process record
			// before the Start side effect was applied, so registration alone
			// is not proof that an acknowledged Start already succeeded.
			_, stopped := d.sup.StoppedAgents[cmd.TargetAgentID]
			return !stopped, ""
		}
		agent.Mu.Lock()
		requestedTaskID := agent.RequestedTaskID
		assignedTaskID := agent.AssignedTaskID
		agent.Mu.Unlock()
		if taskID := cmd.Payload["task_id"]; taskID != "" &&
			(taskID == requestedTaskID || taskID == assignedTaskID) {
			return true, ""
		}
		return false, "ambiguous_ephemeral_start_recovery"
	}
	return false, ""
}

func (d *Daemon) agentGenerationAfterCommand(cmd *domain.AgentCommand) bool {
	acceptedAt := cmd.UpdatedAt
	if cmd.AckedAt != nil {
		acceptedAt = *cmd.AckedAt
	}
	if acceptedAt.IsZero() {
		return false
	}
	d.sup.AgentsMu.RLock()
	defer d.sup.AgentsMu.RUnlock()
	for _, agent := range d.sup.Agents {
		if agent.Entry.Worktree == cmd.TargetAgentID {
			return agent.LifecycleGenerationAt.After(acceptedAt)
		}
	}
	return false
}

func (d *Daemon) agentYieldAlreadyApplied(name string) bool {
	d.sup.AgentsMu.RLock()
	defer d.sup.AgentsMu.RUnlock()
	for _, agent := range d.sup.Agents {
		if agent.Entry.Worktree != name {
			continue
		}
		agent.Mu.Lock()
		pid := agent.Pid
		agent.Mu.Unlock()
		return pid == 0 || supervisor.IsYieldRequested(agent.WorktreePath)
	}
	return true
}

// completeAgentCommand retries only the terminal status write, never the
// lifecycle side effect or projection. Keeping this target's worker occupied
// preserves FIFO while FleetDB is transiently unavailable.
func (d *Daemon) completeAgentCommand(
	cmd *domain.AgentCommand,
	update store.AgentCommandComplete,
	authority *agentCommandAuthority,
) bool {
	ownerID, ownerErr := d.sup.CommandOwnerID()
	if ownerErr != nil {
		slog.Error("durable lifecycle command owner unavailable; leaving command acknowledged",
			"command_id", cmd.CommandID,
			"node_id", d.sup.NodeID,
			"err", ownerErr)
		return false
	}
	update.NodeID = d.sup.NodeID
	update.OwnerID = ownerID
	for {
		update = agentCommandCompletionWithAuthority(update, authority)
		err := d.writeAgentCommandCompletion(cmd, update)
		if err == nil {
			return true
		}
		if agentCommandCompletionLostOwnership(cmd, err) {
			var reacquired bool
			authority, reacquired = d.reacquireAgentCommandCompletionOwnership(cmd, authority)
			if !reacquired {
				return false
			}
			continue
		}
		if !d.waitToRetryAgentCommandCompletion(cmd, err) {
			return false
		}
	}
}

// reassertAgentCommandLifecycle synchronizes daemon-local desired intent before
// the durable command becomes terminal. AgentCommand remains the durable
// accepted transition; this path never writes the legacy Agent runtime
// projection.
func (d *Daemon) reassertAgentCommandLifecycle(cmd *domain.AgentCommand, resp DaemonControlResponse) {
	desired, ok := d.agentCommandLifecycleProjection(cmd, resp)
	if !ok {
		return
	}
	d.markDaemonAgentIntentAccepted(cmd.TargetAgentID, desired)
}

func (d *Daemon) agentCommandLifecycleProjection(
	cmd *domain.AgentCommand,
	resp DaemonControlResponse,
) (domain.AgentDesiredState, bool) {
	if cmd == nil {
		return "", false
	}
	if resp.Success {
		switch cmd.Type {
		case "stop":
			return domain.AgentDesiredStopped, true
		case "start", "restart":
			return domain.AgentDesiredRunning, true
		default:
			return "", false
		}
	}
	switch cmd.Type {
	case "start", "stop", "restart", "yield":
		if d.isAgentRunning(cmd.TargetAgentID) {
			return domain.AgentDesiredRunning, true
		}
		return domain.AgentDesiredStopped, true
	default:
		return "", false
	}
}
