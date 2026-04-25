package daemonwire

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
)

// BuildAgentControlFn returns a callback that sends control commands to the
// daemon control socket. Returns nil if the socket path cannot be resolved
// (e.g., no daemon config). The socket path is resolved on each call to handle
// daemon restarts that may change the PID file location.
func BuildAgentControlFn() agentcontrol.AgentControlFn {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil
	}

	// Per-operation socket read deadlines.
	opDeadline := map[string]time.Duration{
		"agent_yield":   5 * time.Second,
		"agent_list":    5 * time.Second,
		"agent_start":   10 * time.Second,
		"agent_stop":    35 * time.Second, // daemon write deadline is 20s; 15s buffer
		"agent_restart": 80 * time.Second, // yieldTimeout(default 60s) + 20s buffer
	}

	return func(op, agentName string, force bool) (*agentcontrol.AgentControlResult, error) {
		socketPath := resolveControlSocketPath(projectDir)
		deadline := opDeadline[op]
		if deadline == 0 {
			deadline = 30 * time.Second
		}
		return sendControlRequest(socketPath, op, agentName, force, deadline)
	}
}

// resolveControlSocketPath returns the daemon control socket path for the
// given project directory. Re-resolves on each call because the daemon may
// restart with a different config.
func resolveControlSocketPath(projectDir string) string {
	dc, err := config.LoadDaemonConfig(projectDir)
	if err != nil {
		dc = &config.DaemonConfig{
			Daemon: config.DaemonSettings{PIDFile: ".loom/daemon.pid"},
		}
	}
	pidFilePath := dc.Daemon.PIDFile
	if !filepath.IsAbs(pidFilePath) {
		pidFilePath = filepath.Join(projectDir, pidFilePath)
	}
	return filepath.Join(filepath.Dir(pidFilePath), "daemon.sock")
}

// sendControlRequest dials the daemon control socket, sends a JSON request,
// and reads the JSON response. The readDeadline sets the per-operation timeout.
func sendControlRequest(socketPath, op, agentName string, force bool, readDeadline time.Duration) (*agentcontrol.AgentControlResult, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("daemon is not running (no control socket at %s)", socketPath)
	}
	defer func() { _ = conn.Close() }()

	reqData, err := json.Marshal(struct {
		Operation string `json:"operation"`
		AgentName string `json:"agent_name,omitempty"`
		Force     bool   `json:"force,omitempty"`
	}{Operation: op, AgentName: agentName, Force: force})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqData = append(reqData, '\n')

	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(reqData); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return nil, fmt.Errorf("read response: %w", scanErr)
		}
		return nil, fmt.Errorf("empty response from daemon")
	}

	var result agentcontrol.AgentControlResult
	if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &result, nil
}

// LoadDaemonSupervisor reads the loom supervisor state file for the given
// project directory and converts it to webui DTO types. Returns os.ErrNotExist
// if the daemon is not running for that directory.
func LoadDaemonSupervisor(projectDir string) (*webui.DaemonSupervisorData, error) {
	statePath := config.ResolveDaemonStatePath(projectDir)
	state, err := daemon.ReadStateFile(statePath)
	if err != nil {
		return nil, err
	}
	agents := make([]webui.DaemonAgentEntry, len(state.Agents))
	for i, a := range state.Agents {
		agents[i] = webui.DaemonAgentEntry{
			Worktree:       a.Worktree,
			Role:           a.Role,
			Repo:           a.Repo,
			PID:            a.PID,
			Status:         a.Status,
			TaskID:         a.TaskID,
			EpicID:         a.EpicID,
			CurrentBackend: a.CurrentBackend,
			RestartCount:   a.RestartCount,
			LastStart:      a.LastStart,
			LastExit:       a.LastExit,
			LastExitCode:   a.LastExitCode,
			StopReason:     a.StopReason,
			StoppedAt:      a.StoppedAt,
			WorktreePath:   a.WorktreePath,
			LastErrorClass: a.LastErrorClass,
			NoWorkCount:    a.NoWorkCount,
			BackoffUntil:   a.BackoffUntil,
			RemoteBranch:   a.RemoteBranch,
		}
	}
	return &webui.DaemonSupervisorData{
		PID:           state.PID,
		StartedAt:     state.StartedAt,
		UptimeSeconds: time.Since(state.StartedAt).Seconds(),
		Agents:        agents,
	}, nil
}

// LoadDaemonConfigRaw loads the loom.yaml for the given project directory and
// returns it as JSON.
func LoadDaemonConfigRaw(projectDir string) (json.RawMessage, error) {
	cfg, err := config.LoadDaemonConfig(projectDir)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal daemon config: %w", err)
	}
	return json.RawMessage(data), nil
}

// BuildDaemonSupervisorFn returns a callback bound to the server's working
// directory — used by the global /api/daemon/supervisor route. The
// workspace-scoped /api/workspaces/{ws}/daemon/supervisor route uses
// LoadDaemonSupervisor directly with the workspace's resolved path.
func BuildDaemonSupervisorFn() func() (*webui.DaemonSupervisorData, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil
	}
	return func() (*webui.DaemonSupervisorData, error) {
		return LoadDaemonSupervisor(projectDir)
	}
}

// BuildDaemonConfigFn returns a callback bound to the server's working
// directory — global /api/daemon/config counterpart to the workspace-scoped
// route (see BuildDaemonSupervisorFn).
func BuildDaemonConfigFn() func() (json.RawMessage, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil
	}
	return func() (json.RawMessage, error) {
		return LoadDaemonConfigRaw(projectDir)
	}
}

