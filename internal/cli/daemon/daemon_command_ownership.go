package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// agentCommandAuthority is deliberately process-local. Its proof contains the
// raw ownership bearer token, so it must never be logged, serialized, or
// persisted. Durable AgentCommand rows carry only lease ID + fencing token.
type agentCommandAuthority struct {
	proof            supervisor.AgentCommandOwnershipProof
	transientRelease func()
	releases         []func()
}

func (a *agentCommandAuthority) addRelease(release func()) {
	if release != nil {
		a.releases = append(a.releases, release)
	}
}

func (a *agentCommandAuthority) setReacquiredProof(
	proof supervisor.AgentCommandOwnershipProof,
	transientRelease func(),
) {
	if a.transientRelease != nil {
		a.transientRelease()
	}
	a.proof = proof
	a.transientRelease = transientRelease
}

func (a *agentCommandAuthority) releaseTransientBeforeReplacement() {
	if a.transientRelease == nil {
		return
	}
	a.transientRelease()
	a.transientRelease = nil
}

func (a *agentCommandAuthority) release() {
	for i := len(a.releases) - 1; i >= 0; i-- {
		a.releases[i]()
	}
	a.releases = nil
	if a.transientRelease != nil {
		a.transientRelease()
		a.transientRelease = nil
	}
}

func (a *agentCommandAuthority) storeProof() store.AgentCommandOwnershipProof {
	return store.AgentCommandOwnershipProof{
		OwnershipLeaseID:      a.proof.LeaseID,
		OwnershipToken:        a.proof.Token,
		OwnershipFencingToken: a.proof.FencingToken,
	}
}

type agentCommandOwnershipHolder struct {
	ownerID string
	nodeID  string
}

type agentCommandOwnershipSnapshot map[string]agentCommandOwnershipHolder

func (d *Daemon) agentCommandOwnershipSnapshot(
	ctx context.Context,
) (agentCommandOwnershipSnapshot, error) {
	if d == nil || d.store == nil || d.store.AgentOwnershipLeases() == nil {
		return nil, errors.New("agent ownership lease store is unavailable")
	}
	leases, err := d.store.AgentOwnershipLeases().List(
		ctx,
		d.sup.WorkspaceID,
		store.AgentOwnershipLeaseFilter{Status: domain.AgentLeaseActive},
	)
	if err != nil {
		return nil, err
	}
	snapshot := make(agentCommandOwnershipSnapshot)
	for _, lease := range leases {
		if lease == nil ||
			lease.AgentID == "" ||
			lease.Status != domain.AgentLeaseActive {
			continue
		}
		snapshot[lease.AgentID] = agentCommandOwnershipHolder{
			ownerID: lease.OwnerID,
			nodeID:  lease.NodeID,
		}
	}
	return snapshot, nil
}

func (d *Daemon) agentCommandBelongsToThisDaemon(cmd *domain.AgentCommand) bool {
	return d.agentCommandBelongsToThisDaemonWithOwnership(cmd, nil)
}

func (d *Daemon) agentCommandBelongsToThisDaemonWithOwnership(
	cmd *domain.AgentCommand,
	ownership agentCommandOwnershipSnapshot,
) bool {
	if cmd == nil || d == nil || d.sup == nil || d.sup.NodeID == "" {
		return false
	}
	switch cmd.Status {
	case domain.AgentCommandAcked, domain.AgentCommandRunning:
		ownerID, err := d.sup.CommandOwnerID()
		if err != nil {
			return false
		}
		return ownerID != "" && cmd.AckedBy == ownerID
	default:
		if cmd.TargetNodeID != "" && cmd.TargetNodeID != d.sup.NodeID {
			return false
		}
		proof, err := d.sup.CurrentAgentCommandOwnershipProof(cmd.TargetAgentID)
		if err == nil {
			if proof.NodeID != d.sup.NodeID {
				return false
			}
			ownerID, ownerErr := d.sup.CommandOwnerID()
			return ownerErr == nil && proof.OwnerID == ownerID
		}
		ownerID, err := d.sup.CommandOwnerID()
		if err != nil {
			return false
		}
		if holder, live := ownership[cmd.TargetAgentID]; live {
			// A queued command behind another stable runtime's live aggregate
			// ownership is excluded before the global 50-row cap. The same
			// stable owner may reacquire a raw proof after process replacement.
			return holder.ownerID == ownerID
		}
		if !knownAgentLifecycleCommandType(cmd.Type) {
			return false
		}
		cfg := d.configSnapshot()
		if cfg == nil {
			return false
		}
		for _, entry := range cfg.Agents {
			if entry.Worktree == cmd.TargetAgentID {
				return true
			}
		}
		return false
	}
}

