package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
)

// defaultNoWorkBackoff matches the supervisor's fallback no-work backoff.
const defaultNoWorkBackoff = 30 * time.Second

// stateFileStaleThreshold is how old daemon-agents.json may be (relative to
// now) before the check flags the state updater as wedged. The state updater
// ticks every 5s; 30s leaves headroom for system pauses.
const stateFileStaleThreshold = 30 * time.Second

// checkDaemonStuck inspects daemon-agents.json against the current daemon
// process. It flags two failure modes that signal a wedged supervisor:
//
//  1. Any agent's backoff_until is in the past by more than 2× no_work_backoff
//     while the daemon process is still alive.
//  2. daemon-agents.json mtime is older than ~30s while the daemon is alive —
//     the 5s state updater goroutine has died.
//
//nolint:funlen // I/O steps are sequential and read top-to-bottom.
func checkDaemonStuck() CheckResult {
	projectDir, err := os.Getwd()
	if err != nil {
		return CheckResult{} // skip — checkLoomDaemon will flag the working-dir issue
	}

	dcfg, cfgErr := cfgpkg.LoadDaemonConfig(projectDir)
	if cfgErr != nil {
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

	data, readErr := os.ReadFile(stateFilePath) //nolint:gosec // stateFilePath is derived from the loom project's own .loom/ directory
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

// evaluateDaemonStuck is the pure analysis used by checkDaemonStuck.
//
//nolint:funlen // The fail/warn/pass branches share the result-building tail.
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
		if agent.LastErrorClass == agenterr.WorkScanFailureOutcome.String() {
			cause := strings.TrimSpace(agent.LastErrorMessage)
			if cause == "" {
				cause = "ready/work scan failed"
			}
			fail = append(fail, fmt.Sprintf("agent %q cannot scan work: %s", agent.Worktree, cause))
		}
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

// resolveNoWorkBackoff returns the configured no-work backoff or the default.
func resolveNoWorkBackoff(dcfg *cfgpkg.DaemonConfig) time.Duration {
	if dcfg != nil && dcfg.Daemon.RestartPolicy.NoWorkBackoff != nil && *dcfg.Daemon.RestartPolicy.NoWorkBackoff > 0 {
		return time.Duration(*dcfg.Daemon.RestartPolicy.NoWorkBackoff) * time.Second
	}
	return defaultNoWorkBackoff
}
