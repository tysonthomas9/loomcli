package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/tysonthomas9/loomcli/internal/events"
)

// configReconciler watches config files for changes and reconciles agents.
// It runs as a goroutine, started from Daemon.Start().
func (d *Daemon) configReconciler() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[daemon] Failed to create config watcher, falling back to polling: %v", err)
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
			log.Printf("[daemon] Config file change detected: %s (%s)", event.Name, event.Op)

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
			log.Printf("[daemon] Config watcher error: %v", err)
		}
	}
}

// watchConfigDirs adds the project and global config directories to the watcher.
// Watches directories (not files) because editors like vim delete and recreate files.
func (d *Daemon) watchConfigDirs(watcher *fsnotify.Watcher) {
	projectConfigDir := d.projectDir
	globalConfigDir := GetConfigDir()

	if err := watcher.Add(projectConfigDir); err != nil {
		log.Printf("[daemon] Failed to watch project dir %s: %v", projectConfigDir, err)
	}
	if globalConfigDir != "" && globalConfigDir != projectConfigDir {
		if err := watcher.Add(globalConfigDir); err != nil {
			log.Printf("[daemon] Failed to watch global config dir %s: %v", globalConfigDir, err)
		}
	}

	log.Printf("[daemon] Config watcher started (project: %s, global: %s)", projectConfigDir, globalConfigDir)
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
	newConfig, err := LoadDaemonConfig(d.projectDir)
	if err != nil {
		log.Printf("[daemon] Config reload failed (keeping current config): %v", err)
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
		log.Printf("[daemon] Config reloaded (no agent changes)")
		return
	}

	// Update stored config and hash before drain/add so that new agents
	// spawned by addAgent see the updated config.
	d.config = newConfig
	d.configHash = newHash

	// Release the lock before drain/add — these can block for a long time
	// (drainAgent waits for superviseAgent goroutine to exit).
	d.reconcileMu.Unlock()

	log.Printf("[daemon] Config changed: %d added, %d removed, %d modified agents",
		len(added), len(removed), len(modified))

	// Drain removed and modified agents
	for _, entry := range removed {
		if err := d.drainAgent(entry.Worktree); err != nil {
			log.Printf("[daemon] Failed to drain removed agent %s: %v", entry.Worktree, err)
		}
	}
	for _, entry := range modified {
		if err := d.drainAgent(entry.Worktree); err != nil {
			log.Printf("[daemon] Failed to drain modified agent %s: %v", entry.Worktree, err)
		}
	}

	// Add new and modified agents
	for _, entry := range added {
		if err := d.addAgent(entry); err != nil {
			log.Printf("[daemon] Failed to add agent %s: %v", entry.Worktree, err)
		}
	}
	for _, entry := range modified {
		if err := d.addAgent(entry); err != nil {
			log.Printf("[daemon] Failed to re-add modified agent %s: %v", entry.Worktree, err)
		}
	}

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
func diffAgents(old, new []AgentEntry) (added, removed, modified []AgentEntry) {
	oldMap := make(map[string]AgentEntry, len(old))
	for _, e := range old {
		oldMap[e.Worktree] = e
	}
	newMap := make(map[string]AgentEntry, len(new))
	for _, e := range new {
		newMap[e.Worktree] = e
	}

	for name, newEntry := range newMap {
		if oldEntry, exists := oldMap[name]; !exists {
			added = append(added, newEntry)
		} else if !reflect.DeepEqual(oldEntry, newEntry) {
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

// computeConfigHash returns a SHA-256 hex digest of the serialized DaemonConfig.
func computeConfigHash(dc *DaemonConfig) string {
	data, err := json.Marshal(dc)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