// BuildWorkspaceAgentQueueFn returns a workspace-aware callback that fetches
// and scores ready issues for a named agent. The resolver maps wsID to daemon
// paths; the returned function loads loom.yaml from the resolved workspace's
// WorkDir on each call.
func BuildWorkspaceAgentQueueFn(resolver func(wsID string) (*webui.WorkspaceDaemonPaths, error)) func(wsID, agentName string) ([]webui.AgentQueueEntry, error) {
	if resolver == nil {
		return nil
	}
	return func(wsID, agentName string) ([]webui.AgentQueueEntry, error) {
		resolved, err := resolver(wsID)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %q daemon: %w", wsID, err)
		}

		cfg, err := config.LoadDaemonConfig(resolved.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("load daemon config: %w", err)
		}

		// Find agent entry by worktree name
		var agent *config.AgentEntry
		for i := range cfg.Agents {
			if cfg.Agents[i].Worktree == agentName {
				agent = &cfg.Agents[i]
				break
			}
		}
		if agent == nil {
			return nil, webui.ErrAgentNotFound
		}

		// Resolve role constraints
		roleConfig, ok := cfg.ResolveRole(agent.Role)
		if !ok {
			roleConfig = config.RoleConfig{TaskFilter: "has_design"}
		}
		constraints := cli.MergeRoleConstraints(roleConfig, *agent)

		issues, err := cli.FetchReadyIssues(agent.Parent, agent.Repo)
		if err != nil {
			return nil, fmt.Errorf("fetch ready issues: %w", err)
		}

		return scoreAndSortQueue(issues, constraints), nil
	}
}

// scoreAndSortQueue scores issues against constraints and returns sorted entries.
func scoreAndSortQueue(issues []backend.IssueData, constraints cli.RoleConstraints) []webui.AgentQueueEntry {
	var entries []webui.AgentQueueEntry
	for _, issue := range issues {
		m := cli.MatchTask(issue, constraints)
		if m.Score > 0 {
			entries = append(entries, webui.AgentQueueEntry{
				IssueID:  m.Issue.ID,
				Title:    m.Issue.Title,
				Priority: m.Issue.Priority,
				Score:    m.Score,
				Reason:   m.Reason,
				Labels:   m.Issue.Labels,
				Parent:   m.Issue.Parent,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
		}
		if entries[i].Priority != entries[j].Priority {
			return entries[i].Priority < entries[j].Priority
		}
		return entries[i].IssueID < entries[j].IssueID
	})
	return entries
}

// BuildWorkspaceDaemonResolver returns a closure that maps a workspace ID to its
// daemon paths (socket, state file, config file, work dir). The closure uses
// wsConfigByIDFn to look up the workspace's projectDir, then delegates to the
// existing path-resolution helpers.
//
// The wsListFn argument is accepted for use by downstream callers (e.g., iterating
// all workspaces for auto-start) but is not used within the resolver closure itself.
// Taking it here avoids a breaking signature change later.
func BuildWorkspaceDaemonResolver(
	wsConfigByIDFn func(string) (*ops.WorkspaceData, error),
	wsListFn func() (map[string]string, error),
) func(wsID string) (*webui.WorkspaceDaemonPaths, error) {
	if wsConfigByIDFn == nil {
		return nil
	}
	_ = wsListFn // reserved for future use
	return func(wsID string) (*webui.WorkspaceDaemonPaths, error) {
		if wsID == "" {
			return nil, fmt.Errorf("resolve workspace daemon paths: empty workspace ID")
		}
		wsData, err := wsConfigByIDFn(wsID)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %q daemon paths: %w", wsID, err)
		}
		if wsData == nil || wsData.Path == "" {
			return nil, fmt.Errorf("resolve workspace %q daemon paths: workspace has no path", wsID)
		}
		return &webui.WorkspaceDaemonPaths{
			SocketPath: resolveControlSocketPath(wsData.Path),
			StatePath:  config.ResolveDaemonStatePath(wsData.Path),
			ConfigPath: filepath.Join(wsData.Path, "loom.yaml"),
			WorkDir:    wsData.Path,
		}, nil
	}
}

// BuildAgentStatusCollectFn wraps monitor.BuildSingleAgentStatusCollector in a
// type-adapter that targets webui.AgentStatusCollectFn. The inner closure is
// captured once so the package-level change-detector and commit caches in the
// monitor package are shared across calls and with the background monitor
// collector.
func BuildAgentStatusCollectFn() webui.AgentStatusCollectFn {
	mc := monitor.BuildSingleAgentStatusCollector()
	return func(in webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		r := mc(monitor.SingleAgentStatusInput{
			WorktreePath:  in.WorktreePath,
			AgentName:     in.AgentName,
			Repo:          in.Repo,
			DefaultBranch: in.DefaultBranch,
		})
		return &webui.AgentGitStatus{
			Status:  r.Status,
			Branch:  r.Branch,
			Ahead:   r.Ahead,
			Behind:  r.Behind,
			Changes: r.Changes,
			TaskID:  r.TaskID,
			Err:     r.Err,
		}
	}
}

// ListWorkspaces returns a map of workspace key (ID or name) to path.
func ListWorkspaces() (map[string]string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(cfg.Workspaces))
	for name, ws := range cfg.Workspaces {
		key := name
		if ws.ID != "" {
			key = ws.ID
		}
		result[key] = ws.Path
	}
	return result, nil
}
