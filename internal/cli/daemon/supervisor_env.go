package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
)

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
