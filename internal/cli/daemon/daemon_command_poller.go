package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var agentCommandPollTimeout = 5 * time.Second

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
	ctx, cancel := context.WithTimeout(context.Background(), agentCommandPollTimeout)
	defer cancel()
	cmds, err := d.store.AgentCommands().List(ctx, d.sup.WorkspaceID, store.AgentCommandFilter{
		Status: domain.AgentCommandQueued,
		Limit:  50,
	})
	if err != nil {
		slog.Warn("agent command poll failed", "err", err)
		return
	}
	for _, cmd := range cmds {
		if cmd.TargetNodeID != "" && cmd.TargetNodeID != d.sup.NodeID {
			continue
		}
		d.handleAgentCommand(cmd)
	}
}

func (d *Daemon) handleAgentCommand(cmd *domain.AgentCommand) {
	ctx, cancel := context.WithTimeout(context.Background(), agentCommandPollTimeout)
	defer cancel()

	if _, err := d.store.AgentCommands().Ack(ctx, cmd.WorkspaceKey, cmd.CommandID); err != nil {
		slog.Warn("agent command ack failed", "command_id", cmd.CommandID, "err", err)
		return
	}
	var resp DaemonControlResponse
	switch cmd.Type {
	case "start":
		resp = d.handleAgentControlStart(cmd.TargetAgentID, cmd.Payload["task_id"])
	case "stop":
		resp = d.handleAgentControlStop(cmd.TargetAgentID, cmd.Payload["force"] == "true")
	case "restart":
		resp = d.handleAgentControlRestart(cmd.TargetAgentID)
	case "yield":
		resp = d.handleAgentControlYield(cmd.TargetAgentID)
	default:
		resp = DaemonControlResponse{Error: fmt.Sprintf("unsupported agent command type %q", cmd.Type)}
	}
	status := domain.AgentCommandSucceeded
	result := "ok"
	errClass := ""
	if !resp.Success {
		status = domain.AgentCommandFailed
		result = resp.Error
		errClass = "control_error"
	}
	if _, err := d.store.AgentCommands().Complete(ctx, cmd.WorkspaceKey, cmd.CommandID, store.AgentCommandComplete{
		Status:     status,
		Result:     result,
		ErrorClass: errClass,
	}); err != nil {
		slog.Warn("agent command completion failed", "command_id", cmd.CommandID, "err", err)
	} else {
		slog.Info("agent command completed", "command_id", cmd.CommandID, "type", cmd.Type, "target_agent_id", cmd.TargetAgentID, "status", status, "result", result)
	}
}
