package supervisor

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"

	"go.opentelemetry.io/otel/attribute"
)

// ResolveDaemonPath resolves a path relative to projectDir, or returns it
// unchanged when it is already absolute.
func ResolveDaemonPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(projectDir, path)
}

// buildCommand constructs the exec.Cmd for spawning an agent subprocess (does not start it).
func (s *Supervisor) buildCommand(ap *AgentProcess) (*exec.Cmd, error) {
	cfg := s.ConfigSnapshot()

	ap.Mu.Lock()
	epicID := ap.AssignedEpicID
	ap.Mu.Unlock()

	agentBackend := s.GetEffectiveBackend(ap)
	cmd, err := buildAgentExecCmd(ap, agentBackend, epicID)
	if err != nil {
		return nil, err
	}

	cmd.Dir = ap.WorktreePath
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Env = append(cli.FilteredEnv(),
		fmt.Sprintf("LOOM_AGENT_NAME=%s", ap.Entry.Worktree),
		fmt.Sprintf("LOOM_WORKTREE_PATH=%s", ap.WorktreePath),
		fmt.Sprintf("LOOM_EVENTS_DIR=%s", ResolveDaemonPath(s.ProjectDir, cfg.Daemon.EventsDir)),
	)

	cmd.Env = appendRoleEnv(cmd.Env, ap)
	cmd.Env = appendRoutingEnv(cmd.Env, ap)

	sourceRepos, err := cfgpkg.ResolveAgentRepos(ap.Entry, s.Repos)
	if err != nil {
		return nil, fmt.Errorf("resolve agent repos: %w", err)
	}
	if len(sourceRepos) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_SOURCE_REPOS=%s", strings.Join(sourceRepos, ",")))
	}

	ap.Mu.Lock()
	assignedTaskID := ap.AssignedTaskID
	ap.Mu.Unlock()
	if assignedTaskID != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ASSIGNED_TASK_ID=%s", assignedTaskID))
	}

	cmd.Env = s.appendDaemonEnv(cmd.Env)
	cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_YIELD_FILE=%s", filepath.Join(ap.WorktreePath, YieldFileName)))
	cmd.Env = appendSessionEnv(cmd.Env, ap)

	// Propagate the active trace context so the agent subprocess's bootstrap
	// span and per-request spans inherit the daemon's trace tree.
	// See docs/observability/tracing-contract.md §5.
	if tp := tracing.TraceparentFromContext(cmdstore.RootContext()); tp != "" {
		cmd.Env = append(cmd.Env, "LOOM_TRACE_PARENT="+tp)
	}

	return cmd, nil
}

// buildAgentExecCmd creates the exec.Cmd with the correct arguments for the agent role.
// loomExecutablePath resolves the loom binary that agent workers re-exec.
// It is a seam for tests: under `go test`, os.Executable() is the test binary
// itself, so spawning it would recursively run the whole test suite — every
// spawned "agent" spawns more agents (fork bomb). Tests override this to a
// harmless stub; see TestMain in main_test.go.
var loomExecutablePath = os.Executable

