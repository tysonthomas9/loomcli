package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/hookcfg"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
	"github.com/tysonthomas9/loomcli/internal/skillmat"

	"go.opentelemetry.io/otel/attribute"
)

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
	// Both ends of a human answer wait need the same clock: the child's ask
	// deadline runs slightly inside this bound so an unanswered prompt ends in
	// the child's clean decline, never the watchdog's kill.
	cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_INPUT_WAIT_MAX_SECONDS=%d", s.GetInputWaitMax()))

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

	if cmd.Env, err = s.appendRuntimeEnv(cmd.Env, ap); err != nil {
		return nil, err
	}

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
	// The input policy is structured, so unlike the tool lists it travels as
	// JSON in one variable. Nothing is exported for a nil policy: the leaf
	// reads an absent variable as deny-everything, which is the same state, so
	// an empty export would only add a way for the two to disagree. An encode
	// failure is logged and skipped rather than failing the spawn — the agent
	// then runs under the deny-everything default, which is the restrictive
	// direction, and refusing to spawn over a serialization bug would take the
	// fleet down for a knob that is failing safe.
	if policy, err := domain.EncodeRoleInputPolicy(ap.RoleConfig.InputPolicy); err != nil {
		log.Printf("[daemon] Agent %s: input_policy not exported (%v) — the agent will deny every harness prompt",
			ap.Entry.Worktree, err)
	} else if policy != "" {
		env = append(env, fmt.Sprintf("LOOM_ROLE_INPUT_POLICY=%s", policy))
	}
	if ap.RoleConfig.MaxBudgetUSD != nil {
		env = append(env, fmt.Sprintf("LOOM_MAX_BUDGET_USD=%.2f", *ap.RoleConfig.MaxBudgetUSD))
	}
	if ap.RoleConfig.Executor != "" {
		env = append(env, fmt.Sprintf("LOOM_ROLE_EXECUTOR=%s", ap.RoleConfig.Executor))
	}
	if ap.RoleConfig.Effort != "" {
		env = append(env,
			fmt.Sprintf("LOOM_AGENT_EFFORT=%s", ap.RoleConfig.Effort),
			fmt.Sprintf("LOOM_CLAUDE_EFFORT=%s", ap.RoleConfig.Effort),
		)
	}
	if ap.RoleConfig.Model != "" {
		// Consumed by resolveAgentModel(): claude TurnConfig.Model /
		// --model, codex -c model=, opencode --model fallback. Without this
		// the role's model field was stored and displayed but never reached
		// any backend.
		env = append(env, fmt.Sprintf("LOOM_AGENT_MODEL=%s", ap.RoleConfig.Model))
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
	if err := s.materializeSkills(ap); err != nil {
		if skillmat.IsStoreUnavailable(err) {
			slog.Warn("skill store unavailable; continuing with existing materialization",
				"worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "err", err)
		} else {
			recordErr(span, err, "spawn.materialize_skills")
			return fmt.Errorf("materialize skills: %w", err)
		}
	}
	s.ensureHookConfig(ap)

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

func (s *Supervisor) ensureHookConfig(ap *AgentProcess) {
	backend := s.GetEffectiveBackend(ap)
	if err := hookcfg.EnsureSkillMaterializeHook(ap.WorktreePath, backend); err != nil {
		slog.Warn("agent hook configuration failed; continuing without raw-PTY pre-turn hook",
			"worktree", ap.Entry.Worktree, "backend", backend, "err", err)
	}
}

func (s *Supervisor) materializeSkills(ap *AgentProcess) error {
	if s.WorkspaceID == "" {
		return nil
	}
	if s.ControlStore == nil {
		slog.Warn("skill store is not configured; continuing without skill materialization",
			"worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID)
		return nil
	}
	ctx, cancel := context.WithTimeout(cmdstore.RootContext(), controlPlaneOperationTimeout)
	defer cancel()
	return skillmat.MaterializeLeased(ctx, s.ControlStore, s.WorkspaceID, ap.Entry.Role, ap.WorktreePath)
}

// materializeIdleSkills keeps an idle worker's worktree current while the
// ready queue is empty: skills edited between spawns converge within one
// no_work_backoff instead of waiting for the next claim. Only the no-work
// loop qualifies — other pre-flight failures (backend gates) skip it.
// Failures only log; the next spawn's materializeSkills call is the
// enforcing one.
func (s *Supervisor) materializeIdleSkills(ap *AgentProcess) {
	ap.Mu.Lock()
	noWork := ap.LastNoWork
	ap.Mu.Unlock()
	if !noWork {
		return
	}
	if err := s.materializeSkills(ap); err != nil {
		slog.Warn("idle skill materialization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
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
		// Both sinks are unavailable, so the child's stdout and stderr go
		// nowhere: cmd.Stdout/Stderr stay nil, which os/exec wires to
		// /dev/null. That is survivable but it must not be silent — with no
		// child output there is no way to tell a working agent from one that
		// printed an error and exited, which is exactly the state that made a
		// real "every agent exits 0 doing nothing" bug undiagnosable. The two
		// openers each log their own reason above; this says what the
		// combination costs.
		log.Printf("[daemon] Agent %s: NO LOG SINK — the agent's output is being discarded; "+
			"set daemon.log_dir or fix the agent archive to make this run diagnosable",
			ap.Entry.Worktree)
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
// reset on clean exit when the task status is already terminal). incomplete
// splits the exit-0 case further: an unfinished turn requeues its task but
// keeps the worktree intact for the next attempt. It is an explicit argument
// rather than a read of ap.LastError because the pre-flight caller runs BEFORE
// this cycle classifies anything — it would otherwise inherit the previous
// cycle's verdict and skip the cold-start cleanup it exists to perform.
func (s *Supervisor) recoverAgent(ap *AgentProcess, exitCode int, incomplete bool) error {
	return agent.RecoverWorktree(ap.WorktreePath, ap.Entry.Worktree, exitCode, incomplete)
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

// AgentProfilesDirName is the workspace-relative root holding per-agent
// harness profile directories: .loom/agent-profiles/<worktree>/{claude,codex}.
// When a backend subdirectory exists for an agent, the supervisor exports the
// matching harness config-root variable (CLAUDE_CONFIG_DIR / CODEX_HOME) into
// the agent process. envfilter allowlists both names, so the value flows
// unchanged through cli.FilteredEnv() into the harness child built by the
// backends layer — no change is needed there.
//
// Directory existence is the whole contract: there is no config key and no
// flag, so the same layout works unchanged inside a container image.
//
// It is an alias, not a second literal: agentprofile owns the layout, and the
// readers (transcript mirroring, `loom doctor`) resolve it from there.
const AgentProfilesDirName = agentprofile.DirName

// appendRuntimeEnv appends the per-run environment an agent subprocess needs:
// the daemon's control-plane wiring, its verified harness profile roots, the
// yield file it watches, and its session identity. It is the one step of
// buildCommand that can fail, because an agent whose profile does not verify
// must not boot at all — see AppendProfileEnv.
func (s *Supervisor) appendRuntimeEnv(env []string, ap *AgentProcess) ([]string, error) {
	env = s.appendDaemonEnv(env)
	env, err := AppendProfileEnv(env, s.ProjectDir, ap.Entry.Worktree)
	if err != nil {
		return nil, fmt.Errorf("agent %s profile: %w", ap.Entry.Worktree, err)
	}
	env = append(env, fmt.Sprintf("LOOM_YIELD_FILE=%s", filepath.Join(ap.WorktreePath, YieldFileName)))
	return appendSessionEnv(env, ap), nil
}

// ProfileManifestName is the launch-verification manifest a provisioned
// profile root carries. Format and fingerprint scheme are documented on
// agentprofile.ManifestName, which owns the verification.
const ProfileManifestName = agentprofile.ManifestName

// Boot-refusal reasons, distinguished so an operator reading the agent's
// failure knows which repair applies: re-provision (stale fingerprint),
// re-bless the upgrade (version drift), or provision at all (no manifest).
// They are aliases of the agentprofile sentinels, so errors.Is works across
// both packages.
var (
	ErrProfileManifestMissing     = agentprofile.ErrManifestMissing
	ErrProfileManifestUnreadable  = agentprofile.ErrManifestUnreadable
	ErrProfileFingerprintMismatch = agentprofile.ErrFingerprintMismatch
	ErrProfileVersionDrift        = agentprofile.ErrVersionDrift
	ErrProfileVersionUnknown      = agentprofile.ErrVersionUnknown
)

// ErrProfileTokenUnreadable is deliberately NOT an agentprofile alias: the
// credential file is the supervisor's concern, not the manifest's — the
// manifest does not describe it, so agentprofile has no counterpart to alias.
// Keep it here rather than "tidying" it into agentprofile.
var ErrProfileTokenUnreadable = errors.New("profile harness token unreadable")

// profileHarnessEnvVar maps a profile harness root to the environment variable
// that points the harness at it. Together with agentprofile.HarnessBinary this
// is the whole export vocabulary; a new harness is one entry in each map.
var profileHarnessEnvVar = map[string]string{
	"claude": "CLAUDE_CONFIG_DIR",
	"codex":  "CODEX_HOME",
}

// profileHarnesses is the fixed order profile roots are resolved in, so an
// agent's environment is byte-identical from one boot to the next.
var profileHarnesses = []string{"claude", "codex"}

// profileTokenFile names the file inside a harness profile root that carries
// that profile's OWN long-lived credential, and profileTokenEnvVar the
// variable exporting it. Only claude has one: `claude setup-token` mints a
// per-invocation, non-rotating token and prints it instead of writing a
// credentials file, so the operator's setup-profile-token.sh captures it to
// <root>/claude/oauth-token (mode 600). codex has no equivalent, and a harness
// absent from these maps simply gets no credential injected.
//
// This is what makes a profile an IDENTITY rather than a copy of one. The
// keychain-copy fallback shares the operator's own OAuth pair across every
// profile, and the operator's next /login refresh invalidates it for whichever
// profile copied it last — the "Login expired" the agents kept hitting on an
// uncontrolled schedule. A profile carrying its own token is unaffected by
// anyone else's refresh.
//
// The token file is deliberately NOT in the manifest's file list: that list is
// an allowlist of files the fingerprint covers, and a credential must not be
// hashed into a value that is written down, compared and reported.
var (
	profileTokenFile = map[string]string{
		"claude": "oauth-token",
	}
	profileTokenEnvVar = map[string]string{
		"claude": "CLAUDE_CODE_OAUTH_TOKEN",
	}
)

// ProfileHarnesses returns the harnesses a profile root can be provisioned
// for. Callers that inject one harness at a time (`loom lead`) iterate this
// rather than writing their own list, which is how the two would drift.
func ProfileHarnesses() []string {
	return append([]string(nil), profileHarnesses...)
}

// ProfileHarnessBinary returns the binary whose --version output a harness
// profile's manifest pins, or "" for an unknown harness. Exported so a caller
// verifying a root outside the spawn path resolves the same binary the spawn
// path would, and so the provisioner's pin can be asserted against it. The
// table itself lives in agentprofile, which owns verification.
func ProfileHarnessBinary(harness string) string {
	return agentprofile.HarnessBinary[harness]
}

// ProfileEnvVar returns the environment variable a harness profile root is
// exported as, or "" for an unknown harness. It is exported so a caller can
// tell whether a variable is ALREADY set before paying for verification —
// `loom lead` must leave an inherited value alone, including an operator's own
// config root that no manifest here could ever verify.
func ProfileEnvVar(harness string) string {
	return profileHarnessEnvVar[harness]
}

// ProfileHarnessEnv resolves one harness profile root for an agent, verifies
// it, and returns the KEY=VALUE assignment that exports it — or "" when the
// agent has no such root on disk.
//
// This is the single implementation of the resolve-verify-export policy. The
// supervisor reaches it through AppendProfileEnv at spawn; `loom lead`, the one
// agent the supervisor does not spawn, calls it per harness so it can skip the
// ones whose variable it inherited. Neither may grow a second, weaker copy.
//
// An existing but unverifiable profile is a BOOT FAILURE, never a fallback to
// legacy env: silently running the agent against the operator's full ~/.claude
// is the exact leak per-agent profiles close. Per-agent boot degradation
// contains the failure to the one agent whose profile is broken.
func ProfileHarnessEnv(projectDir, agent, harness string) (string, []string, error) {
	root := agentprofile.Dir(projectDir, agent)
	if root == "" {
		// No resolvable profile root (empty or non-segment agent name): the
		// same situation as no profile on disk, so stay on the legacy env.
		return "", nil, nil
	}
	envVar := profileHarnessEnvVar[harness]
	if envVar == "" {
		return "", nil, nil
	}
	dir := filepath.Join(root, harness)
	if !dirExists(dir) {
		return "", nil, nil
	}
	if err := verifyProfileManifest(dir, agentprofile.HarnessBinary[harness]); err != nil {
		return "", nil, err
	}
	env := []string{fmt.Sprintf("%s=%s", envVar, dir)}
	secret, err := ProfileSecretEnv(dir, harness)
	if err != nil {
		return dir, nil, err
	}
	return dir, append(env, secret...), nil
}

// ProfileSecretEnv returns the assignments exporting the credential a harness
// profile root carries of its own, or nothing when it carries none — which is
// every profile that has not been migrated to a setup-token identity yet, and
// every harness that has no such file at all. Absent is not an error: it is
// the pre-existing configuration, and it must keep working unchanged.
//
// It is exported for `loom lead`, the one agent the supervisor does not spawn,
// which may INHERIT its config root and so never reach ProfileHarnessEnv —
// but must still pick up that root's credential rather than run on whatever
// token the operator's shell happened to hold.
//
// Neither the token nor any prefix of it appears in the returned error, and it
// is never logged: the only place the value may go is the child's environment.
func ProfileSecretEnv(dir, harness string) ([]string, error) {
	name, envVar := profileTokenFile[harness], profileTokenEnvVar[harness]
	if name == "" || envVar == "" || dir == "" {
		return nil, nil
	}
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path derived from the workspace profile layout, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrProfileTokenUnreadable, path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		// Present but empty is a broken provisioning run, not a legacy
		// profile: falling through to the operator's token would restore the
		// exact sharing this file exists to end, silently.
		return nil, fmt.Errorf("%w: %s: file is empty", ErrProfileTokenUnreadable, path)
	}
	return []string{fmt.Sprintf("%s=%s", envVar, token)}, nil
}

// AppendProfileEnv injects every per-agent harness profile root that exists on
// disk, after verifying each one against its manifest, together with any
// credential that root carries of its own. Absent directories leave the
// environment untouched, preserving the legacy behavior of inheriting the
// operator's ~/.claude and ~/.codex.
//
// The profile's assignments are appended LAST, so a profile token overrides an
// operator token the filtered environment carried in — the allowlist passes
// CLAUDE_CODE_OAUTH_TOKEN through, and exec resolves duplicates to the final
// assignment.
func AppendProfileEnv(env []string, projectDir, agent string) ([]string, error) {
	for _, harness := range profileHarnesses {
		_, assignments, err := ProfileHarnessEnv(projectDir, agent, harness)
		if err != nil {
			return nil, err
		}
		env = append(env, assignments...)
	}
	return env, nil
}

// VerifyProfileManifest applies the spawn path's verify-or-refuse rule to a
// profile root for a caller outside the daemon. `loom lead` is the one agent
// the supervisor does not spawn — the workspace launcher exports
// CLAUDE_CONFIG_DIR itself — so it must reuse this check rather than grow a
// second, weaker policy alongside it.
func VerifyProfileManifest(dir, binary string) error {
	return verifyProfileManifest(dir, binary)
}

// verifyProfileManifest verifies dir against its manifest, supplying the
// observed harness version from this package's TTL cache. binary selects which
// cached probe to use; the verification itself lives in agentprofile.
func verifyProfileManifest(dir, binary string) error {
	return agentprofile.Verify(dir, harnessVersion(binary))
}

// harnessVersionTTL bounds how long a probed --version string is reused. It is
// deliberately coarse: the point is that one spawn cycle — every agent the
// supervisor brings up in a burst — costs a single probe per binary rather
// than one per agent, each of which forks a node CLI and can cost seconds.
// A harness upgrade lands within a TTL, and the next boot re-probes.
const harnessVersionTTL = 2 * time.Minute

var (
	harnessVersionMu    sync.Mutex
	harnessVersionCache = map[string]harnessVersionEntry{}
)

type harnessVersionEntry struct {
	version string
	probed  time.Time
}

// harnessVersion returns the cached "<binary> --version" first line, probing
// at most once per binary per TTL. Failures are NOT cached: a probe killed
// under load would otherwise refuse every agent boot for the whole TTL.
func harnessVersion(binary string) string {
	harnessVersionMu.Lock()
	if e, ok := harnessVersionCache[binary]; ok && time.Since(e.probed) < harnessVersionTTL {
		harnessVersionMu.Unlock()
		return e.version
	}
	harnessVersionMu.Unlock()

	version := probeHarnessVersion(binary)
	if version == "" {
		return ""
	}
	harnessVersionMu.Lock()
	harnessVersionCache[binary] = harnessVersionEntry{version: version, probed: time.Now()}
	harnessVersionMu.Unlock()
	return version
}

// ResetHarnessVersionCache drops every cached probe. For testing only: a test
// that shims a harness on PATH must not inherit a version another test — or
// the enforcement `loom lead` now runs at startup — already probed off the
// real binary.
func ResetHarnessVersionCache() {
	harnessVersionMu.Lock()
	harnessVersionCache = map[string]harnessVersionEntry{}
	harnessVersionMu.Unlock()
}

// probeHarnessVersion is a seam for tests; production runs the real binary.
var probeHarnessVersion = func(binary string) string {
	return agentprofile.ProbeVersion(binary, backends.VersionProbeTimeout)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
