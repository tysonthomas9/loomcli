package doctor

import (
	"fmt"
	"os"
	"strings"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/daemonlog"
)

// daemonLogWarnAge / daemonLogFailAge bound how long the daemon's own log may
// go unwritten while the process is alive. The health checker emits a
// heartbeat line every 60s (log_heartbeat_sec), so 2m is two missed beats and
// 5m is five — generous enough that a laptop resumed from suspend clears the
// condition on the next beat instead of latching a failure.
const (
	daemonLogWarnAge = 2 * time.Minute
	daemonLogFailAge = 5 * time.Minute
)

// daemonLogRemediation is the operator's next step when daemon.log has gone
// silent under a live daemon.
const daemonLogRemediation = "check free disk space; inspect the node's degraded: labels; " +
	"restart with: kill -TERM <pid> && loom daemon"

// checkDaemonLogging verifies that a running daemon is still writing to the
// log file it owns. It is the log-side twin of checkDaemonStuck: that check
// watches daemon-agents.json (the state updater), this one watches daemon.log
// (the health checker's heartbeat). A daemon can lose one without the other,
// and knowing which is broken is the whole point of running both.
func checkDaemonLogging() CheckResult {
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

	workspaceID := ""
	if ws, wsErr := cfgpkg.ResolveActiveWorkspace(); wsErr == nil && ws != nil {
		workspaceID = ws.ID
	}

	logPath := daemonlog.DaemonLogPath(projectDir, dcfg, workspaceID)
	logInfo, statErr := os.Stat(logPath) //nolint:gosec // logPath is derived from the loom project's own config

	// The state file is read only as a discriminator: a fresh one alongside a
	// stale log says the daemon is supervising fine and the log path itself is
	// what broke.
	stateInfo, _ := os.Stat(cfgpkg.ResolveDaemonStatePath(projectDir))

	return evaluateDaemonLogging(logPath, logInfo, stateInfo, statErr, time.Now())
}

// evaluateDaemonLogging is the pure analysis used by checkDaemonLogging.
// logInfo may be nil when statErr is non-nil; stateInfo may be nil when the
// state file is absent or unreadable.
func evaluateDaemonLogging(logPath string, logInfo, stateInfo os.FileInfo, statErr error, now time.Time) CheckResult {
	if statErr != nil {
		return daemonLogStatFailure(logPath, statErr)
	}

	age := now.Sub(logInfo.ModTime()).Truncate(time.Second)
	if age < daemonLogWarnAge {
		return CheckResult{
			Name:    "daemon_logging",
			Status:  StatusPass,
			Summary: "daemon log is current",
		}
	}

	details := []string{
		fmt.Sprintf("path: %s", logPath),
		fmt.Sprintf("last written: %s ago", age),
	}
	if stateFileIsFresh(stateInfo, now) {
		details = append(details,
			"state file is current — the log path specifically is broken, not the daemon")
	}
	details = append(details, daemonLogRemediation)

	status := StatusWarn
	summary := fmt.Sprintf("daemon.log has not been written for %s", age)
	if age > daemonLogFailAge {
		status = StatusFail
		summary = fmt.Sprintf("daemon alive but daemon.log has not been written for %s", age)
	}

	return CheckResult{
		Name:    "daemon_logging",
		Status:  status,
		Summary: summary,
		Detail:  strings.Join(details, "\n"),
	}
}

// daemonLogStatFailure renders the two ways stat(daemon.log) can fail. Neither
// is a FAIL: a missing log under a daemon that is otherwise healthy usually
// means the running binary predates the self-owned log file.
func daemonLogStatFailure(logPath string, statErr error) CheckResult {
	if os.IsNotExist(statErr) {
		return CheckResult{
			Name:    "daemon_logging",
			Status:  StatusWarn,
			Summary: "daemon running but has no self-owned log file",
			Detail: fmt.Sprintf("expected: %s\n"+
				"The running daemon binary may predate the self-owned log file; "+
				"restart it to pick up the change.", logPath),
		}
	}
	return CheckResult{
		Name:    "daemon_logging",
		Status:  StatusWarn,
		Summary: "could not stat daemon log file",
		Detail:  fmt.Sprintf("path: %s\n%s", logPath, statErr.Error()),
	}
}

// stateFileIsFresh reports whether daemon-agents.json was updated recently
// enough that the state updater is demonstrably alive. It reuses
// checkDaemonStuck's threshold so the two checks cannot disagree about what
// "fresh" means.
func stateFileIsFresh(stateInfo os.FileInfo, now time.Time) bool {
	return stateInfo != nil && now.Sub(stateInfo.ModTime()) <= stateFileStaleThreshold
}
