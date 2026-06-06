package supervisor

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

type maxRetriesExhausted struct {
	AgentName    string
	Role         string
	TaskID       string
	Backend      string
	MaxRetries   int
	RestartCount int
	ErrorClass   string
	ErrorMessage string
}

// newMaxRetriesExhaustedLocked snapshots the signals needed after the restart
// decision releases ap.Mu. Caller holds ap.Mu.
func newMaxRetriesExhaustedLocked(ap *AgentProcess, maxRetries int) *maxRetriesExhausted {
	info := &maxRetriesExhausted{
		AgentName:    ap.Entry.Worktree,
		Role:         ap.Entry.Role,
		TaskID:       ap.AssignedTaskID,
		MaxRetries:   maxRetries,
		RestartCount: ap.RestartCount,
		ErrorClass:   "unknown",
	}
	if ap.LastError != nil {
		info.ErrorClass = ap.LastError.Class.String()
		info.ErrorMessage = strings.TrimSpace(ap.LastError.Message)
	}
	if info.TaskID == "" {
		info.TaskID = strings.TrimSpace(ap.RequestedTaskID)
	}
	return info
}

func (s *Supervisor) handleMaxRetriesExhausted(ap *AgentProcess, info maxRetriesExhausted) {
	slog.Warn("agent restart budget exhausted; entering error state",
		"worktree", info.AgentName,
		"max_retries", info.MaxRetries,
		"restart_count", info.RestartCount)
	s.markControlPlaneAgentState(ap, domain.AgentStateError)
	s.markAgentStoppedForExplicitResume(info.AgentName)
	if info.TaskID == "" || s.IssueBackend == nil {
		return
	}
	s.blockTaskAfterMaxRetries(info)
}

func (s *Supervisor) markAgentStoppedForExplicitResume(agentName string) {
	if agentName == "" {
		return
	}
	s.AgentsMu.Lock()
	if s.StoppedAgents == nil {
		s.StoppedAgents = make(map[string]struct{})
	}
	s.StoppedAgents[agentName] = struct{}{}
	s.AgentsMu.Unlock()
}

func (s *Supervisor) blockTaskAfterMaxRetries(info maxRetriesExhausted) {
	status := "blocked"
	assignee := info.AgentName

	ctx, cancel := s.operationContext()
	defer cancel()
	if err := s.IssueBackend.Update(ctx, info.TaskID, backend.UpdateParams{
		Status:   &status,
		Assignee: &assignee,
	}); err != nil {
		slog.Warn("failed to block task after agent retry budget exhausted",
			"worktree", info.AgentName, "task_id", info.TaskID, "err", err)
		return
	}

	if _, err := s.IssueBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: info.TaskID,
		Author:  "loom-daemon",
		Text:    maxRetriesExhaustedComment(info),
	}); err != nil {
		slog.Warn("failed to comment after agent retry budget exhausted",
			"worktree", info.AgentName, "task_id", info.TaskID, "err", err)
	}
}

func maxRetriesExhaustedComment(info maxRetriesExhausted) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agent %s stopped with error after exhausting its retry budget (%d failed attempt(s), max_retries=%d). Automatic retries are stopped; start or restart the agent to resume.",
		info.AgentName, info.RestartCount, info.MaxRetries)
	if info.Backend != "" {
		fmt.Fprintf(&b, " Backend: %s.", info.Backend)
	}
	if info.ErrorClass != "" {
		fmt.Fprintf(&b, " Last error: %s", info.ErrorClass)
		if info.ErrorMessage != "" {
			fmt.Fprintf(&b, ": %s", info.ErrorMessage)
		}
		b.WriteString(".")
	}
	return b.String()
}

func isReplaceableTerminalAgent(ap *AgentProcess) bool {
	done, ok := pendingTerminalAgentDone(ap)
	if !ok {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func pendingTerminalAgentDone(ap *AgentProcess) (<-chan struct{}, bool) {
	if ap == nil || ap.Done == nil {
		return nil, false
	}
	ap.Mu.Lock()
	reason := ap.StopReason
	ap.Mu.Unlock()
	if reason != StopReasonMaxRetries && reason != StopReasonFatalError {
		return nil, false
	}
	return ap.Done, true
}
