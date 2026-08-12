package supervisor

import "time"

// Agent-IPC activity sinks, keyed by the agent's IPC identity (the worktree
// name). Tested in activity_test.go.

// findAgentByWorktree returns the supervised agent with the given worktree name,
// or nil when none matches. Shared by the agent-IPC sinks (RecordAgentActivity,
// RecordAgentInputWait), which are both keyed by the agent's IPC identity.
func (s *Supervisor) findAgentByWorktree(agentName string) *AgentProcess {
	s.AgentsMu.RLock()
	defer s.AgentsMu.RUnlock()
	for _, ap := range s.Agents {
		if ap.Entry.Worktree == agentName {
			return ap
		}
	}
	return nil
}

// RecordAgentActivity advances ap.LastActivity for the named agent toward the
// observed PTY-output timestamp. It is a no-op if the agent isn't currently
// supervised. Out-of-order heartbeats never regress the stored value — callers
// can safely retry without ever rewinding the timestamp.
func (s *Supervisor) RecordAgentActivity(agentName string, at time.Time) {
	if agentName == "" || at.IsZero() {
		return
	}
	target := s.findAgentByWorktree(agentName)
	if target == nil {
		return
	}
	target.Mu.Lock()
	if at.After(target.LastActivity) {
		target.LastActivity = at
	}
	target.Mu.Unlock()
}
