package daemon

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/events/otelexport"
)

// startStateUpdater runs a goroutine that periodically writes the daemon state file.
// Returns a channel that is closed when the updater exits.
func startStateUpdater(shutdown <-chan struct{}, stateFilePath string, startedAt time.Time, daemon *Daemon, maxRetries int) <-chan struct{} {
	done := make(chan struct{})
	daemon.sup.RegisterTick(supervisor.GoroutineStateUpdater)
	go func() {
		defer close(done)
		defer daemon.sup.RecoverAndSignal(supervisor.GoroutineStateUpdater)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-shutdown:
				return
			case <-ticker.C:
				// A failed state write used to be a fmt.Printf on a stdout that,
				// for a daemonized process, may be a closed or redirected fd —
				// so during the 2026-08-31 outage nothing anywhere recorded that
				// the daemon's state file had gone stale. Record it as a
				// first-class degradation instead, and log/publish only on the
				// transitions so a persistently full disk does not emit twelve
				// lines a minute.
				if err := writeStateFile(stateFilePath, startedAt, daemon.Agents(), daemon.ParkedAgents(),
					daemon.QuarantinedTasks(), daemon.sup.Degradations(), maxRetries,
					stateExtras{Hold: daemon.sup.ClaimHoldSnapshot(), Walls: daemon.sup.WallSnapshot()}); err != nil {
					if daemon.sup.RecordDegradation(supervisor.DegradationStateWrite, err) {
						slog.Error("daemon state file write failing", "path", stateFilePath, "err", err)
						daemon.sup.PublishDegradation(supervisor.DegradationStateWrite)
					}
				} else if daemon.sup.ClearDegradation(supervisor.DegradationStateWrite) {
					slog.Info("daemon state file write recovered", "path", stateFilePath)
					daemon.sup.PublishDegradation(supervisor.DegradationStateWrite)
				}
				daemon.sup.RecordTick(supervisor.GoroutineStateUpdater)
			}
		}
	}()
	return done
}

// initOTelExporter initializes the OTel exporter if configured and subscribes it to the bus.
// Returns nil if OTel is not enabled or initialization fails.
func initOTelExporter(config *config.DaemonConfig, eventBus *events.Bus) *otelexport.Exporter {
	if config.Daemon.OTel == nil || !config.Daemon.OTel.Enabled {
		return nil
	}

	otelCfg := otelexport.Config{
		Enabled:         true,
		Endpoint:        config.Daemon.OTel.Endpoint,
		Protocol:        config.Daemon.OTel.Protocol,
		ServiceName:     config.Daemon.OTel.ServiceName,
		SampleRate:      config.Daemon.OTel.SampleRate,
		FlushIntervalMs: config.Daemon.OTel.FlushIntervalMs,
		Traces:          config.Daemon.OTel.Traces,
		Metrics:         config.Daemon.OTel.Metrics,
	}.Resolved()

	exp, err := otelexport.New(otelCfg)
	if err != nil {
		log.Printf("warning: failed to initialize OTel exporter: %v (continuing without OTel)", err)
		return nil
	}

	eventBus.Subscribe(exp.HandleEvent)
	log.Printf("OTel exporter initialized: endpoint=%s traces=%v metrics=%v",
		otelCfg.Endpoint, otelCfg.TracesEnabled(), otelCfg.MetricsEnabled())
	return exp
}

// stopOTelExporter gracefully shuts down the OTel exporter with a timeout.
func stopOTelExporter(exp *otelexport.Exporter) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exp.Stop(ctx); err != nil {
		log.Printf("warning: OTel shutdown error: %v", err)
	}
}

