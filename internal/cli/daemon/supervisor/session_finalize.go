package supervisor

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// leafUsage is the token/cost usage a canonical transcript's terminal `result`
// entry carries (the TS leaf serializes it into that entry's `output` field —
// local-task-runner resultEntry/taskUsageFromEntries). The Go leaf's raw stream
// has no such entry, so it decodes to the zero value (usage stays 0, unchanged).
type leafUsage struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

func (u leafUsage) cost() float64 {
	if u.CostUSD != 0 {
		return u.CostUSD
	}
	return u.EstimatedCostUSD
}

// readLeafTranscript reads the local session's on-disk native transcript ONCE
// so finalization can backfill token usage (collector-less finalization would
// otherwise record tokens=0). ok=false when there is no transcript yet.
func (s *Supervisor) readLeafTranscript(sessionID string) (data []byte, usage leafUsage, ok bool) {
	if sessionID == "" {
		return nil, leafUsage{}, false
	}
	sessStore, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return nil, leafUsage{}, false
	}
	data, err = os.ReadFile(sessStore.NativeTranscriptPath(sessionID)) //nolint:gosec // session-owned path
	if err != nil || len(data) == 0 {
		return nil, leafUsage{}, false
	}
	return data, extractLeafUsage(data), true
}

// extractLeafUsage scans a canonical transcript from the end for the terminal
// `result` entry and decodes the usage object the TS leaf serialized into its
// `output` field. Returns the zero value for a raw backend stream (no `result`).
func extractLeafUsage(data []byte) leafUsage {
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var ev struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type != "result" || ev.Output == "" {
			continue
		}
		var u leafUsage
		if json.Unmarshal([]byte(ev.Output), &u) != nil {
			return leafUsage{}
		}
		return u
	}
	return leafUsage{}
}

func (s *Supervisor) completeBackendUnavailableCleanup(ap *AgentProcess) {
	s.completePreSpawnCleanup(ap, "backend_unavailable")
}

// completePreSpawnCleanup unwinds task, local-session, and worker state created by
// preFlightSetup when the subprocess cannot safely be started.
func (s *Supervisor) completePreSpawnCleanup(ap *AgentProcess, errClass string) {
	state := takeAgentSessionForFinalize(ap)
	taskID := s.taskIDForLifecycle(ap, nil)
	if state.session != nil {
		_ = state.session.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: errClass})
	}
	if taskID != "" {
		s.releaseAssignedTaskClaim(ap, taskID)
	}
	s.deregisterWorker(ap)
}

type agentSessionFinalizeState struct {
	session   *sessions.Session
	sessionID string
	beforeRef string
}

// finalizeAgentSession finalizes the daemon-created session after agent exit.
func (s *Supervisor) finalizeAgentSession(ap *AgentProcess, exitCode int) {
	state := takeAgentSessionForFinalize(ap)
	taskID := s.taskIDForFinalize(ap)
	if state.session != nil || state.sessionID != "" {
		errClass := agentErrorClass(ap)
		// Read before finalizeLocalSession, whose codex/claude re-sync can
		// rewrite the on-disk file. Usage remains local-session evidence; batch
		// execution no longer shadows that evidence into Interaction-owned
		// AgentSession rows.
		_, usage, _ := s.readLeafTranscript(state.sessionID)
		_ = finalizeLocalSession(state.session, ap, state.beforeRef, taskID, exitCode, errClass, usage)
	}
	// Claim and Worker cleanup are execution lifecycle responsibilities. They
	// must not depend on local transcript-session creation having succeeded.
	s.releaseAssignedTaskClaim(ap, taskID)
	s.deregisterWorker(ap)
}

// takeAgentSessionForFinalize retires local session state and revokes the
// process-local IPC credential before transcript and Git finalization.
func takeAgentSessionForFinalize(ap *AgentProcess) agentSessionFinalizeState {
	ap.Mu.Lock()
	state := agentSessionFinalizeState{
		session:   ap.Session,
		sessionID: ap.AgentSessionID,
		beforeRef: ap.BeforeRef,
	}
	ap.Session = nil
	ap.AgentSessionID = ""
	ap.AgentIPCAuthToken = ""
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
	usage leafUsage,
) sessionfinalize.WithWorktreeResult {
	result, err := sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath: ap.WorktreePath,
		BeforeRef:    beforeRef,
		TaskID:       taskID,
		ExitCode:     exitCode,
		ErrorClass:   errClass,
		// Carry the leaf's reported usage so the supervisor's collector-less finalize
		// records non-zero tokens on the session (otherwise the reaped worker's
		// collector-aware finalize never runs and tokens land 0). Zero for the Go leaf.
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		EstimatedCostUSD: usage.cost(),
	})
	if err != nil {
		slog.Warn("session finalization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
	return result
}