func (d *Daemon) agentCommandAckedByThisProcess(
	cmd *domain.AgentCommand,
	authority *agentCommandAuthority,
) bool {
	if !d.agentCommandClaimedByThisProcess(cmd) {
		return false
	}
	if authority == nil || authority.proof.LeaseID == "" {
		return cmd.OwnershipLeaseID == "" && cmd.OwnershipFencingToken == 0
	}
	if authority.proof.OwnerID != cmd.AckedBy ||
		authority.proof.NodeID != cmd.TargetNodeID ||
		authority.proof.AgentID != cmd.TargetAgentID {
		return false
	}
	if cmd.OwnershipFencingToken == 0 {
		return true
	}
	if authority.proof.FencingToken > cmd.OwnershipFencingToken {
		return true
	}
	return cmd.OwnershipLeaseID == authority.proof.LeaseID &&
		cmd.OwnershipFencingToken == authority.proof.FencingToken
}

func (d *Daemon) agentCommandClaimedByThisProcess(cmd *domain.AgentCommand) bool {
	if d == nil || d.sup == nil || cmd == nil {
		return false
	}
	ownerID, err := d.sup.CommandOwnerID()
	if err != nil ||
		cmd.TargetNodeID != d.sup.NodeID ||
		cmd.AckedBy == "" ||
		cmd.AckedBy != ownerID {
		return false
	}
	return true
}

func (d *Daemon) alignAgentCommandAuthority(
	cmd *domain.AgentCommand,
	authority *agentCommandAuthority,
) bool {
	if d.agentCommandAckedByThisProcess(cmd, authority) {
		return true
	}
	if !d.agentCommandClaimedByThisProcess(cmd) ||
		cmd.TargetAgentID == "" ||
		authority == nil {
		return false
	}
	proof, release, err := d.sup.ReacquireAgentCommandOwnership(cmd.TargetAgentID)
	if err != nil {
		slog.Warn("agent command claimant could not align current ownership generation",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID,
			"err", err)
		return false
	}
	authority.setReacquiredProof(proof, release)
	return d.agentCommandAckedByThisProcess(cmd, authority)
}

func (d *Daemon) settleAgentCommandExecutionOwnership(
	cmd *domain.AgentCommand,
	recovering bool,
	replacement *supervisor.AgentProcess,
	resp *DaemonControlResponse,
	recoveryErrorClass *string,
	authority *agentCommandAuthority,
) bool {
	if replacement != nil {
		if !d.settleReplacementAgentCommandOwnership(
			cmd,
			replacement,
			resp,
			recoveryErrorClass,
			authority,
		) {
			return false
		}
	} else if cmd.Type == "restart" && !recovering && !resp.Success {
		d.reacquireFailedRestartAgentCommandOwnership(cmd, authority)
	}
	return d.ensureStartedAgentCommandOwnership(cmd, *resp, authority)
}