func buildAgentExecCmd(ap *AgentProcess, backend, epicID string) (*exec.Cmd, error) {
	loomPath, err := loomExecutablePath()
	if err != nil {
		return nil, fmt.Errorf("resolve loom executable: %w", err)
	}
	if BuiltInRoles[ap.Entry.Role] {
		args := []string{ap.Entry.Role, ap.WorktreePath, "--auto", "--daemon-mode"}
		if backend != "" {
			args = append(args, "--backend", backend)
		}
		if epicID != "" {
			args = append(args, "--parent", epicID)
		}
		return exec.Command(loomPath, args...), nil //nolint:gosec // G204: intentional loom subprocess launch
	}

	if strings.TrimSpace(ap.RoleConfig.TaskFilter) == "bug" && !ap.RoleConfig.ReadOnly {
		return nil, fmt.Errorf("custom role %q task_filter %q requires read_only=true", ap.Entry.Role, "bug")
	}
	promptFile := strings.TrimSpace(ap.RoleConfig.PromptFile)
	if promptFile == "" {
		return nil, fmt.Errorf("custom role %q missing prompt_file", ap.Entry.Role)
	}
	args := []string{"agent", ap.WorktreePath, "--prompt", promptFile, "--auto", "--daemon-mode"}
	if ap.RoleConfig.TaskFilter != "" {
		args = append(args, "--task-filter", ap.RoleConfig.TaskFilter)
	}
	if backend != "" {
		args = append(args, "--backend", backend)
	}
	if epicID != "" {
		args = append(args, "--parent", epicID)
	}
	return exec.Command(loomPath, args...), nil //nolint:gosec // G204: intentional loom subprocess launch
}

// appendRoleEnv adds role constraint env vars (allowed/denied tools, read-only, repo).
func appendRoleEnv(env []string, ap *AgentProcess) []string {
	// Role configuration is authoritative. Do not let a daemon-level
	// LOOM_READ_ONLY value leak into a role that was created or edited as
	// writable.
	filtered := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, "LOOM_READ_ONLY=") {
			filtered = append(filtered, entry)
		}
	}
	env = filtered

	if ap.Entry.Repo != "" {
		env = append(env, fmt.Sprintf("LOOM_AGENT_REPO=%s", ap.Entry.Repo))
	}
	if len(ap.RoleConfig.AllowedTools) > 0 {
		env = append(env, fmt.Sprintf("LOOM_ALLOWED_TOOLS=%s", strings.Join(ap.RoleConfig.AllowedTools, ",")))
	}
	if len(ap.RoleConfig.DeniedTools) > 0 {
		env = append(env, fmt.Sprintf("LOOM_DENIED_TOOLS=%s", strings.Join(ap.RoleConfig.DeniedTools, ",")))
	}
	if ap.RoleConfig.ReadOnly {
		env = append(env, "LOOM_READ_ONLY=1")
	}
	if ap.RoleConfig.MaxBudgetUSD != nil {
		env = append(env, fmt.Sprintf("LOOM_MAX_BUDGET_USD=%.2f", *ap.RoleConfig.MaxBudgetUSD))
	}
	if ap.RoleConfig.Effort != "" {
		env = append(env,
			fmt.Sprintf("LOOM_AGENT_EFFORT=%s", ap.RoleConfig.Effort),
			fmt.Sprintf("LOOM_CLAUDE_EFFORT=%s", ap.RoleConfig.Effort),
		)
	}
	return env
}

// appendRoutingEnv adds routing constraint env vars (skills, path patterns, priority, role).
func appendRoutingEnv(env []string, ap *AgentProcess) []string {
	if len(ap.RoleConfig.Skills) > 0 {
		env = append(env, fmt.Sprintf("LOOM_ROLE_SKILLS=%s", strings.Join(ap.RoleConfig.Skills, ",")))
	}
	if len(ap.RoleConfig.PathPatterns) > 0 {
		env = append(env, fmt.Sprintf("LOOM_ROLE_PATH_PATTERNS=%s", strings.Join(ap.RoleConfig.PathPatterns, ",")))
	}
	if ap.RoleConfig.MaxPriority != nil {
		env = append(env, fmt.Sprintf("LOOM_ROLE_MAX_PRIORITY=%d", *ap.RoleConfig.MaxPriority))
	}
	if ap.RoleConfig.TaskFilter != "" {
		env = append(env, fmt.Sprintf("LOOM_ROLE_TASK_FILTER=%s", ap.RoleConfig.TaskFilter))
	}
	env = append(env, fmt.Sprintf("LOOM_ROLE=%s", ap.Entry.Role))
	if len(ap.Entry.PathPatterns) > 0 {
		env = append(env, fmt.Sprintf("LOOM_AGENT_PATH_PATTERNS=%s", strings.Join(ap.Entry.PathPatterns, ",")))
	}
	return env
}

