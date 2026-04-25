package agentstatus

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// workspaceMeta holds derived per-workspace data used to enrich every entry.
type workspaceMeta struct {
	name             string
	repoBranch       map[string]string
	repoOrder        []ops.WorkspaceRepo
	crossRepoByName  map[string]bool
	haveRepoBranches bool
}

// loadWorkspaceMeta gathers workspace topology used to enrich each agent entry.
// Returns an empty meta on lookup failure or nil resolver — the handler must
// continue without aborting when wsConfig is unavailable.
func loadWorkspaceMeta(wsID string, wsConfigByIDFn func(string) (*ops.WorkspaceData, error)) workspaceMeta {
	if wsConfigByIDFn == nil {
		return workspaceMeta{}
	}
	wsData, err := wsConfigByIDFn(wsID)
	if err != nil {
		slog.Warn("agentstatus: workspace config lookup failed; falling back",
			"wsID", wsID, "err", err)
		return workspaceMeta{}
	}
	if wsData == nil {
		return workspaceMeta{}
	}
	m := workspaceMeta{name: wsData.Name}
	if len(wsData.Repos) > 0 {
		m.haveRepoBranches = true
		m.repoOrder = wsData.Repos
		m.repoBranch = make(map[string]string, len(wsData.Repos))
		for _, rp := range wsData.Repos {
			m.repoBranch[rp.Name] = rp.DefaultBranch
		}
	}
	if len(wsData.Agents) > 0 {
		m.crossRepoByName = make(map[string]bool, len(wsData.Agents))
		for _, ag := range wsData.Agents {
			m.crossRepoByName[ag.Name] = ag.CrossRepo
		}
	}
	return m
}

// resolveDefaultBranch picks the integration branch for an agent's
// ahead/behind comparison. Returns the branch and whether the agent's Repo
// resolved to a known workspace repo (false signals a stale daemon entry).
func resolveDefaultBranch(repo string, m workspaceMeta) (branch string, repoMatched bool) {
	repoMatched = true
	if repo != "" && m.haveRepoBranches {
		if b, ok := m.repoBranch[repo]; ok {
			branch = b
		} else {
			repoMatched = false
		}
	}
	if branch == "" && len(m.repoOrder) > 0 {
		branch = m.repoOrder[0].DefaultBranch
	}
	if branch == "" {
		branch = "main"
	}
	return branch, repoMatched
}

// initEntryFromDaemon copies daemon-sourced fields from a DaemonAgentEntry into
// the response shape. Git/lock/yield fields are filled separately.
func initEntryFromDaemon(a webui.DaemonAgentEntry, m workspaceMeta) AgentStatusEntry {
	return AgentStatusEntry{
		Worktree:         a.Worktree,
		WorktreePath:     a.WorktreePath,
		Path:             a.WorktreePath,
		Role:             a.Role,
		Repo:             a.Repo,
		Workspace:        m.name,
		CrossRepo:        m.crossRepoByName[a.Worktree],
		PID:              a.PID,
		SupervisorStatus: a.Status,
		RestartCount:     a.RestartCount,
		LastErrorClass:   a.LastErrorClass,
		BackoffUntil:     a.BackoffUntil,
		StopReason:       a.StopReason,
		EpicID:           a.EpicID,
		CurrentBackend:   a.CurrentBackend,
		RemoteBranch:     a.RemoteBranch,
	}
}

// applyGitStatus populates git/lock fields, falling back to the supervisor
// status when collectFn reports an error.
func applyGitStatus(entry *AgentStatusEntry, supervisorStatus string, git *webui.AgentGitStatus) {
	if git.Err != nil {
		entry.Status = supervisorStatus
		entry.Error = git.Err.Error()
		return
	}
	entry.Status = git.Status
	entry.Branch = git.Branch
	entry.Ahead = git.Ahead
	entry.Behind = git.Behind
	entry.Changes = git.Changes
	entry.TaskID = git.TaskID
}

// appendError concatenates a new message onto an existing error field, using
// "; " as a separator.
func appendError(existing, msg string) string {
	if existing == "" {
		return msg
	}
	return existing + "; " + msg
}

// buildEntry produces the per-agent response entry, applying the partial-
// failure envelope rules: collectFn errors fall back to supervisor_status with
// zero git fields and an `error` payload; unknown-repo concatenates onto any
// existing error with "; ".
func buildEntry(
	a webui.DaemonAgentEntry,
	m workspaceMeta,
	collectFn webui.AgentStatusCollectFn,
) AgentStatusEntry {
	defaultBranch, repoMatched := resolveDefaultBranch(a.Repo, m)
	git := collectFn(webui.AgentStatusCollectInput{
		WorktreePath:  a.WorktreePath,
		AgentName:     a.Worktree,
		Repo:          a.Repo,
		DefaultBranch: defaultBranch,
	})
	if git == nil {
		git = &webui.AgentGitStatus{}
	}

	entry := initEntryFromDaemon(a, m)
	applyGitStatus(&entry, a.Status, git)

	if !repoMatched {
		entry.Error = appendError(entry.Error, "unknown repo: "+a.Repo)
	}

	if y, _ := readYieldFile(a.WorktreePath); y != nil {
		entry.YieldRequested = true
		entry.YieldReason = y.Reason
		entry.YieldRequestedAt = y.RequestedAt
	}

	return entry
}

