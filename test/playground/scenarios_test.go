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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	runLoom(t, scenario,
		"data", "create",
		"--title", "Crash scenario",
		"--type", "task",
		"--priority", "2",
		"--status", "open",
		"--design", "Crash classification verification",
	)

	daemon := startScenarioDaemon(t, scenario, daemonLog)
	t.Cleanup(func() { scenarioCleanup(t, scenario, daemon) })

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

// TestPlaygroundHangKilledByWatchdog asserts that a silent backend is killed
// by the supervisor's output-timeout watchdog.
//
// Regression guard for the single-pgroup hung-process path: the backend claims
// work, emits its initial line, then stays silent. With a shortened watchdog,
// the daemon must log the hung-process kill instead of leaving the agent stuck
// forever.
func TestPlaygroundHangKilledByWatchdog(t *testing.T) {
	requireServe(t)

	const scenario = "hang"
	watchdog := durationFromEnv("PLAYGROUND_WATCHDOG_TIMEOUT", 15*time.Second)
	startedTimeout := durationFromEnv("PLAYGROUND_HANG_STARTED_WAIT", 25*time.Second)
	killTimeout := durationFromEnv("PLAYGROUND_HANG_WATCHDOG_WAIT", 60*time.Second)

	_ = exec.Command("bash",
		filepath.Join(hereDir(t), "teardown.sh"), scenario).Run()

	runScenarioScript(t, "setup.sh", []string{scenario}, map[string]string{
		"LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS": strconv.Itoa(int(watchdog.Seconds())),
	})

	daemonLog := filepath.Join(scenarioRuntimeDir(t, scenario), "hang.daemon.log")
	runLoom(t, scenario,
		"data", "create",
		"--title", "Hang scenario",
		"--type", "task",
		"--priority", "2",
		"--status", "open",
		"--design", "Hung-backend watchdog verification",
	)

	daemon := startScenarioDaemon(t, scenario, daemonLog)
	t.Cleanup(func() { scenarioCleanup(t, scenario, daemon) })

	startedFlag := filepath.Join(scenarioMarkerDir(t, scenario), "started.flag")
	if !waitForFile(startedFlag, startedTimeout) {
		t.Fatalf("hang backend never wrote started.flag at %s within %s\n--- daemon.log tail ---\n%s",
			startedFlag, startedTimeout, tailFile(daemonLog, 50))
	}

	hungPattern := regexp.MustCompile(`killing hung process, no activity detected`)
	if !waitForLogLine(daemonLog, hungPattern, killTimeout) {
		t.Fatalf("daemon never logged watchdog kill within %s\n--- daemon.log tail ---\n%s",
			killTimeout, tailFile(daemonLog, 80))
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
	runLoom(t, scenario,
		"data", "create",
		"--title", "Slow scenario",
		"--type", "task",
		"--priority", "2",
		"--status", "open",
		"--design", "Slow-work watchdog tolerance verification",
	)

	daemon := startScenarioDaemon(t, scenario, daemonLog)
	t.Cleanup(func() { scenarioCleanup(t, scenario, daemon) })

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

// TestPlaygroundPlannerLeaksClaimLock reproduces the planner-leaks-lock defect
// observed against the real claude/codex backends in the TREE workspace.
//
// Reproduction shape (matches what we found on TREE-1):
//
//   - A previous planner agent claimed the task, wrote a design, and exited.
//     The issue state was left as: status=open, design populated, the
//     fleet-db distributed lock still held by the planner actor (the
//     supervisor's resetTask path is skipped on clean exits — see
//     agent/recover.go:172-183 — and `loom data update --design ...`
//     without --status doesn't touch the lock).
//   - By the time the coder/worker tried to pick the task up, the planner
//     agent was no longer in the daemon (or never restarted), so the
//     supervisor's restart-cycle orphan reset (recover_helpers.go
//     resetOrphanedAgentTasks) never ran for the planner. Without that
//     reset the lock sits at its TTL (5min default) indefinitely from the
//     coder's perspective.
//   - The coder's has_design filter sees the task as eligible (queue says
//     "1 task matches") but every claim attempt returns conflict
//     "already_claimed". The supervisor classifies this as NoWork — the
//     agent looks idle when it is in fact blocked.
//
// The test pre-stages the leaked-lock state directly via raw fleet-db API
// calls (POST /claim as the synthetic planner + PATCH to status=open),
// then runs the daemon with ONLY the coder agent registered. With no
// planner agent in the daemon, the orphan-reset path never fires for the
// planner actor, and the leak persists for the entire observation window.
//
// Expected to FAIL today (the defect reproduces). Once the supervisor (or
// `loom complete` semantics) is changed to drop the lock unconditionally
// on clean agent exits — or `loom data update` is made to release on
// design write — the coder will close the task and the second-to-last
// "appears fixed" Fatalf path will trip, signalling that this test should
// be updated to assert the new healthy behavior.
func TestPlaygroundPlannerLeaksClaimLock(t *testing.T) {
	requireServe(t)

	const scenario = "leakclaim"
	const stalePlanner = "leakclaim-planner"
	leakObserveDelay := durationFromEnv("PLAYGROUND_LEAKCLAIM_OBSERVE", 45*time.Second)

	_ = exec.Command("bash",
		filepath.Join(hereDir(t), "teardown.sh"), scenario).Run()

	runScenarioScript(t, "setup.sh", []string{scenario}, nil)

	wsKey := workspaceKeyForScenario(scenario)
	taskID, err := firstTaskID(t, scenario)
	if err != nil {
		t.Fatalf("locate seeded task: %v", err)
	}

	// Stage 1: synthesize the leaked-lock state. Claim as the synthetic
	// planner actor to acquire the distributed lock and flip status to
	// in_progress; then PATCH directly (bypassing FleetBackend's
	// transitionToOpen, which would translate status=open into a /release
	// call and clear the lock) so the issue's status returns to open with
	// the lock and assignee still held by the planner. This is exactly
	// the shape TREE-1 was in.
	if _, ok, err := probeClaim(t, wsKey, taskID, stalePlanner); err != nil || !ok {
		t.Fatalf("initial claim as %q failed: ok=%v err=%v", stalePlanner, ok, err)
	}
	if err := patchIssue(t, wsKey, taskID, stalePlanner, map[string]any{
		"status": "open",
		"design": "stale design from planner that leaked its lock",
	}); err != nil {
		t.Fatalf("patch issue back to open: %v", err)
	}

	// Confirm the staged state before starting the daemon: status=open,
	// lock still held by stalePlanner. probeClaim as a fresh actor must
	// see the leak.
	preHolder, preClaimable, err := probeClaim(t, wsKey, taskID, "leakclaim-probe")
	if err != nil {
		t.Fatalf("staging probe: %v", err)
	}
	if preClaimable {
		t.Fatalf("staging failed: lock was already released before daemon start")
	}
	if preHolder != stalePlanner {
		t.Fatalf("staging produced wrong lock holder: want %q got %q", stalePlanner, preHolder)
	}

	// Stage 2: run the daemon with only the coder agent. The coder's
	// has_design filter sees the task in its ready queue; every claim
	// attempt will hit conflict and the supervisor will record NoWork.
	daemonLog := filepath.Join(scenarioRuntimeDir(t, scenario), "leakclaim.daemon.log")
	daemon := startScenarioDaemon(t, scenario, daemonLog)
	t.Cleanup(func() { scenarioCleanup(t, scenario, daemon) })

	// Stage 3: observe. With a healthy supervisor the coder would claim
	// and close within ~30s. With the leaked lock it never can, so the
	// task status stays open (and not closed) for the entire window.
	deadline := time.Now().Add(leakObserveDelay)
	for time.Now().Before(deadline) {
		status, err := taskStatus(t, scenario, taskID)
		if err != nil {
			t.Fatalf("read task status: %v", err)
		}
		if status == "closed" {
			t.Fatalf("coder closed %s despite leaked planner lock — bug appears fixed; update this test\n--- daemon.log tail ---\n%s",
				taskID, tailFile(daemonLog, 80))
		}
		time.Sleep(3 * time.Second)
	}

	// Stage 4: confirm the lock is still leaked at the end of the window.
	holder, claimable, err := probeClaim(t, wsKey, taskID, "leakclaim-probe-final")
	if err != nil {
		t.Fatalf("final probe: %v", err)
	}
	if claimable {
		t.Fatalf("lock unexpectedly released during observe window — bug appears fixed; update this test\n--- daemon.log tail ---\n%s",
			tailFile(daemonLog, 80))
	}
	if holder != stalePlanner {
		t.Errorf("end-of-window lock holder changed: want %q got %q", stalePlanner, holder)
	}

	t.Logf("reproduced planner-leaks-claim-lock: %s held by %s for %s, coder blocked\n--- daemon.log tail ---\n%s",
		taskID, holder, leakObserveDelay, tailFile(daemonLog, 60))
}

// patchIssue calls PATCH /api/v1/{ws}/issues/{id} on the embedded fleet-db
// directly. Used by TestPlaygroundPlannerLeaksClaimLock to push the issue
// back to status=open without the loomcli FleetBackend's transitionToOpen
// helper (which would translate that into a /release call and clear the
// distributed lock we're trying to leak on purpose).
func patchIssue(t *testing.T, ws, taskID, actor string, fields map[string]any) error {
	t.Helper()
	base := fleetDBBaseURL(t)
	url := fmt.Sprintf("%s/api/v1/%s/issues/%s", base, ws, taskID)
	body, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Actor", actor)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PATCH %s: HTTP %d: %s", url, resp.StatusCode, string(b))
	}
	return nil
}

