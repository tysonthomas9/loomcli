package subscription

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// agentStatePollInterval is how often the watcher checks daemon-agents.json
// for mtime changes. The daemon writes state every ~5s, so 3s is fast enough
// to catch changes within one write cycle while keeping poll overhead trivial.
// Declared as a var (not const) so tests can shrink it for fast iteration;
// production code should not mutate it at runtime.
var agentStatePollInterval = 3 * time.Second

// SetAgentStatePath configures the daemon-agents.json path that the agent
// state watcher polls. Safe to call before or after Start(); the watcher
// picks up the new path on its next tick. Pass "" to disable the watcher.
// Resets the stored mtime so the first observation on the new path always
// broadcasts — this both gives the frontend an immediate signal on
// workspace activation and prevents stale mtimes from the previous path
// from masking real changes on the new one.
func (s *DaemonSubscriber) SetAgentStatePath(path string) {
	s.agentStateMu.Lock()
	s.agentStatePath = path
	s.agentStateMtime = time.Time{}
	s.agentStateMu.Unlock()
}

// agentStateWatchLoop polls daemon-agents.json mtime and broadcasts an
// "agent_state_change" mutation whenever the file's mtime changes. This is
// a lightweight cache-invalidation signal for SSE clients — the payload
// contains only the workspace ID and timestamp, not per-agent data. The
// loop survives daemon restarts (file deletion + recreation) and never
// terminates on file-not-found.
func (s *DaemonSubscriber) agentStateWatchLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(agentStatePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
		}
		s.checkAgentStateOnce()
	}
}

// checkAgentStateOnce runs one iteration of the watcher: read the current
// path/mtime under lock, stat the file, and if the mtime has changed,
// commit it and broadcast — provided the path hasn't been swapped under us
// by a concurrent SetAgentStatePath.
func (s *DaemonSubscriber) checkAgentStateOnce() {
	s.agentStateMu.RLock()
	path := s.agentStatePath
	prev := s.agentStateMtime
	s.agentStateMu.RUnlock()
	if path == "" {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("agent state watcher: stat error",
				"workspace_id", s.workspaceID, "path", path, "err", err)
		}
		return
	}

	mtime := info.ModTime()
	if mtime.Equal(prev) {
		return
	}

	// Commit the new mtime only if the path hasn't changed under us. A
	// concurrent SetAgentStatePath could have swapped the path to a
	// different file between our RLock snapshot and the stat; writing mtime
	// from the old file onto the new path's state would cause missed or
	// spurious broadcasts on the next tick.
	s.agentStateMu.Lock()
	if s.agentStatePath != path {
		s.agentStateMu.Unlock()
		return
	}
	s.agentStateMtime = mtime
	s.agentStateMu.Unlock()

	s.hub.Broadcast(&realtime.MutationPayload{
		Type:        "agent_state_change",
		WorkspaceID: s.workspaceID,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
	})

	slog.Info("agent state change broadcast",
		"workspace_id", s.workspaceID,
		"old_mtime", prev.Format(time.RFC3339Nano),
		"new_mtime", mtime.Format(time.RFC3339Nano))
}
