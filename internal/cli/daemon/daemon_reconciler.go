package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// configReconciler polls FleetDB-backed daemon config and reconciles agents.
// It runs as a goroutine, started from Daemon.Start().
func (d *Daemon) configReconciler() {
	d.configPollingLoop()
}

// configPollingLoop polls for config changes every 30 seconds.
func (d *Daemon) configPollingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.sup.Shutdown:
			return
		case <-ticker.C:
			d.reloadAndReconcile()
			d.sup.RecordTick(supervisor.GoroutineConfigReconciler)
		}
	}
}

// reloadAndReconcile loads FleetDB config, checks for changes, and reconciles agents.
func (d *Daemon) reloadAndReconcile() {
	newConfig, err := config.LoadDaemonConfig(d.projectDir)
	if err != nil {
		slog.Warn("config reload failed, keeping current config", "err", err)
		d.emitConfigReloadError(err)
		return
	}

	newHash := computeConfigHash(newConfig)

	// Acquire reconcileMu for the diff+mutate phase to prevent overlapping reconciliations.
	d.reconcileMu.Lock()

	if newHash == d.configHash {
		d.reconcileMu.Unlock()
		return // no-op
	}

	oldAgents := d.config.Agents
	added, removed, modified := diffAgents(oldAgents, newConfig.Agents)

	if len(added) == 0 && len(removed) == 0 && len(modified) == 0 {
		d.config = newConfig
		d.configHash = newHash
		d.reconcileMu.Unlock()
		slog.Info("config reloaded, no agent changes")
		return
	}

	// In fleet mode, store updated config but skip local drain/add.
	if cli.IsFleetMode(newConfig) {
		d.config = newConfig
		d.configHash = newHash
		d.reconcileMu.Unlock()
		slog.Info("config reloaded in fleet mode (agent changes not applied locally)",
			"added", len(added), "removed", len(removed), "modified", len(modified))
		return
	}

	d.config = newConfig
	d.configHash = newHash
	d.reconcileMu.Unlock()

	slog.Info("config changed", "added", len(added), "removed", len(removed), "modified", len(modified))

	d.applyAgentChanges(added, removed, modified)

	d.emitConfigReloadSuccess(len(added), len(removed), len(modified))
}

// emitConfigReloadError emits a ConfigReloaded event with an error.
func (d *Daemon) emitConfigReloadError(err error) {
	if evt, evtErr := events.NewEvent(events.ConfigReloaded, "", "", "", events.ConfigReloadedData{
		Error: err.Error(),
	}); evtErr == nil {
		d.emitEvent(evt)
	}
}

// emitConfigReloadSuccess emits a ConfigReloaded event with agent change counts.
func (d *Daemon) emitConfigReloadSuccess(added, removed, modified int) {
	if evt, err := events.NewEvent(events.ConfigReloaded, "", "", "", events.ConfigReloadedData{
		Added:    added,
		Removed:  removed,
		Modified: modified,
	}); err == nil {
		d.emitEvent(evt)
	}
}

// applyAgentChanges drains removed/modified agents and adds new/modified agents.
func (d *Daemon) applyAgentChanges(added, removed, modified []config.AgentEntry) {
	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()

	modifiedToDrain := d.modifiedAgentsToDrain(modified)
	d.drainAgents(removed, "removed")
	d.drainAgents(modifiedToDrain, "modified")

	d.addNewAgents(added, "add")
	d.addNewAgents(modifiedToDrain, "re-add modified")
}

