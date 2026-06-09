package supervisor

import (
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func (s *Supervisor) completeBackendUnavailableCleanup(ap *AgentProcess) {
	state := takeAgentSessionForFinalize(ap)
	taskID := s.taskIDForLifecycle(ap, nil)
	if state.session != nil {
		_ = state.session.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "backend_unavailable"})
	}
	if state.sessionID != "" {
		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:  state.sessionID,
			leaseID:    state.leaseID,
			leaseToken: state.leaseToken,
			exitCode:   -1,
			errClass:   "backend_unavailable",
			taskID:     taskID,
		})
		return
	}
	if taskID == "" {
		return
	}
	s.releaseAssignedTaskClaim(ap, taskID)
	s.deregisterWorker(ap)
}

type agentSessionFinalizeState struct {
	session    *sessions.Session
	sessionID  string
	leaseID    string
	leaseToken string
	beforeRef  string
}

// finalizeAgentSession finalizes the daemon-created session after agent exit.
func (s *Supervisor) finalizeAgentSession(ap *AgentProcess, exitCode int) {
	state := takeAgentSessionForFinalize(ap)
	if state.session == nil && state.sessionID == "" {
		return
	}
	taskID := s.taskIDForFinalize(ap)
	errClass := agentErrorClass(ap)
	diffResult := finalizeLocalSession(state.session, ap, state.beforeRef, taskID, exitCode, errClass)
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:  state.sessionID,
		leaseID:    state.leaseID,
		leaseToken: state.leaseToken,
		exitCode:   exitCode,
		errClass:   errClass,
		taskID:     taskID,
		diffResult: diffResult,
	})
}

func takeAgentSessionForFinalize(ap *AgentProcess) agentSessionFinalizeState {
	ap.Mu.Lock()
	state := agentSessionFinalizeState{
		session:    ap.Session,
		sessionID:  ap.AgentSessionID,
		leaseID:    ap.AgentLeaseID,
		leaseToken: ap.AgentLeaseToken,
		beforeRef:  ap.BeforeRef,
	}
	ap.Session = nil
	ap.AgentSessionID = ""
	ap.AgentLeaseID = ""
	ap.AgentLeaseToken = ""
	ap.Mu.Unlock()
	return state
}

func (s *Supervisor) taskIDForFinalize(ap *AgentProcess) string {
	taskID := ""
	if info, lockErr := cli.ReadLockFile(ap.WorktreePath); lockErr == nil {
		taskID = info.TaskID
	}
	if taskID == "" {
		taskID = s.taskIDForLifecycle(ap, nil)
	}
	return taskID
}

func agentErrorClass(ap *AgentProcess) string {
	ap.Mu.Lock()
	errClass := ""
	if ap.LastError != nil {
		errClass = ap.LastError.Class.String()
	}
	ap.Mu.Unlock()
	return errClass
}

func finalizeLocalSession(
	sess *sessions.Session,
	ap *AgentProcess,
	beforeRef string,
	taskID string,
	exitCode int,
	errClass string,
) sessionfinalize.WithWorktreeResult {
	result, err := sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath: ap.WorktreePath,
		BeforeRef:    beforeRef,
		TaskID:       taskID,
		ExitCode:     exitCode,
		ErrorClass:   errClass,
	})
	if err != nil {
		slog.Warn("session finalization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
	return result
}