func (d *Daemon) settleReplacementAgentCommandOwnership(
	cmd *domain.AgentCommand,
	replacement *supervisor.AgentProcess,
	resp *DaemonControlResponse,
	recoveryErrorClass *string,
	authority *agentCommandAuthority,
) bool {
	waitCtx, waitCancel := context.WithTimeout(
		context.Background(),
		agentCommandRestartOwnershipTimeout,
	)
	replacementProof, err := d.sup.WaitForAgentCommandOwnership(
		waitCtx,
		replacement,
		authority.proof.LifecycleGenerationAt,
		authority.proof.FencingToken,
	)
	waitCancel()
	authority.addRelease(func() {
		d.sup.ReleaseAgentCommandOwnershipReservation(replacement)
	})
	if err == nil {
		authority.proof = replacementProof
		return true
	}

	*resp = DaemonControlResponse{
		Error: "replacement agent did not reacquire lifecycle authority",
	}
	*recoveryErrorClass = "restart_ownership_reacquire_failed"
	slog.Warn("restart replacement ownership unavailable",
		"command_id", cmd.CommandID,
		"target_agent_id", cmd.TargetAgentID,
		"err", err)
	proof, release, reacquireErr := d.sup.ReacquireAgentCommandOwnership(cmd.TargetAgentID)
	if reacquireErr == nil {
		authority.setReacquiredProof(proof, release)
		return true
	}
	// No fenced terminal write is possible. Leave the durable row acknowledged
	// so the next poll/replacement daemon can recover; do not strand this FIFO
	// worker in an infinite Complete retry.
	slog.Warn("agent command terminal ownership unavailable; leaving acknowledged",
		"command_id", cmd.CommandID,
		"target_agent_id", cmd.TargetAgentID,
		"err", reacquireErr)
	return false
}

func (d *Daemon) reacquireFailedRestartAgentCommandOwnership(
	cmd *domain.AgentCommand,
	authority *agentCommandAuthority,
) {
	// Restart may have drained the old generation before AddAgent failed.
	// Reacquire as the same stable owner so even the failed terminal write
	// remains owner-fenced; never complete with a released/stale guess.
	proof, release, err := d.sup.ReacquireAgentCommandOwnership(cmd.TargetAgentID)
	if err == nil {
		authority.setReacquiredProof(proof, release)
	}
}

func (d *Daemon) ensureStartedAgentCommandOwnership(
	cmd *domain.AgentCommand,
	resp DaemonControlResponse,
	authority *agentCommandAuthority,
) bool {
	if cmd.Type != "start" || !resp.Success || authority.proof.LeaseID != "" {
		return true
	}
	proof, release, err := d.sup.ReacquireAgentCommandOwnership(cmd.TargetAgentID)
	if err != nil {
		slog.Warn("started agent has no terminal ownership proof; leaving command acknowledged",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID,
			"err", err)
		return false
	}
	authority.setReacquiredProof(proof, release)
	return true
}

func (d *Daemon) prepareRecoveringAgentCommand(
	cmd *domain.AgentCommand,
	authority *agentCommandAuthority,
) (*domain.AgentCommand, bool, bool) {
	if !d.agentCommandBelongsToThisDaemon(cmd) {
		slog.Info("ignoring acknowledged agent command owned by another daemon",
			"command_id", cmd.CommandID,
			"target_node_id", cmd.TargetNodeID,
			"acked_by", cmd.AckedBy,
			"node_id", d.sup.NodeID)
		return nil, false, true
	}
	if cmd.OwnershipLeaseID != "" || cmd.OwnershipFencingToken > 0 {
		proof, release, err := d.sup.ReacquireAgentCommandOwnership(cmd.TargetAgentID)
		if err != nil {
			slog.Warn("acknowledged agent command ownership recovery failed",
				"command_id", cmd.CommandID,
				"target_agent_id", cmd.TargetAgentID,
				"err", err)
			return nil, false, false
		}
		authority.setReacquiredProof(proof, release)
	}
	slog.Info("resuming acknowledged agent command after daemon interruption",
		"command_id", cmd.CommandID,
		"type", cmd.Type,
		"target_agent_id", cmd.TargetAgentID)
	return cmd, true, false
}

func (d *Daemon) prepareQueuedAgentCommandAuthority(
	ctx context.Context,
	cmd *domain.AgentCommand,
	ownerID string,
	authority *agentCommandAuthority,
) bool {
	proof, proofErr := d.sup.CurrentAgentCommandOwnershipProof(cmd.TargetAgentID)
	if proofErr != nil {
		return d.prepareQueuedAgentCommandWithoutProof(
			ctx,
			cmd,
			ownerID,
			proofErr,
			authority,
		)
	}
	if proof.OwnerID != ownerID || proof.NodeID != d.sup.NodeID {
		slog.Warn("agent command local ownership proof does not belong to this daemon",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID,
			"proof_owner_id", proof.OwnerID,
			"proof_node_id", proof.NodeID,
			"node_id", d.sup.NodeID)
		return false
	}
	authority.proof = proof
	return true
}