func (d *Daemon) modifiedAgentsToDrain(entries []config.AgentEntry) []config.AgentEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]config.AgentEntry, 0, len(entries))
	for _, entry := range entries {
		if d.hasDurableActiveEphemeralWork(entry) {
			slog.Info("deferring modified agent reconcile while ephemeral task is active", "worktree", entry.Worktree)
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (d *Daemon) hasDurableActiveEphemeralWork(entry config.AgentEntry) bool {
	if entry.Mode != domain.AgentModeEphemeral || d.store == nil || d.sup == nil || d.sup.WorkspaceID == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	active, err := d.hasLiveEphemeralStartCommand(ctx, entry.Worktree)
	if err != nil {
		slog.Warn("ephemeral command state check failed; deferring reconcile", "worktree", entry.Worktree, "err", err)
		return true
	}
	if active {
		return true
	}
	active, err = d.hasLiveEphemeralTaskSession(ctx, entry.Worktree)
	if err != nil {
		slog.Warn("ephemeral session state check failed; deferring reconcile", "worktree", entry.Worktree, "err", err)
		return true
	}
	if active {
		return true
	}
	agent, err := d.store.Agents().Get(ctx, d.sup.WorkspaceID, entry.Worktree)
	if errors.Is(err, domain.ErrNotFound) || agent == nil {
		return false
	}
	if err != nil {
		slog.Warn("ephemeral agent state check failed; deferring reconcile", "worktree", entry.Worktree, "err", err)
		return true
	}
	return agent.Mode == domain.AgentModeEphemeral && agent.State == domain.AgentStateActive
}

func (d *Daemon) hasLiveEphemeralStartCommand(ctx context.Context, agentName string) (bool, error) {
	if d.store.AgentCommands() == nil {
		return false, nil
	}
	cmds, err := d.store.AgentCommands().List(ctx, d.sup.WorkspaceID, store.AgentCommandFilter{
		TargetAgentID: agentName,
		Limit:         100,
	})
	if err != nil {
		return false, err
	}
	for _, cmd := range cmds {
		if cmd == nil || cmd.Type != "start" || cmd.Payload["task_id"] == "" {
			continue
		}
		if liveAgentCommandStatus(cmd.Status) {
			return true, nil
		}
	}
	return false, nil
}

func (d *Daemon) hasLiveEphemeralTaskSession(ctx context.Context, agentName string) (bool, error) {
	if d.store.AgentSessions() == nil {
		return false, nil
	}
	sessions, err := d.store.AgentSessions().List(ctx, d.sup.WorkspaceID, store.AgentSessionFilter{
		AgentID: agentName,
		Limit:   100,
	})
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session == nil || session.Kind != domain.AgentSessionKindTask || session.TaskID == "" {
			continue
		}
		if liveAgentSessionStatus(session.Status) {
			return true, nil
		}
	}
	return false, nil
}

func liveAgentCommandStatus(status domain.AgentCommandStatus) bool {
	switch status {
	case domain.AgentCommandQueued, domain.AgentCommandAcked, domain.AgentCommandRunning:
		return true
	default:
		return false
	}
}

func liveAgentSessionStatus(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionQueued, domain.AgentSessionLeased, domain.AgentSessionStarting, domain.AgentSessionRunning:
		return true
	default:
		return false
	}
}

// drainAgents stops a list of agents, logging errors.
func (d *Daemon) drainAgents(entries []config.AgentEntry, label string) {
	for _, entry := range entries {
		if err := d.sup.DrainAgent(entry.Worktree); err != nil {
			slog.Error("failed to drain "+label+" agent", "worktree", entry.Worktree, "err", err)
		}
	}
}

// addNewAgents starts agents, skipping those manually stopped.
func (d *Daemon) addNewAgents(entries []config.AgentEntry, label string) {
	for _, entry := range entries {
		if !entry.ShouldSupervise() {
			slog.Info("skipping "+label+" of agent with non-running desired state", "worktree", entry.Worktree, "desired_state", entry.DesiredState)
			continue
		}
		if d.isAgentStopped(entry.Worktree) {
			slog.Info("skipping "+label+" of manually stopped agent", "worktree", entry.Worktree)
			continue
		}
		if err := d.sup.AddAgent(entry); err != nil {
			slog.Error("failed to "+label+" agent", "worktree", entry.Worktree, "err", err)
		}
	}
}

// diffAgents compares old and new agent lists and categorizes changes.
// Agents are identified by their Worktree name.
func diffAgents(old, new []config.AgentEntry) (added, removed, modified []config.AgentEntry) {
	oldMap := make(map[string]config.AgentEntry, len(old))
	for _, e := range old {
		oldMap[e.Worktree] = e
	}
	newMap := make(map[string]config.AgentEntry, len(new))
	for _, e := range new {
		newMap[e.Worktree] = e
	}

	for name, newEntry := range newMap {
		if oldEntry, exists := oldMap[name]; !exists {
			added = append(added, newEntry)
		} else if !oldEntry.Equal(newEntry) {
			modified = append(modified, newEntry)
		}
	}
	for name, oldEntry := range oldMap {
		if _, exists := newMap[name]; !exists {
			removed = append(removed, oldEntry)
		}
	}
	return
}

// computeConfigHash returns a SHA-256 hex digest of the serialized config.DaemonConfig.
func computeConfigHash(dc *config.DaemonConfig) string {
	data, err := json.Marshal(dc)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
