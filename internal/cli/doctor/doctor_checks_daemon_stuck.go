package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
)

// defaultNoWorkBackoff matches supervisor.getNoWorkBackoff()'s fallback. The
// check uses this when the loaded config does not specify NoWorkBackoff.
const defaultNoWorkBackoff = 30 * time.Second

// stateFileStaleThreshold is how old daemon-agents.json may be (relative to
// now) before the check flags the state updater as wedged. The state updater
// ticks every 5s; 30s leaves headroom for system pauses but is well below any
// reasonable user-visible "the daemon is broken" delay.
const stateFileStaleThreshold = 30 * time.Second

// checkDaemonStuck inspects daemon-agents.json against the current daemon
// process. It flags two failure modes that signal a wedged supervisor:
//
//  1. Any agent's backoff_until is in the past by more than 2× no_work_backoff
//     while the daemon process is still alive — the freeze signature observed
//     on 2026-05-11 (backoff_until pinned to 07:42 hours after expiry).
//  2. daemon-agents.json mtime is older than ~30s while the daemon is alive —
//     the 5s state updater goroutine has died.
//
// Both conditions return StatusFail because they're correlated with user-
// visible loss of supervision (no restarts, no events). The check is silent
// (no result row) when the daemon is not running — checkLoomDaemon already
// covers that path.
//
//nolint:funlen // I/O steps (cwd → config → PID → state file → unmarshal) are sequential and read top-to-bottom; splitting would hide the failure ordering.
func checkDaemonStuck() CheckResult {
	projectDir, err := os.Getwd()
	if err != nil {
		return CheckResult{} // skip — checkLoomDaemon will flag the working-dir issue
	}

	dcfg, cfgErr := cfgpkg.LoadDaemonConfig(projectDir)
	if cfgErr != nil {
		// Fall back to defaults — we can still inspect the state file.
		dcfg = &cfgpkg.DaemonConfig{
			Daemon: cfgpkg.DaemonSettings{PIDFile: ".loom/daemon.pid"},
		}
	}

	pidFilePath := daemon.ResolveDaemonPath(projectDir, dcfg.Daemon.PIDFile)
	_, running := daemon.IsLoomDaemonRunning(pidFilePath)
	if !running {
		return CheckResult{} // checkLoomDaemon covers stale PID / not running
	}

	stateFilePath := cfgpkg.ResolveDaemonStatePath(projectDir)
	stateInfo, statErr := os.Stat(stateFilePath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return CheckResult{
				Name:    "daemon_stuck",
				Status:  StatusWarn,
				Summary: "daemon running but state file missing",
				Detail:  fmt.Sprintf("expected: %s", stateFilePath),
			}
		}
		return CheckResult{
			Name:    "daemon_stuck",
			Status:  StatusWarn,
			Summary: "could not stat daemon state file",
			Detail:  statErr.Error(),
		}
	}

	data, readErr := os.ReadFile(stateFilePath) //nolint:gosec // stateFilePath is derived from the loom project's own .loom/ directory, not user input
	if readErr != nil {
		return CheckResult{
			Name:    "daemon_stuck",
			Status:  StatusWarn,
			Summary: "could not read daemon state file",
			Detail:  readErr.Error(),
		}
	}
	var state daemon.DaemonState
	if jsonErr := json.Unmarshal(data, &state); jsonErr != nil {
		return CheckResult{
			Name:    "daemon_stuck",
			Status:  StatusWarn,
			Summary: "could not parse daemon state file",
			Detail:  jsonErr.Error(),
		}
	}

	return evaluateDaemonStuck(state, stateInfo.ModTime(), time.Now(),
		resolveNoWorkBackoff(dcfg))
}

// evaluateDaemonStuck is the pure analysis used by checkDaemonStuck. Splitting
// the file I/O from the policy lets tests drive the check with synthetic
// state without writing temp files.
//
//nolint:funlen // The fail/warn/pass branches share the result-building tail; splitting them obscures the threshold ladder that the check enforces.
func evaluateDaemonStuck(state daemon.DaemonState, stateMtime, now time.Time, noWorkBackoff time.Duration) CheckResult {
	if noWorkBackoff <= 0 {
		noWorkBackoff = defaultNoWorkBackoff
	}
	fatalThreshold := 2 * noWorkBackoff

	var fail []string
	var warn []string

	// State file mtime check — the 5s state updater goroutine should keep
	// this fresh. A stale mtime while the daemon is alive means the updater
	// died (panic) or is blocked.
	mtimeAge := now.Sub(stateMtime)
	if mtimeAge > stateFileStaleThreshold {
		fail = append(fail, fmt.Sprintf("daemon-agents.json mtime is %s old (state updater wedged)",
			mtimeAge.Truncate(time.Second)))
	}

	// Per-agent backoff_until staleness.
	for _, agent := range state.Agents {
		if agent.BackoffUntil.IsZero() {
			continue
		}
		lateness := now.Sub(agent.BackoffUntil)
		if lateness <= 0 {
			continue // still legitimately in backoff
		}
		switch {
		case lateness > fatalThreshold:
			fail = append(fail, fmt.Sprintf("agent %q backoff_until is %s in the past (>2× no_work_backoff=%s)",
				agent.Worktree, lateness.Truncate(time.Second), noWorkBackoff))
		case lateness > noWorkBackoff:
			warn = append(warn, fmt.Sprintf("agent %q backoff_until is %s in the past (>1× no_work_backoff=%s)",
				agent.Worktree, lateness.Truncate(time.Second), noWorkBackoff))
		}
	}

	if len(fail) > 0 {
		details := append([]string{}, fail...)
		details = append(details, warn...)
		details = append(details, "Inspect logs around the freeze; restart with: kill -TERM <pid> && loom daemon")
		return CheckResult{
			Name:    "daemon_stuck",
			Status:  StatusFail,
			Summary: fmt.Sprintf("daemon supervisor appears stuck (%d critical signal(s))", len(fail)),
			Detail:  strings.Join(details, "\n"),
		}
	}
	if len(warn) > 0 {
		details := append([]string{}, warn...)
		details = append(details, "Watch for escalation if these persist past 2× no_work_backoff.")
		return CheckResult{
			Name:    "daemon_stuck",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("daemon supervisor backoff slipping (%d agent(s))", len(warn)),
			Detail:  strings.Join(details, "\n"),
		}
	}

	return CheckResult{
		Name:    "daemon_stuck",
		Status:  StatusPass,
		Summary: "daemon supervisor liveness OK",
	}
}

// resolveNoWorkBackoff mirrors supervisor.getNoWorkBackoff()'s priority order
// (config override → default). Kept local so the doctor check does not import
// the supervisor package just for one default constant.
func resolveNoWorkBackoff(dcfg *cfgpkg.DaemonConfig) time.Duration {
	if dcfg != nil && dcfg.Daemon.RestartPolicy.NoWorkBackoff != nil && *dcfg.Daemon.RestartPolicy.NoWorkBackoff > 0 {
		return time.Duration(*dcfg.Daemon.RestartPolicy.NoWorkBackoff) * time.Second
	}
	return defaultNoWorkBackoff
}
