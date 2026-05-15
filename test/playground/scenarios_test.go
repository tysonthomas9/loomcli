//go:build playground

// Daemon-lifecycle failure-mode scenarios, ported from scenarios/*.sh.
//
// One runner, one assertion style, parallelizable. Each scenario:
//   1. Provisions an isolated `playground-<scenario>` workspace via setup.sh
//   2. Drives the supervisor against a misbehaving loom-backend-playground-<scenario>
//   3. Asserts the supervisor's observable response (log line, kill or
//      no-kill, heartbeat count, etc.)
//   4. Tears the workspace down regardless of outcome.
//
// Scenarios run sequentially today because they share `~/.loom/` namespace
// state; if that ever becomes shardable, drop t.Parallel() in here.

package playground_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scenarioRuntimeDir returns `test/playground/.runtime-<scenario>`.
func scenarioRuntimeDir(t *testing.T, scenario string) string {
	t.Helper()
	return filepath.Join(hereDir(t), ".runtime-"+scenario)
}

// workspaceKeyForScenario mirrors setup.sh: `PLAYGROUND-<UPPER>`.
func workspaceKeyForScenario(scenario string) string {
	return "PLAYGROUND-" + strings.ToUpper(scenario)
}

// runScenarioScript runs a script under test/playground/ with positional
// args and extra env, surfacing tail output on failure.
func runScenarioScript(t *testing.T, name string, args []string, extraEnv map[string]string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(hereDir(t), name)}, args...)...)
	cmd.Env = os.Environ()
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v failed: %v\n--- output tail ---\n%s",
			name, args, err, tail(out.String(), 40))
	}
}

// readScenarioEnv parses .runtime-<scenario>/env into a map. setup.sh writes
// `export KEY=value` or `export KEY="value"`; $PATH is expanded against the
// current process so the test inherits a usable PATH with the backend bin on it.
func readScenarioEnv(t *testing.T, scenario string) map[string]string {
	t.Helper()
	envPath := filepath.Join(scenarioRuntimeDir(t, scenario), "env")
	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read %s: %v", envPath, err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
		if line == "" {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		k := line[:eq]
		v := strings.Trim(line[eq+1:], `"`)
		v = strings.ReplaceAll(v, "$PATH", os.Getenv("PATH"))
		out[k] = v
	}
	return out
}

// startScenarioDaemon launches `loom daemon` with the scenario's env applied
// and stdout/stderr piped to logPath. Returns the process for cleanup.
func startScenarioDaemon(t *testing.T, scenario, logPath string) *exec.Cmd {
	t.Helper()
	env := readScenarioEnv(t, scenario)
	cmd := exec.Command("loom", "daemon")
	cmd.Dir = hereDir(t)
	procEnv := append(os.Environ(),
		"PATH="+env["PATH"],
		"LOOM_WORKSPACE="+env["LOOM_WORKSPACE"],
	)
	if v, ok := env["LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS"]; ok {
		procEnv = append(procEnv, "LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS="+v)
	}
	cmd.Env = procEnv
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create %s: %v", logPath, err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	// Match daemon.sh's 2s settle window so agent registration completes
	// before scenarios start asserting against state.
	time.Sleep(2 * time.Second)
	return cmd
}

// runLoom runs `loom <args...>` with the scenario env applied. Used to
// create the per-scenario task that the daemon then processes.
func runLoom(t *testing.T, scenario string, args ...string) {
	t.Helper()
	env := readScenarioEnv(t, scenario)
	cmd := exec.Command("loom", args...)
	cmd.Env = append(os.Environ(),
		"PATH="+env["PATH"],
		"LOOM_WORKSPACE="+env["LOOM_WORKSPACE"],
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("loom %v failed: %v\n%s", args, err, out.String())
	}
}