func (d *Daemon) prepareQueuedAgentCommandWithoutProof(
	ctx context.Context,
	cmd *domain.AgentCommand,
	ownerID string,
	proofErr error,
	authority *agentCommandAuthority,
) bool {
	if !knownAgentLifecycleCommandType(cmd.Type) {
		slog.Warn("agent command has no active local ownership proof; refusing acknowledgement",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID,
			"err", proofErr)
		return false
	}
	ownership, err := d.agentCommandOwnershipSnapshot(ctx)
	if err != nil {
		slog.Warn("agent command ownership snapshot failed before acknowledgement",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID,
			"err", err)
		return false
	}
	holder, live := ownership[cmd.TargetAgentID]
	if !live {
		return true
	}
	if holder.ownerID != ownerID {
		slog.Info("agent command target is owned by another stable runtime",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID)
		// The row is still queued and therefore still the durable FIFO head.
		// Keep it at the local head rather than advancing a successor into a
		// permanent ordering inversion.
		return false
	}
	proof, release, err := d.sup.ReacquireAgentCommandOwnership(cmd.TargetAgentID)
	if err != nil {
		slog.Warn("agent command same-owner recovery failed before acknowledgement",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID,
			"err", err)
		return false
	}
	authority.setReacquiredProof(proof, release)
	return true
}

func agentCommandCompletionWithAuthority(
	update store.AgentCommandComplete,
	authority *agentCommandAuthority,
) store.AgentCommandComplete {
	if authority == nil || authority.proof.LeaseID == "" {
		return update
	}
	update.NodeID = authority.proof.NodeID
	update.OwnerID = authority.proof.OwnerID
	update.AgentCommandOwnershipProof = authority.storeProof()
	return update
}

func (d *Daemon) writeAgentCommandCompletion(
	cmd *domain.AgentCommand,
	update store.AgentCommandComplete,
) error {
	completeCtx, completeCancel := context.WithTimeout(
		context.Background(),
		agentCommandPollTimeout,
	)
	defer completeCancel()
	_, err := d.store.AgentCommands().Complete(
		completeCtx,
		cmd.WorkspaceKey,
		cmd.CommandID,
		update,
	)
	return err
}

func agentCommandCompletionLostOwnership(cmd *domain.AgentCommand, err error) bool {
	return cmd.TargetAgentID != "" &&
		(errors.Is(err, domain.ErrConflict) ||
			errors.Is(err, domain.ErrNotOwner) ||
			errors.Is(err, domain.ErrGone))
}

func (d *Daemon) reacquireAgentCommandCompletionOwnership(
	cmd *domain.AgentCommand,
	authority *agentCommandAuthority,
) (*agentCommandAuthority, bool) {
	proof, release, err := d.sup.ReacquireAgentCommandOwnership(cmd.TargetAgentID)
	if err != nil {
		slog.Warn("agent command completion lost ownership; leaving acknowledged",
			"command_id", cmd.CommandID,
			"target_agent_id", cmd.TargetAgentID,
			"err", err)
		return authority, false
	}
	if authority == nil {
		authority = &agentCommandAuthority{}
	}
	authority.setReacquiredProof(proof, release)
	return authority, true
}

func (d *Daemon) waitToRetryAgentCommandCompletion(
	cmd *domain.AgentCommand,
	err error,
) bool {
	slog.Warn("agent command completion failed; retrying",
		"command_id", cmd.CommandID,
		"err", err)
	timer := time.NewTimer(agentCommandRetryDelay)
	select {
	case <-d.sup.Shutdown:
		timer.Stop()
		slog.Warn("agent command completion abandoned during shutdown",
			"command_id", cmd.CommandID)
		return false
	case <-timer.C:
		return true
	}
}