// printDryRunInfo displays what would happen in dry-run mode.
// NOTE: If config.DaemonSettings gains secret fields (RedisURL),
// their values should be masked via config.SecretResolver.MaskSecrets() before printing.
func printDryRunInfo(config *config.DaemonConfig, pidFile, logDir, stateFile, spawnMetricsFile string) {
	fmt.Println("DRY RUN - No daemon will be started")
	fmt.Println("")
	fmt.Println("Configuration:")
	fmt.Printf("  PID file: %s\n", pidFile)
	fmt.Printf("  State file: %s\n", stateFile)
	fmt.Printf("  Log directory: %s\n", logDir)
	fmt.Printf("  Spawn metrics file: %s\n", spawnMetricsFile)
	if config.Daemon.RestartPolicy.MaxRetries != nil {
		fmt.Printf("  Max retries: %d\n", *config.Daemon.RestartPolicy.MaxRetries)
	} else {
		fmt.Printf("  Max retries: 3 (default)\n")
	}
	if config.Daemon.RestartPolicy.BackoffInitial != nil {
		fmt.Printf("  Backoff initial: %ds\n", *config.Daemon.RestartPolicy.BackoffInitial)
	} else {
		fmt.Printf("  Backoff initial: 2s (default)\n")
	}
	if config.Daemon.RestartPolicy.BackoffMax != nil {
		fmt.Printf("  Backoff max: %ds\n", *config.Daemon.RestartPolicy.BackoffMax)
	} else {
		fmt.Printf("  Backoff max: 300s (default)\n")
	}
	if config.Daemon.MaxAgents != nil {
		fmt.Printf("  Max agents: %d\n", *config.Daemon.MaxAgents)
	} else {
		fmt.Printf("  Max agents: 20 (default)\n")
	}
	fmt.Println("")
	fmt.Println("Agents to supervise:")
	for _, a := range config.Agents {
		fmt.Printf("  - %s (role: %s, auto: %v)\n", a.Worktree, a.Role, a.Auto)
	}
	fmt.Println("")
	fmt.Println("Recommended systemd resource controls:")
	fmt.Println("  LimitNOFILE=65536      # file descriptor limit")
	fmt.Println("  MemoryMax=4G           # memory ceiling")
	fmt.Println("  CPUQuota=200%          # CPU limit (200% = 2 cores)")
	fmt.Println("  TasksMax=256           # max tasks (processes+threads)")
}

// ResolveDaemonPath delegates to supervisor.ResolveDaemonPath for daemon callers.
func ResolveDaemonPath(projectDir, path string) string {
	return supervisor.ResolveDaemonPath(projectDir, path)
}

// ---------------------------------------------------------------------------
// Supervisor environment hygiene: inherited-agent-identity guard and session scrub
// ---------------------------------------------------------------------------
// The supervisor is a fleet-level process: it owns every agent, and it has no
// agent identity of its own. When an agent restarts the daemon from inside its
// own session (a `pm2 restart`, or a `pm2 start` under a Claude Code agent's
// shell), the process manager captures that agent's whole environment as the
// daemon's app definition and the supervisor comes up wearing worker-N's
// identity. Measured 2026-08-27: the daemon then read role identity from that
// environment, claimed nothing for ~4 hours while five ready P1 tasks sat
// unclaimed, heartbeat the agent's dead lease into HTTP 410s, and carried a
// foreign workspace's CLAUDE_CONFIG_DIR. Nothing about it self-heals, because
// the poisoned definition is what a re-register copies forward.
//
// Two defenses live here, and they are deliberately different in kind:
//
//   - agentIdentityEnvNames are markers a supervisor can never legitimately
//     hold. Seeing one means the process was started from an agent session, so
//     the daemon refuses to boot and says how to clear it. Silently continuing
//     is what cost four hours.
//   - agentSessionEnvNames are session-scoped leftovers that are merely wrong
//     to inherit. They are scrubbed from the daemon's own process environment
//     at boot, which also keeps them out of every spawned agent: agent
//     subprocess environments are built from cli.FilteredEnv(), i.e. from this
//     process's environment.

// agentIdentityEnvNames are the variables whose presence proves the daemon
// inherited an agent's environment. Their presence is fatal, not scrubbable:
// an operator needs to fix the process definition, and a scrub would hide it.
var agentIdentityEnvNames = []string{
	"LOOM_AGENT_NAME",
	"LOOM_ASSIGNED_TASK_ID",
}

