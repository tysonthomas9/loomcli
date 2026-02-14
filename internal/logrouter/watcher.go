package logrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// AgentLockInfo represents the structure of the .agent.lock file.
type AgentLockInfo struct {
	PID           int    `json:"pid"`
	Command       string `json:"command"`
	AgentName     string `json:"agent_name"`
	TaskID        string `json:"task_id"`
	TaskTitle     string `json:"task_title"`
	State         string `json:"state"`
}

// LockWatcher watches for changes to the agent lock file and updates the router.
type LockWatcher struct {
	lockPath string
	router   *LogRouter
	watcher  *fsnotify.Watcher
}

// NewLockWatcher creates a new LockWatcher for the specified lock file.
func NewLockWatcher(lockPath string, router *LogRouter) (*LockWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	lw := &LockWatcher{
		lockPath: lockPath,
		router:   router,
		watcher:  watcher,
	}

	// Watch the directory containing the lock file (to catch create events)
	lockDir := filepath.Dir(lockPath)
	if err := watcher.Add(lockDir); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to watch lock directory: %w", err)
	}

	// Do initial read if lock file exists
	lw.readAndUpdateLock()

	return lw, nil
}

// Watch starts watching for lock file changes. Blocks until context is cancelled.
func (lw *LockWatcher) Watch(ctx context.Context) {
	lockFileName := filepath.Base(lw.lockPath)

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-lw.watcher.Events:
			if !ok {
				return
			}
			// Only process events for our lock file
			if filepath.Base(event.Name) != lockFileName {
				continue
			}

			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				lw.readAndUpdateLock()
			} else if event.Op&fsnotify.Remove != 0 {
				// Lock file removed - clear task
				if err := lw.router.ClearTask(); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to clear task: %v\n", err)
				}
			}
		case err, ok := <-lw.watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)
		}
	}
}

// readAndUpdateLock reads the lock file and updates the router.
func (lw *LockWatcher) readAndUpdateLock() {
	lockInfo, err := lw.readLockFile()
	if err != nil {
		// Lock file might not exist yet or be temporarily unavailable
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: failed to read lock file: %v\n", err)
		}
		return
	}

	// Determine phase from command
	phase := commandToPhase(lockInfo.Command)

	// Update router with task info
	if lockInfo.TaskID != "" {
		if err := lw.router.SetTask(lockInfo.TaskID, phase); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to set task: %v\n", err)
		}
	} else {
		if err := lw.router.ClearTask(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to clear task: %v\n", err)
		}
	}
}

// readLockFile reads and parses the agent lock file.
func (lw *LockWatcher) readLockFile() (*AgentLockInfo, error) {
	// #nosec G304 - controlled path from CLI flags
	data, err := os.ReadFile(lw.lockPath)
	if err != nil {
		return nil, err
	}

	var lockInfo AgentLockInfo
	if err := json.Unmarshal(data, &lockInfo); err != nil {
		return nil, fmt.Errorf("failed to parse lock file JSON: %w", err)
	}

	return &lockInfo, nil
}

// commandToPhase converts the command field to a phase name.
func commandToPhase(command string) string {
	switch command {
	case "plan":
		return "planning"
	case "task":
		return "implementation"
	default:
		// Default to implementation for unknown commands
		return "implementation"
	}
}

// Close closes the file watcher.
func (lw *LockWatcher) Close() error {
	return lw.watcher.Close()
}
