package supervisor

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// leafUsage is the token/cost usage a canonical transcript's terminal `result`
// entry carries (the TS leaf serializes it into that entry's `output` field —
// local-task-runner resultEntry/taskUsageFromEntries). The Go leaf's raw stream
// has no such entry, so that decode yields the zero value; harnessLeafUsage is
// the second source that covers the Go leaf.
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

// readLeafTranscript reads the session's on-disk native transcript ONCE so the
// finalize can reuse it for both the on-disk token backfill (the supervisor's
// collector-less finalize otherwise lands tokens=0) and the control-plane
// transcript_ref artifact upload. ok=false when there is no transcript yet.
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

// resolveLeafTokens applies the two-source precedence for a collector-less
// finalize: the TS leaf's own accounting when it reported any, otherwise the
// back-fill read from the harness's transcript.
//
// The TS leaf wins because its `result` entry is the runner's first-hand
// accounting for exactly this run, while the back-fill infers a session from
// what is newest on disk. backfill is a thunk so the harness read — which walks
// a project directory and parses JSONL — is skipped entirely on the TS path.
func resolveLeafTokens(fromResultEntry leafUsage, backfill func() leafUsage) leafUsage {
	if fromResultEntry != (leafUsage{}) {
		return fromResultEntry
	}
	return backfill()
}

// backfillHarnessUsage back-fills tokens for the Go leaf, which writes no
// canonical `result` entry for extractLeafUsage to find.
//
// It gathers the three things the harness transcript readers need — which
// backend ran, which working directory it ran in, and when the session started
// — then reads the totals straight out of the backend's own session log. The
// worktree lock's carried claude_session_id is passed as a hint; it is present
// after a failed run (the daemon keeps it for --resume) and cleared after a
// successful one, so the resolver falls back to the newest transcript written
// since the session began, exactly as the transcript mirror already does.
//
// Best-effort throughout: every miss returns the zero value, which is the
// tokens=0 that used to be recorded unconditionally.
func (s *Supervisor) backfillHarnessUsage(ap *AgentProcess, sess *sessions.Session) leafUsage {
	backend := s.GetEffectiveBackend(ap)
	if backend == "" || ap.WorktreePath == "" {
		return leafUsage{}
	}

	ap.Mu.Lock()
	since := ap.LastStart
	ap.Mu.Unlock()
	if sess != nil && !sess.Meta.StartedAt.IsZero() {
		since = sess.Meta.StartedAt
	}

	hint := ""
	if info, lockErr := cli.ReadLockFile(ap.WorktreePath); lockErr == nil {
		hint = info.ClaudeSessionID
	}
	return harnessLeafUsage(backend, ap.WorktreePath, hint, since)
}

// harnessLeafUsage is the pure half: resolve the harness session id, read its
// cumulative usage, and price it.
//
// The token counts are carried across verbatim — see
// backends.SessionTokensFromHarnessUsage for why Claude's and Codex's
// input_tokens must not be reconciled with each other. The cost is an ESTIMATE
// from loom's own pricing table, not a harness-reported figure, so it lands in
// EstimatedCostUSD and leafUsage.cost() still lets a real cost_usd win wherever
// one exists.
func harnessLeafUsage(backend, workDir, hintSessionID string, since time.Time) leafUsage {
	sessionID := sessions.LatestHarnessSessionID(backend, workDir, hintSessionID, since)
	u := backends.ReadHarnessUsage(backend, sessionID, workDir)
	if u == nil {
		return leafUsage{}
	}
	input, output, cacheRead, cacheWrite := backends.SessionTokensFromHarnessUsage(u)
	out := leafUsage{
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
	out.EstimatedCostUSD = usage.EstimateCost(usage.ResolvePricing(backend), usage.SessionUsage{
		InputTokens:      out.InputTokens,
		OutputTokens:     out.OutputTokens,
		CacheReadTokens:  out.CacheReadTokens,
		CacheWriteTokens: out.CacheWriteTokens,
	})
	return out
}

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
	// Read the leaf transcript once: it feeds both the on-disk token backfill (via
	// finalizeLocalSession) and the control-plane transcript_ref artifact upload.
	// Read before finalizeLocalSession, whose codex/claude re-sync can rewrite the
	// on-disk file — this captures the TS leaf's canonical transcript verbatim.
	transcriptData, resultEntryTokens, _ := s.readLeafTranscript(state.sessionID)
	leafTokens := resolveLeafTokens(resultEntryTokens, func() leafUsage {
		return s.backfillHarnessUsage(ap, state.session)
	})
	diffResult := finalizeLocalSession(state.session, ap, state.beforeRef, taskID, exitCode, errClass, leafTokens)
	// KNOWN GAP — local session only. leafTokens lands on the on-disk session
	// record; it does NOT reach the control plane, because store.AgentSessionUpdate
	// has no token or cost fields (only Status/TaskID/FinishedAt/ErrorClass/
	// ExitCode/Metadata). The one hole it would fit through is the untyped
	// Metadata map, and smuggling stringified token counts through there would
	// invent a wire contract fleet-db does not agree to. Carrying usage to the
	// control plane needs typed fields on AgentSessionUpdate first; that is a
	// fleet-db schema change, deliberately not made here.
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:      state.sessionID,
		leaseID:        state.leaseID,
		leaseToken:     state.leaseToken,
		exitCode:       exitCode,
		errClass:       errClass,
		taskID:         taskID,
		diffResult:     diffResult,
		transcriptData: transcriptData,
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
	leafTokens leafUsage,
) sessionfinalize.WithWorktreeResult {
	result, err := sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath: ap.WorktreePath,
		BeforeRef:    beforeRef,
		TaskID:       taskID,
		ExitCode:     exitCode,
		ErrorClass:   errClass,
		// Carry the leaf's reported usage so the supervisor's collector-less finalize
		// records non-zero tokens on the session (otherwise the reaped worker's
		// collector-aware finalize never runs and tokens land 0). Sourced from the
		// TS leaf's `result` entry, else from the harness's own transcript.
		InputTokens:      leafTokens.InputTokens,
		OutputTokens:     leafTokens.OutputTokens,
		CacheReadTokens:  leafTokens.CacheReadTokens,
		CacheWriteTokens: leafTokens.CacheWriteTokens,
		EstimatedCostUSD: leafTokens.cost(),
	})
	if err != nil {
		slog.Warn("session finalization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
	return result
}