// appendSessionEnv adds session-related env vars for transcript-based liveness tracking.
func appendSessionEnv(env []string, ap *AgentProcess) []string {
	ap.Mu.Lock()
	sessionID := ""
	if ap.Session != nil {
		sessionID = ap.Session.SessionID()
	}
	leaseID := ap.AgentLeaseID
	leaseToken := ap.AgentLeaseToken
	parentSessionID := ap.ParentSessionID
	ownershipLeaseID := ap.OwnershipLeaseID
	ownershipFencingToken := ap.OwnershipFencingToken
	ap.Mu.Unlock()
	if sessionID != "" {
		env = append(env,
			fmt.Sprintf("LOOM_SESSION_ID=%s", sessionID),
			fmt.Sprintf("LOOM_WORKSPACE_RUNTIME_DIR=%s", cli.GetWorkspaceRuntimeDir()),
		)
	}
	if leaseID != "" && leaseToken != "" {
		env = append(env,
			fmt.Sprintf("LOOM_AGENT_LEASE_ID=%s", leaseID),
			fmt.Sprintf("LOOM_AGENT_LEASE_TOKEN=%s", leaseToken),
		)
	}
	if parentSessionID != "" {
		env = append(env, fmt.Sprintf("LOOM_ORCHESTRATOR_SESSION_ID=%s", parentSessionID))
	}
	if ownershipLeaseID != "" {
		env = append(env,
			fmt.Sprintf("LOOM_AGENT_OWNERSHIP_LEASE_ID=%s", ownershipLeaseID),
			// LOOM_AGENT_OWNERSHIP_FENCING_TOKEN is write-only by contract —
			// if you add a runtime reader, the verify-re-acquire path in
			// ownership.go must refresh the token (or kill instead of
			// continuing): re-acquire can bump the fencing token while the
			// running subprocess keeps its spawn-time env. Guarded by
			// TestOwnershipFencingEnvHasNoRuntimeReader.
			fmt.Sprintf("LOOM_AGENT_OWNERSHIP_FENCING_TOKEN=%d", ownershipFencingToken),
		)
	}
	return env
}

// spawnAgent starts the subprocess for an agent. The whole sequence
// (buildCommand → cmd.Start → first control-plane heartbeat) is wrapped in a
// daemon.supervisor.spawn span so failures classify cleanly as either a
// build/start failure or a heartbeat failure.
//
//nolint:funlen // Linear orchestration: gate → build → start → record. Each step is short; extracting would fragment the lifecycle.
func (s *Supervisor) spawnAgent(ap *AgentProcess) error {
	_, span := startSpan(cmdstore.RootContext(),
		"daemon.supervisor.spawn",
		attribute.String("loom.agent", ap.Entry.Worktree),
		attribute.String("loom.role", ap.Entry.Role),
		attribute.String("loom.workspace", s.WorkspaceID),
	)
	defer span.End()

	if err := s.gateBackendAvailable(ap); err != nil {
		recordErr(span, err, "spawn.backend_unavailable")
		return err
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		recordErr(span, err, "spawn.build_command")
		return fmt.Errorf("build command: %w", err)
	}

	ap.Mu.Lock()

	s.setupAgentLogFile(ap, cmd)

	if err := cmd.Start(); err != nil {
		closeAgentLogs(ap)
		ap.Mu.Unlock()
		recordErr(span, err, "spawn.start")
		return fmt.Errorf("failed to start subprocess: %w", err)
	}

	ap.Cmd = cmd
	ap.Pid = cmd.Process.Pid
	ap.LastStart = time.Now()
	ap.StopReason = ""
	ap.BackoffUntil = time.Time{}

	pid := ap.Pid
	worktree := ap.Entry.Worktree
	role := ap.Entry.Role
	epicID := ap.AssignedEpicID
	ap.Mu.Unlock()

	span.SetAttributes(attribute.Int("loom.pid", pid))

	log.Printf("[daemon] Agent %s: spawned subprocess PID %d", worktree, pid)

	if evt, err := events.NewEvent(events.AgentStarted, worktree, role, epicID, events.AgentStartedData{PID: pid}); err == nil {
		s.EmitEvent(evt)
	}
	s.markControlPlaneAgentSessionRunning(ap)

	return nil
}

