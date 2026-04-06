package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// configReconciler watches config files for changes and reconciles agents.
// It runs as a goroutine, started from Daemon.Start().
func (d *Daemon) configReconciler() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("failed to create config watcher, falling back to polling", "err", err)
		d.configPollingFallback()
		return
	}
	defer func() { _ = watcher.Close() }()

	d.watchConfigDirs(watcher)

	var debounceTimer *time.Timer
	var debounceMu sync.Mutex
	var stopped atomic.Bool

	for {
		select {
		case <-d.shutdown:
			stopped.Store(true)
			debounceMu.Lock()
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceMu.Unlock()
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !isConfigFileEvent(event) {
				continue
			}
			slog.Info("config file change detected", "file", event.Name, "op", event.Op)

			// Debounce: reset 500ms timer on each event
			debounceMu.Lock()
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
				if !stopped.Load() {
					d.reloadAndReconcile()
				}
			})
			debounceMu.Unlock()

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("config watcher error", "err", err)
		}
	}
}

// watchConfigDirs adds the project and global config directories to the watcher.
// Watches directories (not files) because editors like vim delete and recreate files.
func (d *Daemon) watchConfigDirs(watcher *fsnotify.Watcher) {
	projectConfigDir := d.projectDir
	globalConfigDir := config.GetConfigDir()

	if err := watcher.Add(projectConfigDir); err != nil {
		slog.Warn("failed to watch project dir", "path", projectConfigDir, "err", err)
	}
	if globalConfigDir != "" && globalConfigDir != projectConfigDir {
		if err := watcher.Add(globalConfigDir); err != nil {
			slog.Warn("failed to watch global config dir", "path", globalConfigDir, "err", err)
		}
	}

	slog.Info("config watcher started", "project_dir", projectConfigDir, "global_dir", globalConfigDir)
}

// isConfigFileEvent returns true if the fsnotify event is for a config file we care about.
func isConfigFileEvent(event fsnotify.Event) bool {
	base := filepath.Base(event.Name)
	if base != "loom.yaml" && base != "config.yaml" {
		return false
	}
	return event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0
}

// configPollingFallback polls for config changes every 30 seconds.
// Used when fsnotify is unavailable (e.g., NFS, containers).
func (d *Daemon) configPollingFallback() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.shutdown:
			return
		case <-ticker.C:
			d.reloadAndReconcile()
		}
	}
}

// reloadAndReconcile loads the config, checks for changes, and reconciles agents.
// Config loading (I/O) happens outside the lock; only the diff+mutate phase is serialized.
func (d *Daemon) reloadAndReconcile() {
	// Load config outside the lock — this does file I/O, env expansion, secret resolution.
	newConfig, err := config.LoadDaemonConfig(d.projectDir)
	if err != nil {
		slog.Warn("config reload failed, keeping current config", "err", err)
		if evt, err := events.NewEvent(events.ConfigReloaded, "", "", "", events.ConfigReloadedData{
			Error: err.Error(),
		}); err == nil {
			d.emitEvent(evt)
		}
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
		// Config changed but no agent differences (e.g., daemon settings only)
		d.config = newConfig
		d.configHash = newHash
		d.reconcileMu.Unlock()
		slog.Info("config reloaded, no agent changes")
		return
	}

	// In fleet mode, agent lifecycle is managed by the fleet server.
	// Store the updated config but do not drain/add agents locally —
	// their stopCh/done channels were never initialized.
	if cli.IsFleetMode(newConfig) {
		d.config = newConfig
		d.configHash = newHash
		d.reconcileMu.Unlock()
		slog.Info("config reloaded in fleet mode (agent changes not applied locally)",
			"added", len(added), "removed", len(removed), "modified", len(modified))
		return
	}

	// Update stored config and hash before drain/add so that new agents
	// spawned by addAgent see the updated config.
	d.config = newConfig
	d.configHash = newHash

	// Release reconcileMu before drain/add — drainAgent blocks on <-target.done,
	// and superviseAgent calls configSnapshot() which acquires reconcileMu.RLock.
	// Holding reconcileMu.Lock through drain would deadlock.
	d.reconcileMu.Unlock()

	slog.Info("config changed", "added", len(added), "removed", len(removed), "modified", len(modified))

	// Serialize the drain/add phase to prevent concurrent reconciliations from
	// racing on the same agent (e.g., double SIGTERM/SIGKILL, duplicate adds).
	d.drainAddMu.Lock()

	// Drain removed and modified agents
	for _, entry := range removed {
		if err := d.drainAgent(entry.Worktree); err != nil {
			slog.Error("failed to drain removed agent", "worktree", entry.Worktree, "err", err)
		}
	}
	for _, entry := range modified {
		if err := d.drainAgent(entry.Worktree); err != nil {
			slog.Error("failed to drain modified agent", "worktree", entry.Worktree, "err", err)
		}
	}

	// Add new and modified agents.
	// Agents stopped via control socket are excluded from re-addition until
	// explicitly started via "loom daemon start <agent>".
	for _, entry := range added {
		if d.isAgentStopped(entry.Worktree) {
			slog.Info("skipping re-add of manually stopped agent", "worktree", entry.Worktree)
			continue
		}
		if err := d.addAgent(entry); err != nil {
			slog.Error("failed to add agent", "worktree", entry.Worktree, "err", err)
		}
	}
	for _, entry := range modified {
		if d.isAgentStopped(entry.Worktree) {
			slog.Info("skipping re-add of manually stopped modified agent", "worktree", entry.Worktree)
			continue
		}
		if err := d.addAgent(entry); err != nil {
			slog.Error("failed to re-add modified agent", "worktree", entry.Worktree, "err", err)
		}
	}

	d.drainAddMu.Unlock()

	if evt, err := events.NewEvent(events.ConfigReloaded, "", "", "", events.ConfigReloadedData{
		Added:    len(added),
		Removed:  len(removed),
		Modified: len(modified),
	}); err == nil {
		d.emitEvent(evt)
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