// resolveSupervisorState reads the daemon supervisor state and the workspace
// paths for the given wsID. Writes an error response and returns nil on
// failure; the caller should abort.
func resolveSupervisorState(
	w http.ResponseWriter,
	wsID string,
	supFn func(wsID string) (*webui.DaemonSupervisorData, error),
	resolverFn func(wsID string) (*webui.WorkspaceDaemonPaths, error),
) (*webui.DaemonSupervisorData, *webui.WorkspaceDaemonPaths) {
	paths, err := resolverFn(wsID)
	if err != nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable,
			dto.NewErrorResponse("daemon unavailable: "+err.Error(), "daemon_unavailable"))
		return nil, nil
	}
	sup, err := supFn(wsID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			handler.WriteJSON(w, http.StatusServiceUnavailable,
				dto.NewErrorResponse("daemon is not running for this workspace", "daemon_not_running"))
			return nil, nil
		}
		handler.WriteJSON(w, http.StatusInternalServerError,
			dto.NewErrorResponse("failed to read daemon state: "+err.Error(), "internal_error"))
		return nil, nil
	}
	return sup, paths
}

// collectEntries enumerates supervisor agents, skipping entries with no
// worktree path, and produces enriched response entries.
func collectEntries(
	wsID string,
	agents []webui.DaemonAgentEntry,
	meta workspaceMeta,
	collectFn webui.AgentStatusCollectFn,
) []AgentStatusEntry {
	entries := make([]AgentStatusEntry, 0, len(agents))
	for _, a := range agents {
		if a.WorktreePath == "" {
			slog.Debug("agentstatus: agent has empty worktree_path; skipping",
				"agent", a.Worktree, "wsID", wsID)
			continue
		}
		entries = append(entries, buildEntry(a, meta, collectFn))
	}
	return entries
}

// HandleAgentStatus returns the GET /api/workspaces/{ws}/agents/status handler.
// It enumerates agents from daemon-agents.json (via supFn — already
// workspace-scoped), enriches each with git+lock data via collectFn, reads
// .agent.yield inline, and computes IPC socket health from the resolver paths.
//
// Error envelope:
//   - 400 bad_request — workspace id missing in context
//   - 503 daemon_unavailable — resolver failure (workspace not found)
//   - 503 daemon_not_running — daemon-agents.json missing
//   - 500 internal_error — corrupt state file or other I/O failure
//   - 200 with per-entry "error" — collectFn failed for individual agents
func HandleAgentStatus(
	supFn func(wsID string) (*webui.DaemonSupervisorData, error),
	resolverFn func(wsID string) (*webui.WorkspaceDaemonPaths, error),
	wsConfigByIDFn func(string) (*ops.WorkspaceData, error),
	collectFn webui.AgentStatusCollectFn,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest,
				dto.NewErrorResponse("workspace id is required", "bad_request"))
			return
		}

		sup, paths := resolveSupervisorState(w, wsID, supFn, resolverFn)
		if sup == nil {
			return
		}

		meta := loadWorkspaceMeta(wsID, wsConfigByIDFn)

		ipcSocketActive := false
		if paths != nil && paths.StatePath != "" {
			ipcSocketActive = statExists(filepath.Join(filepath.Dir(paths.StatePath), "agent-ipc.sock"))
		}

		handler.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data": AgentStatusResponse{
				Agents:          collectEntries(wsID, sup.Agents, meta, collectFn),
				IPCSocketActive: ipcSocketActive,
				DaemonPID:       sup.PID,
				DaemonStartedAt: sup.StartedAt,
				WorkspaceName:   meta.name,
				Timestamp:       time.Now().UTC(),
			},
		})
	}
}

// statExists returns true iff os.Stat succeeds. False on any error including
// ErrNotExist or permission denied — both equivalent to "not active" at this
// observation point.
func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readYieldFile reads <dir>/.agent.yield. Returns (nil, nil) on missing file.
// Returns (nil, nil) on parse error too — yield is a hint, not authoritative,
// so a malformed file should not surface as a per-agent error.
func readYieldFile(dir string) (*yieldInfo, error) {
	path := filepath.Join(dir, ".agent.yield")
	data, err := os.ReadFile(path) //nolint:gosec // dir is the worktree path from daemon state
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var y yieldInfo
	if err := json.Unmarshal(data, &y); err != nil {
		return nil, nil
	}
	return &y, nil
}