// fleetDBBaseURL reads fleet-db's HTTP URL from ~/.loom/fleet-db/runtime.json.
// The same path scanFleetDBKeys uses, but for the HTTP-side probe.
func fleetDBBaseURL(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	rtPath := filepath.Join(home, ".loom", "fleet-db", "runtime.json")
	b, err := os.ReadFile(rtPath)
	if err != nil {
		t.Fatalf("read %s: %v", rtPath, err)
	}
	var rt struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("parse %s: %v", rtPath, err)
	}
	if rt.URL == "" {
		t.Fatalf("%s: url is empty", rtPath)
	}
	return strings.TrimRight(rt.URL, "/")
}

// probeClaim attempts to claim taskID in workspace ws as actor. Returns
// (holder, claimable, err): claimable=true means the claim succeeded; on
// conflict claimable=false and holder is the existing_owner reported in
// the error meta. Any other failure is returned as err.
func probeClaim(t *testing.T, ws, taskID, actor string) (string, bool, error) {
	t.Helper()
	base := fleetDBBaseURL(t)
	url := fmt.Sprintf("%s/api/v1/%s/issues/%s/claim", base, ws, taskID)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("X-Actor", actor)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		return actor, true, nil
	}
	var errEnv struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Meta    map[string]string `json:"meta"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(body, &errEnv); jerr != nil {
		return "", false, fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, resp.Status, string(body))
	}
	if errEnv.Error.Code != "already_claimed" {
		return "", false, fmt.Errorf("unexpected error code %q: %s", errEnv.Error.Code, string(body))
	}
	return errEnv.Error.Meta["existing_owner"], false, nil
}

// firstTaskID returns the ID of the first task in the scenario's workspace.
// Used by leakclaim where the test seeds exactly one task.
func firstTaskID(t *testing.T, scenario string) (string, error) {
	t.Helper()
	env := readScenarioEnv(t, scenario)
	cmd := exec.Command("loom", "data", "list", "--output", "json")
	cmd.Env = append(os.Environ(),
		"PATH="+env["PATH"],
		"LOOM_WORKSPACE="+env["LOOM_WORKSPACE"],
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("loom data list: %v\n%s", err, out.String())
	}
	// Strip slog "time=…" lines that loom emits to stdout when opening the
	// embedded fleet-db client. The actual JSON payload starts at the first '['.
	raw := out.Bytes()
	idx := bytes.IndexByte(raw, '[')
	if idx < 0 {
		return "", fmt.Errorf("data list returned no JSON array:\n%s", out.String())
	}
	var arr []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw[idx:], &arr); err != nil {
		return "", fmt.Errorf("parse data list: %v\n%s", err, out.String())
	}
	if len(arr) == 0 {
		return "", fmt.Errorf("no tasks in workspace")
	}
	return arr[0].ID, nil
}

// taskStatus returns the current status field for taskID via loom data show.
func taskStatus(t *testing.T, scenario, taskID string) (string, error) {
	t.Helper()
	env := readScenarioEnv(t, scenario)
	cmd := exec.Command("loom", "data", "show", taskID, "--output", "json")
	cmd.Env = append(os.Environ(),
		"PATH="+env["PATH"],
		"LOOM_WORKSPACE="+env["LOOM_WORKSPACE"],
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("loom data show %s: %v\n%s", taskID, err, out.String())
	}
	raw := out.Bytes()
	idx := bytes.IndexByte(raw, '{')
	if idx < 0 {
		return "", fmt.Errorf("data show returned no JSON object:\n%s", out.String())
	}
	var obj struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw[idx:], &obj); err != nil {
		return "", fmt.Errorf("parse data show: %v\n%s", err, out.String())
	}
	return obj.Status, nil
}

// TestWorkspaceRemoveCascade asserts that `loom workspace remove --force`
// fully purges the workspace's operational data from fleet-db, leaving zero
// keys under `fleet-db:<KEY>:*`. fleet-db implements a two-pass cascade in
// internal/storage/workspace.go (DeleteWorkspace, force=true path).
//
// Currently SKIPPED — the cascade code is present in the fleet-db binary
// but operational keys still survive force-remove in practice (verified
// 2026-05-14: 11 orphan keys remained after a clean create+remove cycle:
// projector:cursor, events:*, role:*, repo:*, repos-meta, roles-meta).
// Most likely cause is an async event projector recreating keys after the
// cascade's straggler sweep completes.
//
// The teardown.sh SCAN/DEL workaround and the setup.sh HTTP-409 retry
// are load-bearing because of this bug. Remove the t.Skip below — and
// then the workarounds — once fleet-db's cascade is reliable end-to-end
// (track upstream).
//
// Direct Redis probe rather than a behavioral "create-twice" probe because
// `workspace remove` has separate unrelated cleanup gaps (e.g. it doesn't
// delete the git branch it created in the source repo) that confound
// behavioral assertions.
func TestWorkspaceRemoveCascade(t *testing.T) {
	t.Skip("fleet-db cascade-delete races with the event projector; workaround in teardown.sh is still load-bearing. Re-enable once upstream fix lands and verify with `go test -tags=playground -run TestWorkspaceRemoveCascade`.")
	requireServe(t)

	const probeName = "purgeprobe"
	probeKey := strings.ToUpper(probeName)

	tmpRepo := t.TempDir()
	if out, err := exec.Command("git",
		"-c", "init.defaultBranch=main",
		"init", "-q", tmpRepo).CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", tmpRepo, err, out)
	}
	if out, err := exec.Command("git",
		"-c", "user.email=probe@loom.local", "-c", "user.name=Probe",
		"-C", tmpRepo, "commit", "--allow-empty", "-q", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	loomRun := func(args ...string) (string, error) {
		cmd := exec.Command("loom", args...)
		cmd.Env = os.Environ()
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		return out.String(), err
	}

	_, _ = loomRun("workspace", "remove", probeKey, "--force")
	t.Cleanup(func() {
		_, _ = loomRun("workspace", "remove", probeKey, "--force")
	})

	if out, err := loomRun("workspace", "create", probeName, "--repos", tmpRepo); err != nil {
		t.Fatalf("workspace create: %v\n%s", err, out)
	}

	// Verify operational keys exist before remove — otherwise this test
	// would pass trivially against any fleet-db (or no fleet-db at all).
	preKeys, err := scanFleetDBKeys(t, probeKey)
	if err != nil {
		t.Fatalf("scan before remove: %v", err)
	}
	if len(preKeys) == 0 {
		t.Fatalf("no fleet-db:%s:* keys after create — probe is meaningless", probeKey)
	}

	if out, err := loomRun("workspace", "remove", probeKey, "--force"); err != nil {
		t.Fatalf("workspace remove: %v\n%s", err, out)
	}

	postKeys, err := scanFleetDBKeys(t, probeKey)
	if err != nil {
		t.Fatalf("scan after remove: %v", err)
	}
	if len(postKeys) > 0 {
		sample := postKeys
		if len(sample) > 10 {
			sample = sample[:10]
		}
		t.Errorf("expected zero fleet-db:%s:* keys after force-remove, found %d\nfirst few: %v",
			probeKey, len(postKeys), sample)
	}
}

// scanFleetDBKeys connects to fleet-db's embedded Redis (address from
// ~/.loom/fleet-db/runtime.json) and returns every key matching
// `fleet-db:<workspace>:*`. Mirrors the SCAN logic from teardown.sh's
// embedded Python so the two see the same surface.
func scanFleetDBKeys(t *testing.T, workspace string) ([]string, error) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	rtPath := filepath.Join(home, ".loom", "fleet-db", "runtime.json")
	b, err := os.ReadFile(rtPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rtPath, err)
	}
	var rt struct {
		RedisAddr string `json:"redis_addr"`
	}
	if err := json.Unmarshal(b, &rt); err != nil {
		return nil, fmt.Errorf("parse %s: %w", rtPath, err)
	}
	if rt.RedisAddr == "" {
		return nil, fmt.Errorf("%s: redis_addr is empty", rtPath)
	}

	conn, err := net.DialTimeout("tcp", rt.RedisAddr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	pattern := fmt.Sprintf("fleet-db:%s:*", workspace)
	cursor := "0"
	var keys []string
	for {
		nextCursor, batch, err := redisScan(conn, cursor, pattern)
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		if nextCursor == "0" {
			break
		}
		cursor = nextCursor
	}
	return keys, nil
}

// redisScan issues one `SCAN cursor MATCH pattern COUNT 1000` and returns
// the next cursor + matched keys. Just enough RESP to avoid pulling in a
// Redis client dep for a test probe.
func redisScan(conn net.Conn, cursor, pattern string) (string, []string, error) {
	req := buildRESP("SCAN", cursor, "MATCH", pattern, "COUNT", "1000")
	if _, err := conn.Write(req); err != nil {
		return "", nil, err
	}
	r := newRESPReader(conn)
	arr, err := r.readArray()
	if err != nil {
		return "", nil, err
	}
	if len(arr) != 2 {
		return "", nil, fmt.Errorf("SCAN: expected 2-element array, got %d", len(arr))
	}
	nextCursor, ok := arr[0].(string)
	if !ok {
		return "", nil, fmt.Errorf("SCAN: cursor not a string: %T", arr[0])
	}
	rawKeys, ok := arr[1].([]any)
	if !ok {
		return "", nil, fmt.Errorf("SCAN: keys not an array: %T", arr[1])
	}
	keys := make([]string, 0, len(rawKeys))
	for _, k := range rawKeys {
		if s, ok := k.(string); ok {
			keys = append(keys, s)
		}
	}
	return nextCursor, keys, nil
}

// buildRESP serializes a Redis command as a RESP array of bulk strings.
func buildRESP(parts ...string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, p := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(p), p)
	}
	return b.Bytes()
}

// respReader is a minimal RESP2 reader that handles bulk strings and
// arrays of bulk strings — enough for the SCAN reply shape.
type respReader struct {
	conn net.Conn
	buf  []byte
}

func newRESPReader(conn net.Conn) *respReader {
	return &respReader{conn: conn}
}

func (r *respReader) readLine() (string, error) {
	for {
		if i := bytes.Index(r.buf, []byte("\r\n")); i >= 0 {
			line := string(r.buf[:i])
			r.buf = r.buf[i+2:]
			return line, nil
		}
		tmp := make([]byte, 4096)
		n, err := r.conn.Read(tmp)
		if n > 0 {
			r.buf = append(r.buf, tmp[:n]...)
		}
		if err != nil {
			return "", err
		}
	}
}

func (r *respReader) readN(n int) ([]byte, error) {
	for len(r.buf) < n {
		tmp := make([]byte, 4096)
		nn, err := r.conn.Read(tmp)
		if nn > 0 {
			r.buf = append(r.buf, tmp[:nn]...)
		}
		if err != nil {
			return nil, err
		}
	}
	out := r.buf[:n]
	r.buf = r.buf[n:]
	return out, nil
}

func (r *respReader) readReply() (any, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, fmt.Errorf("empty RESP line")
	}
	switch line[0] {
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		data, err := r.readN(n)
		if err != nil {
			return nil, err
		}
		if _, err := r.readLine(); err != nil {
			return nil, err
		}
		return string(data), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i], err = r.readReply()
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case '+':
		return line[1:], nil
	case '-':
		return nil, fmt.Errorf("RESP error: %s", line[1:])
	case ':':
		return strconv.Atoi(line[1:])
	default:
		return nil, fmt.Errorf("unsupported RESP type %q", line[0])
	}
}

func (r *respReader) readArray() ([]any, error) {
	v, err := r.readReply()
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", v)
	}
	return arr, nil
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