// setupAgentLogFile wires the agent subprocess's stdout/stderr to its log
// sinks. Up to two sinks are active:
//
//   - The daemon process log (<daemon.LogDir>/<ws>/<role>-<worktree>.log): the
//     watchdog stats its mtime for liveness (see checkWatchdog) and the crash
//     classifier reads its tail (see classify.go), so its path and append
//     semantics are preserved exactly.
//   - The canonical agent archive (~/.loom/logs/<ws>/agents/<worktree>.log): the
//     web UI "Logs" tab reads this via webuilog.GetAgentLogPath. Without it,
//     daemon-supervised agents 404 in the Logs tab even though tmux-mode agents
//     — whose pane tmux routes here via `pipe-pane … loom log-router` — are
//     inspectable. Daemon-mode agents bypass tmux, so we write it directly.
//
// When both sinks are open the child's output is fanned out with io.MultiWriter.
// The watchdog still observes the daemon log's mtime because the os/exec copy
// advances it as output arrives. Must be called while ap.Mu is held.
func (s *Supervisor) setupAgentLogFile(ap *AgentProcess, cmd *exec.Cmd) {
	var sinks []io.Writer

	if f := s.openDaemonLogFile(ap); f != nil {
		ap.LogFile = f
		sinks = append(sinks, f)
	}
	if af := s.openAgentArchiveLog(ap); af != nil {
		ap.ArchiveLogFile = af
		sinks = append(sinks, af)
	}

	switch len(sinks) {
	case 0:
		return
	case 1:
		cmd.Stdout, cmd.Stderr = sinks[0], sinks[0]
	default:
		w := io.MultiWriter(sinks...)
		cmd.Stdout, cmd.Stderr = w, w
	}
}

// openDaemonLogFile opens the daemon process log consumed by the watchdog and
// crash classifier. Returns nil (logging disabled for this sink) when no LogDir
// is configured or the file can't be opened.
func (s *Supervisor) openDaemonLogFile(ap *AgentProcess) *os.File {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.LogDir == "" {
		return nil
	}

	logDir := cfg.Daemon.LogDir
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(s.ProjectDir, logDir)
	}
	if s.WorkspaceID != "" {
		logDir = filepath.Join(logDir, s.WorkspaceID)
	}

	if err := os.MkdirAll(logDir, 0700); err != nil {
		log.Printf("[daemon] Agent %s: failed to create log directory: %v", ap.Entry.Worktree, err)
		return nil
	}

	safeWorktree := filepath.Base(ap.Entry.Worktree)
	safeRole := filepath.Base(ap.Entry.Role)
	if safeRole != ap.Entry.Role {
		log.Printf("[daemon] Agent %s: role name %q sanitized to %q for log path",
			ap.Entry.Worktree, ap.Entry.Role, safeRole)
	}
	logFilePath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", safeRole, safeWorktree))
	ap.LogFilePath = logFilePath

	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304: log file path from daemon config
	if err != nil {
		log.Printf("[daemon] Agent %s: failed to open log file: %v", ap.Entry.Worktree, err)
		return nil
	}
	return f
}