// agentSessionEnvNames are per-session variables the supervisor must not carry
// or hand to its children. LOOM_WORKSPACE, LOOM_SERVER_URL, LOOM_FLEET_DB_* and
// LOOM_DAEMON_* are intentionally absent — those are the supervisor's own
// configuration.
var agentSessionEnvNames = []string{
	"LOOM_AGENT_LEASE_ID",
	"LOOM_AGENT_PATH_PATTERNS",
	"LOOM_AGENT_REPO",
	"LOOM_ROLE",
	"LOOM_ROLE_EXECUTOR",
	"LOOM_ROLE_INPUT_POLICY",
	"LOOM_ROLE_LABELS",
	"LOOM_ROLE_MAX_PRIORITY",
	"LOOM_ROLE_PATH_PATTERNS",
	"LOOM_ROLE_SKILLS",
	"LOOM_ROLE_TASK_FILTER",
	"LOOM_SESSION_ID",
	"LOOM_SOURCE_REPOS",
	"LOOM_TRACE_PARENT",
	"LOOM_WORKTREE_PATH",
	"LOOM_YIELD_FILE",
	// claude-code's per-session bookkeeping. CLAUDE_CONFIG_DIR is included on
	// purpose: inherited, it points the supervisor (and every agent it spawns)
	// at one agent's profile — cross-workspace credential contamination, and a
	// direct violation of the per-agent-profile invariant. Agent profile dirs
	// are set per agent at spawn, never inherited.
	//
	// CLAUDE_CODE_OAUTH_TOKEN and ANTHROPIC_API_KEY are deliberately NOT here:
	// they are machine-level credentials the supervisor must pass on, not
	// session state.
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CONFIG_DIR",
	"CLAUDE_PID",
}

// agentSessionEnvPrefixes catch the same class of variable when a new one is
// added upstream (CLAUDE_CODE_SSE_PORT and friends). Kept narrow so the
// credential names above are not swept up by a broad "CLAUDE_" match.
var agentSessionEnvPrefixes = []string{
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_SESSION",
	"LOOM_AGENT_LEASE",
	"LOOM_ROLE_",
}

// applySupervisorEnvHygiene refuses an inherited agent identity and scrubs
// inherited per-session variables from this process's environment. Called once,
// first thing in the daemon's boot path.
func applySupervisorEnvHygiene() error {
	if err := checkSupervisorEnv(os.LookupEnv); err != nil {
		return err
	}
	if scrubbed := scrubSupervisorEnv(); len(scrubbed) > 0 {
		slog.Warn("daemon: scrubbed inherited agent-session environment",
			"vars", strings.Join(scrubbed, ", "))
	}
	return nil
}

// supervisorEnvOK runs the hygiene check and reports the failure on stderr.
// Split from applySupervisorEnvHygiene so the boot path stays a single branch.
func supervisorEnvOK() bool {
	if err := applySupervisorEnvHygiene(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}
	return true
}

// checkSupervisorEnv reports an error when the daemon's environment carries an
// agent identity. lookup is os.LookupEnv in production and a stub in tests.
func checkSupervisorEnv(lookup func(string) (string, bool)) error {
	var found []string
	for _, name := range agentIdentityEnvNames {
		if v, ok := lookup(name); ok && strings.TrimSpace(v) != "" {
			found = append(found, fmt.Sprintf("%s=%s", name, strings.TrimSpace(v)))
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf(
		"refusing to start: this supervisor's environment carries an agent identity (%s).\n"+
			"A daemon started from inside an agent session inherits that agent's identity and\n"+
			"silently supervises nothing. Restarting or re-registering will not clear it — the\n"+
			"process manager stores the polluted environment in the app definition.\n"+
			"Fix: delete and recreate the process from a shell with no LOOM_AGENT_*/\n"+
			"LOOM_ASSIGNED_TASK_ID set (e.g. `pm2 delete <app> && pm2 start ...`)",
		strings.Join(found, ", "))
}

// scrubSupervisorEnv unsets inherited per-session variables from this process's
// environment and returns the names it removed, sorted, for logging. Runs after
// checkSupervisorEnv, so the fatal markers have already been refused.
func scrubSupervisorEnv() []string {
	var removed []string
	for _, entry := range os.Environ() {
		idx := strings.IndexByte(entry, '=')
		if idx < 0 {
			continue
		}
		name := entry[:idx]
		if !isAgentSessionEnv(name) {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			continue
		}
		removed = append(removed, name)
	}
	sort.Strings(removed)
	return removed
}

func isAgentSessionEnv(name string) bool {
	for _, n := range agentSessionEnvNames {
		if name == n {
			return true
		}
	}
	for _, p := range agentSessionEnvPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