// waitForFile polls until path exists or timeout expires. Returns true on
// success, false on timeout — caller decides whether timeout is fatal so
// it can dump daemon logs first.
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// waitForLogLine polls the file at logPath for any line matching pattern.
// Returns true on match, false on timeout.
func waitForLogLine(logPath string, pattern *regexp.Regexp, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(logPath); err == nil && pattern.Match(b) {
			return true
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

// logHasLine reports whether logPath currently contains a line matching pattern.
func logHasLine(logPath string, pattern *regexp.Regexp) bool {
	b, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	return pattern.Match(b)
}

// scenarioMarkerDir mirrors the shell scenarios' convention:
// $LOOM_CONFIG_DIR/workspaces/playground-<scenario>/<scenario>/
// The backends write their started.flag / heartbeat.count here.
func scenarioMarkerDir(t *testing.T, scenario string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	loomDir := os.Getenv("LOOM_CONFIG_DIR")
	if loomDir == "" {
		loomDir = filepath.Join(home, ".loom")
	}
	return filepath.Join(loomDir, "workspaces", "playground-"+scenario, scenario)
}

// tailFile reads up to the last n lines of a file; returns "" if unreadable.
func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return tail(string(b), n)
}

// scenarioCleanup is the common defer body: stop daemon, run teardown.sh
// for the scenario. Errors are swallowed because the test outcome is
// already decided by the assertions above.
func scenarioCleanup(t *testing.T, scenario string, daemon *exec.Cmd) {
	t.Helper()
	stopDaemon(t, daemon)
	teardown := exec.Command("bash",
		filepath.Join(hereDir(t), "teardown.sh"), scenario)
	teardown.Env = os.Environ()
	_ = teardown.Run()
}

// TestPlaygroundCrashClassifiedAsFailure asserts that when the backend exits
// non-zero a few seconds into invoke, the supervisor classifies the exit and
// logs a failure/retry signal.
//
// Ported from scenarios/crash_classified_as_failure.sh. Template-level
// scenario — uses a permissive regex over several known failure log
// shapes so minor wording tweaks in restart.go don't break it.
func TestPlaygroundCrashClassifiedAsFailure(t *testing.T) {
	requireServe(t)

	const scenario = "crash"
	startedTimeout := durationFromEnv("PLAYGROUND_CRASH_STARTED_WAIT", 25*time.Second)
	backoffTimeout := durationFromEnv("PLAYGROUND_CRASH_BACKOFF_WAIT", 30*time.Second)

	// Pre-clean any stale state from earlier interrupted runs.
	_ = exec.Command("bash",
		filepath.Join(hereDir(t), "teardown.sh"), scenario).Run()

	runScenarioScript(t, "setup.sh", []string{scenario}, nil)

	daemonLog := filepath.Join(scenarioRuntimeDir(t, scenario), "crash.daemon.log")
	daemon := startScenarioDaemon(t, scenario, daemonLog)
	t.Cleanup(func() { scenarioCleanup(t, scenario, daemon) })

	runLoom(t, scenario,
		"data", "create",
		"--title", "Crash scenario",
		"--type", "task",
		"--priority", "2",
		"--status", "open",
		"--design", "Crash classification verification",
	)

	startedFlag := filepath.Join(scenarioMarkerDir(t, scenario), "started.flag")
	if !waitForFile(startedFlag, startedTimeout) {
		t.Fatalf("crash backend never wrote started.flag at %s within %s\n--- daemon.log tail ---\n%s",
			startedFlag, startedTimeout, tailFile(daemonLog, 50))
	}

	// Same permissive pattern as the shell scenario. See the script comment
	// for why this is intentionally broad — replace with a tight regex if
	// converting this template into a real regression guard.
	failurePattern := regexp.MustCompile(`waiting.*before retry|spawn failed|max retries|exit code|exited with`)
	if !waitForLogLine(daemonLog, failurePattern, backoffTimeout) {
		t.Fatalf("daemon never logged a failure/retry signal within %s\n--- daemon.log tail ---\n%s",
			backoffTimeout, tailFile(daemonLog, 50))
	}
}

// TestPlaygroundSlowBackendNotKilled asserts that legitimate slow work
// (one stdout line every <interval>s, interval < watchdog timeout) survives
// the supervisor's output-timeout watchdog.
//
// Ported from scenarios/slow_backend_not_killed.sh. Regression guard against
// anyone tightening the watchdog past what real backends need (shortens the
// default, double-counts ticks, stops resetting on transcript writes).
func TestPlaygroundSlowBackendNotKilled(t *testing.T) {
	requireServe(t)

	const scenario = "slow"
	watchdog := durationFromEnv("PLAYGROUND_WATCHDOG_TIMEOUT", 15*time.Second)
	startedTimeout := durationFromEnv("PLAYGROUND_SLOW_STARTED_WAIT", 25*time.Second)
	observe := durationFromEnv("PLAYGROUND_SLOW_OBSERVE", 40*time.Second)

	_ = exec.Command("bash",
		filepath.Join(hereDir(t), "teardown.sh"), scenario).Run()

	// LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS is read by setup.sh and written
	// into .runtime-slow/env so startScenarioDaemon picks it up. The
	// supervisor reads it via Supervisor.GetOutputTimeout
	// (internal/cli/daemon/supervisor/restart.go).
	runScenarioScript(t, "setup.sh", []string{scenario}, map[string]string{
		"LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS": strconv.Itoa(int(watchdog.Seconds())),
	})

	daemonLog := filepath.Join(scenarioRuntimeDir(t, scenario), "slow.daemon.log")
	daemon := startScenarioDaemon(t, scenario, daemonLog)
	t.Cleanup(func() { scenarioCleanup(t, scenario, daemon) })

	runLoom(t, scenario,
		"data", "create",
		"--title", "Slow scenario",
		"--type", "task",
		"--priority", "2",
		"--status", "open",
		"--design", "Slow-work watchdog tolerance verification",
	)

	startedFlag := filepath.Join(scenarioMarkerDir(t, scenario), "started.flag")
	if !waitForFile(startedFlag, startedTimeout) {
		t.Fatalf("slow backend never wrote started.flag at %s within %s\n--- daemon.log tail ---\n%s",
			startedFlag, startedTimeout, tailFile(daemonLog, 50))
	}

	// Observe the full window. If the watchdog were over-aggressive it
	// would log the hung-process kill mid-window; we check at the end.
	time.Sleep(observe)

	// LOG_WATCHDOG_HUNG from lib/daemon.sh — the exact slog message the
	// supervisor emits when it kills for output-timeout.
	hungPattern := regexp.MustCompile(`killing hung process, no activity detected`)
	if logHasLine(daemonLog, hungPattern) {
		t.Errorf("daemon logged watchdog kill — false positive on slow work\n--- daemon.log tail ---\n%s",
			tailFile(daemonLog, 80))
	}

	heartbeatFile := filepath.Join(scenarioMarkerDir(t, scenario), "heartbeat.count")
	b, err := os.ReadFile(heartbeatFile)
	if err != nil {
		t.Fatalf("read heartbeat file %s: %v", heartbeatFile, err)
	}
	ticksStr := strings.TrimSpace(string(b))
	ticks, err := strconv.Atoi(ticksStr)
	if err != nil {
		t.Fatalf("parse heartbeat count %q: %v", ticksStr, err)
	}
	// Slow backend ticks every 10s; over the observe window we expect ≥ 2.
	if ticks < 2 {
		t.Errorf("slow backend completed only %d heartbeat ticks, expected ≥ 2", ticks)
	}
}

// durationFromEnv reads a number-of-seconds env var, returning fallback
// when unset or unparseable.
func durationFromEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}