// openAgentArchiveLog opens the per-workspace agent archive that the web UI Logs
// tab reads (resolved by agent.OpenAgentArchiveLog, which mirrors the reader so
// writer and reader always agree). Best-effort: returns nil on any error so a
// logging failure never blocks spawning the agent.
func (s *Supervisor) openAgentArchiveLog(ap *AgentProcess) *os.File {
	f, err := agent.OpenAgentArchiveLog(s.WorkspaceID, ap.Entry.Worktree)
	if err != nil {
		log.Printf("[daemon] Agent %s: archive log unavailable: %v", ap.Entry.Worktree, err)
		return nil
	}
	return f
}

// closeAgentLogs closes both agent log sinks (best-effort) and clears the
// handles. Must be called while ap.Mu is held.
func closeAgentLogs(ap *AgentProcess) {
	if ap.LogFile != nil {
		if err := ap.LogFile.Close(); err != nil {
			log.Printf("[daemon] Agent %s: failed to close log file: %v", ap.Entry.Worktree, err)
		}
		ap.LogFile = nil
	}
	if ap.ArchiveLogFile != nil {
		if err := ap.ArchiveLogFile.Close(); err != nil {
			log.Printf("[daemon] Agent %s: failed to close archive log: %v", ap.Entry.Worktree, err)
		}
		ap.ArchiveLogFile = nil
	}
}

// waitForAgent blocks until subprocess exits, returns exit code.
func (s *Supervisor) waitForAgent(ap *AgentProcess) int {
	ap.Mu.Lock()
	cmd := ap.Cmd
	ap.Mu.Unlock()

	if cmd == nil {
		return -1
	}

	// Keep this supervise goroutine's liveness tick fresh while we block in
	// cmd.Wait() for the agent's unbounded lifetime; see startAgentWaitHeartbeat.
	stopHeartbeat := s.startAgentWaitHeartbeat(ap)
	// Renew the agent's fleet-db worker-registration lease for the same window,
	// so a live agent is not reaped by the server-side TTL while it runs.
	stopWorkerHeartbeat := s.startWorkerHeartbeat(ap)
	err := cmd.Wait()
	stopHeartbeat()
	stopWorkerHeartbeat()

	ap.Mu.Lock()
	ap.LastExit = time.Now()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			ap.LastExitCode = exitErr.ExitCode()
		} else {
			ap.LastExitCode = -1
		}
	} else {
		ap.LastExitCode = 0
	}
	exitCode := ap.LastExitCode
	pid := ap.Pid // capture before clearing
	worktree := ap.Entry.Worktree
	role := ap.Entry.Role
	epicID := ap.AssignedEpicID
	ap.Cmd = nil
	ap.Pid = 0

	// Close log file handles to prevent leaks
	closeAgentLogs(ap)
	ap.Mu.Unlock()

	// Emit agent_stopped event outside the lock (best-effort)
	if evt, err := events.NewEvent(events.AgentStopped, worktree, role, epicID, events.AgentStoppedData{PID: pid, ExitCode: exitCode}); err == nil {
		s.EmitEvent(evt)
	}

	return exitCode
}

// recoverAgent calls RecoverWorktree for cleanup.
// exitCode is passed so recovery can make smarter decisions (e.g. skip task
// reset on clean exit when the task status is already terminal).
func (s *Supervisor) recoverAgent(ap *AgentProcess, exitCode int) error {
	return agent.RecoverWorktree(ap.WorktreePath, ap.Entry.Worktree, exitCode)
}

// appendDaemonEnv appends daemon-level env vars (workspace ID, IPC socket path)
// to the given env slice. Values are only set when non-empty.
func (s *Supervisor) appendDaemonEnv(env []string) []string {
	if s.WorkspaceID != "" {
		env = append(env,
			fmt.Sprintf("LOOM_WORKSPACE=%s", s.WorkspaceID),
			fmt.Sprintf("LOOM_WORKSPACE_ID=%s", s.WorkspaceID),
		)
	}
	if s.IpcSocketPath != "" {
		env = append(env, fmt.Sprintf("LOOM_DAEMON_SOCKET=%s", s.IpcSocketPath))
	}
	return env
}
