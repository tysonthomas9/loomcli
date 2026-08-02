package daemonwire

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

// BuildDaemonSupervisorFn returns a callback that reads the daemon state file
// and converts it to webui DTO types. Returns nil if the working directory
// cannot be resolved.
func BuildDaemonSupervisorFn() func() (*webui.DaemonSupervisorData, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil
	}

	return func() (*webui.DaemonSupervisorData, error) {
		statePath := config.ResolveDaemonStatePath(projectDir)
		state, err := daemon.ReadStateFile(statePath)
		if err != nil {
			return nil, err // os.ErrNotExist if file missing
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
}

// BuildDaemonConfigFn returns a callback that loads and marshals the daemon
// config to JSON. Returns nil if the working directory cannot be resolved.
func BuildDaemonConfigFn() func() (json.RawMessage, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil
	}

	return func() (json.RawMessage, error) {
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
}

// BuildStoreBackedDaemonConfigFn returns the effective daemon config for the
// active FleetDB workspace. It preserves the JSON shape consumed by the
// WebUI while sourcing agents, roles, and profile settings from store data.
func BuildStoreBackedDaemonConfigFn(s store.Store) func() (json.RawMessage, error) {
	if s == nil {
		return nil
	}
	return func() (json.RawMessage, error) {
		ctx := context.Background()
		wsKey, err := bootstrap.ResolveActiveWorkspaceKey(ctx, s.Workspaces())
		if err != nil {
			return nil, fmt.Errorf("resolve active workspace: %w", err)
		}
		cfg, err := daemonConfigFromStore(ctx, s, wsKey)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("marshal daemon config: %w", err)
		}
		return json.RawMessage(data), nil
	}
}

func daemonConfigFromStore(ctx context.Context, s store.Store, wsKey string) (*config.DaemonConfig, error) {
	profile, err := s.Daemon().Get(ctx, wsKey)
	if err != nil {
		return nil, fmt.Errorf("get daemon profile: %w", err)
	}
	roles, err := s.Roles().List(ctx, wsKey)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	agents, err := s.Agents().List(ctx, wsKey)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}

	cfg := &config.DaemonConfig{
		Daemon: daemonSettingsFromProfile(profile),
		Roles:  make(map[string]config.RoleConfig, len(roles)),
		Agents: make([]config.AgentEntry, 0, len(agents)),
	}
	cfg.Backend = cfg.Daemon.IssueBackend
	for _, r := range roles {
		cfg.Roles[r.Name] = roleConfigFromDomain(r)
	}
	for _, a := range agents {
		cfg.Agents = append(cfg.Agents, agentEntryFromDomain(a))
	}
	return cfg, nil
}

func daemonSettingsFromProfile(p *domain.DaemonProfile) config.DaemonSettings {
	if p == nil {
		return config.DaemonSettings{IssueBackend: "fleetdb"}
	}
	issueBackend := p.IssueBackend
	if issueBackend == "" {
		issueBackend = "fleetdb"
	}
	return config.DaemonSettings{
		PIDFile:        p.PIDFile,
		LogDir:         p.LogDir,
		EventsDir:      p.EventsDir,
		RestartPolicy:  restartPolicyFromDomain(p.RestartPolicy),
		MaxAgents:      cloneIntPtr(p.MaxAgents),
		OTel:           otelFromDomain(p.OTel),
		IssueBackend:   issueBackend,
		StartupTimeout: cloneIntPtr(p.StartupTimeout),
	}
}

func roleConfigFromDomain(r *domain.Role) config.RoleConfig {
	if r == nil {
		return config.RoleConfig{}
	}
	return config.RoleConfig{
		Kind:           string(r.Kind),
		Description:    r.Description,
		Prompt:         r.Prompt,
		PromptFile:     r.PromptFile,
		Model:          r.Model,
		TaskFilter:     r.TaskFilter,
		Backend:        r.Backend,
		Effort:         r.Effort,
		PathPatterns:   append([]string(nil), r.PathPatterns...),
		Skills:         append([]string(nil), r.Skills...),
		Labels:         append([]string(nil), r.Labels...),
		ExcludeLabels:  append([]string(nil), r.ExcludeLabels...),
		MaxPriority:    cloneIntPtr(r.MaxPriority),
		MaxConcurrency: cloneIntPtr(r.MaxConcurrency),
		ReadOnly:       r.ReadOnly,
		AllowedTools:   append([]string(nil), r.AllowedTools...),
		DeniedTools:    append([]string(nil), r.DeniedTools...),
		MaxBudgetUSD:   cloneFloatPtr(r.MaxBudgetUSD),
	}
}

func agentEntryFromDomain(a *domain.Agent) config.AgentEntry {
	if a == nil {
		return config.AgentEntry{}
	}
	return config.AgentEntry{
		Worktree:         a.Name,
		Role:             a.RoleName,
		Auto:             a.Auto,
		Backend:          a.Backend,
		FallbackBackends: append([]string(nil), a.FallbackBackends...),
		Repos:            append([]string(nil), a.Repos...),
		RepoGroups:       append([]string(nil), a.RepoGroups...),
		CrossRepo:        a.CrossRepo,
		Parent:           a.Parent,
		DesiredState:     a.DesiredState,
	}
}

func restartPolicyFromDomain(r domain.RestartPolicy) config.RestartPolicy {
	return config.RestartPolicy{
		MaxRetries:       cloneIntPtr(r.MaxRetries),
		BackoffInitial:   cloneIntPtr(r.BackoffInitial),
		BackoffMax:       cloneIntPtr(r.BackoffMax),
		OutputTimeout:    cloneIntPtr(r.OutputTimeout),
		RateLimitBackoff: cloneIntPtr(r.RateLimitBackoff),
		RateLimitMaxWait: cloneIntPtr(r.RateLimitMaxWait),
		RateLimitNoCount: cloneBoolPtr(r.RateLimitNoCount),
		TimeoutBackoff:   cloneIntPtr(r.TimeoutBackoff),
		NoWorkBackoff:    cloneIntPtr(r.NoWorkBackoff),
		IdlePollInterval: cloneIntPtr(r.IdlePollInterval),
		YieldTimeout:     cloneIntPtr(r.YieldTimeout),
		SigtermTimeout:   cloneIntPtr(r.SigtermTimeout),
	}
}

func otelFromDomain(o *domain.OTelSettings) *config.OTelDaemonConfig {
	if o == nil {
		return nil
	}
	return &config.OTelDaemonConfig{
		Enabled:         o.Enabled,
		Endpoint:        o.Endpoint,
		Protocol:        o.Protocol,
		ServiceName:     o.ServiceName,
		SampleRate:      o.SampleRate,
		FlushIntervalMs: o.FlushIntervalMs,
		Traces:          cloneBoolPtr(o.Traces),
		Metrics:         cloneBoolPtr(o.Metrics),
	}
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// BuildAgentQueueFn returns a callback that fetches and scores ready issues
// for a named agent using the task router. Returns nil if the working
// directory cannot be resolved.
func BuildAgentQueueFn() func(string) ([]webui.AgentQueueEntry, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil
	}

	return func(agentName string) ([]webui.AgentQueueEntry, error) {
		cfg, err := config.LoadDaemonConfig(projectDir)
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
