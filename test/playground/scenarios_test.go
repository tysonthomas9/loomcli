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
	"net"
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

// cleanParentEnv returns os.Environ() with daemon-supervisor- and
// agent-session-specific vars stripped. When this test is itself running
// inside a loom-managed worker, the parent process exposes
// LOOM_DAEMON_SOCKET + a lease token; subprocesses would inherit them and
// route mutations through IPC instead of straight to the playground's
// fleet server. Strip them so each subprocess sees a clean fleet client.
func cleanParentEnv() []string {
	stripped := map[string]struct{}{
		"LOOM_DAEMON_SOCKET":                {},
		"LOOM_AGENT_LEASE_ID":               {},
		"LOOM_AGENT_LEASE_TOKEN":            {},
		"LOOM_AGENT_OWNERSHIP_LEASE_ID":     {},
		"LOOM_AGENT_OWNERSHIP_FENCING_TOKEN": {},
		"LOOM_SESSION_ID":                   {},
		"LOOM_ASSIGNED_TASK_ID":             {},
		"LOOM_ROLE":                         {},
		"LOOM_ROLE_TASK_FILTER":             {},
		"LOOM_WORKTREE_PATH":                {},
		"LOOM_EVENTS_DIR":                   {},
		"LOOM_YIELD_FILE":                   {},
		"LOOM_AGENT_NAME":                   {},
		"LOOM_WORKSPACE":                    {},
		"LOOM_FLEET_ACTOR":                  {},
	}
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := stripped[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runLoomAsActor runs `loom <args...>` with the playground env applied
// plus a LOOM_FLEET_ACTOR override (so different fleet identities can drive
// the same workspace from one test). Returns stdout/stderr + the exit error
// so the caller can assert on either success or expected failure.
func runLoomAsActor(t *testing.T, scenario, actor string, args ...string) (string, error) {
	t.Helper()
	env := readScenarioEnv(t, scenario)
	cmd := exec.Command("loom", args...)
	procEnv := append(cleanParentEnv(),
		"PATH="+env["PATH"],
		"LOOM_WORKSPACE="+env["LOOM_WORKSPACE"],
		"LOOM_FLEET_ACTOR="+actor,
		"LOOM_AGENT_NAME="+actor,
	)
	cmd.Env = procEnv
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// TestPlaygroundPlannerLeaksClaimLock pre-stages the leaked-lock state from
// LOOM-1: an issue claimed by `planner` actor that received `--design` but
// no status transition out of `in_progress`. The lock is still held.
//
// Two-phase assertion:
//
//  1. Pre-fix shape: a downstream `worker` actor cannot claim the issue —
//     fleet-db returns conflict already_claimed.
//  2. Post-fix shape: after `loom complete` runs in a worktree whose
//     `.agent.lock` references the leaked issue (and is invoked as the
//     planner actor), the claim is released and `worker` can claim cleanly.
//
// Negative control: on the parent commit of the LOOM-1 fix, Phase 2 must
// FAIL (worker still can't claim). The fix author should record the
// pre-fix commit hash here when merging.
//
// Pre-fix parent commit: <fill in at merge time>
func TestPlaygroundPlannerLeaksClaimLock(t *testing.T) {
	requireServe(t)

	const scenario = "lockleak"

	// Pre-clean any stale state from earlier interrupted runs.
	_ = exec.Command("bash",
		filepath.Join(hereDir(t), "teardown.sh"), scenario).Run()

	runScenarioScript(t, "setup.sh", []string{scenario}, nil)
	t.Cleanup(func() {
		_ = exec.Command("bash",
			filepath.Join(hereDir(t), "teardown.sh"), scenario).Run()
	})

	// Step 1: create the task as the planner actor (open status).
	createOut, err := runLoomAsActor(t, scenario, "planner",
		"data", "create",
		"--title", "Lock leak repro",
		"--type", "task",
		"--priority", "2",
		"--status", "open",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("loom data create: %v\n%s", err, createOut)
	}
	var created struct {
		ID string `json:"id"`
		// fall back path when the wire shape is the issue itself
		Issue struct {
			ID string `json:"id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal([]byte(createOut), &created); err != nil {
		// Some create outputs wrap in {"data": {...}}. Strip down to
		// the first ID-looking field via a permissive re-parse.
		idMatch := regexp.MustCompile(`"id"\s*:\s*"([^"]+)"`).FindStringSubmatch(createOut)
		if len(idMatch) < 2 {
			t.Fatalf("could not parse created task ID from output: %s", createOut)
		}
		created.ID = idMatch[1]
	}
	taskID := created.ID
	if taskID == "" {
		taskID = created.Issue.ID
	}
	if taskID == "" {
		t.Fatalf("created task ID is empty; output: %s", createOut)
	}

	// Step 2: claim the task as planner — this puts the claim lock on
	// fleet-db with planner holding it. Use `loom data claim` (which calls
	// POST /issues/<id>/claim) so the lock state is exactly the one the
	// real planner pre-claim would produce.
	if out, err := runLoomAsActor(t, scenario, "planner",
		"data", "claim", taskID,
	); err != nil {
		t.Fatalf("planner claim: %v\n%s", err, out)
	}

	// Step 3: planner writes the design but no status transition. This is
	// the LOOM-1 bug shape: the lock leaks because --design alone doesn't
	// trigger release.
	if out, err := runLoomAsActor(t, scenario, "planner",
		"data", "update", taskID, "--design", "Lock leak test design",
	); err != nil {
		t.Fatalf("planner --design write: %v\n%s", err, out)
	}

	// Phase A — pre-fix assertion: worker cannot claim.
	out, claimErr := runLoomAsActor(t, scenario, "worker",
		"data", "claim", taskID,
	)
	if claimErr == nil {
		t.Skipf("worker claim succeeded before fix is applied — fleet-db side may have auto-released. Output:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "conflict") && !strings.Contains(strings.ToLower(out), "already_claimed") {
		t.Fatalf("worker claim failed for an unexpected reason — wanted conflict/already_claimed:\n%s", out)
	}

	// Step 4: simulate the planner agent exiting. Write a synthetic
	// `.agent.lock` to a temp worktree dir whose TaskID is the leaked
	// task, then invoke `loom complete` as the planner actor.
	tmp := t.TempDir()
	lock := map[string]interface{}{
		"pid":        os.Getpid(),
		"command":    "plan",
		"agent_name": "planner",
		"task_id":    taskID,
		"started_at": time.Now().Format(time.RFC3339),
	}
	lockBytes, _ := json.MarshalIndent(lock, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, ".agent.lock"), lockBytes, 0o600); err != nil {
		t.Fatalf("write synthetic .agent.lock: %v", err)
	}
	env := readScenarioEnv(t, scenario)
	completeCmd := exec.Command("loom", "complete")
	completeCmd.Env = append(cleanParentEnv(),
		"PATH="+env["PATH"],
		"LOOM_WORKSPACE="+env["LOOM_WORKSPACE"],
		"LOOM_FLEET_ACTOR=planner",
		"LOOM_AGENT_NAME=planner",
		"LOOM_WORKTREE_PATH="+tmp,
	)
	var completeOut bytes.Buffer
	completeCmd.Stdout = &completeOut
	completeCmd.Stderr = &completeOut
	if err := completeCmd.Run(); err != nil {
		t.Fatalf("loom complete: %v\n%s", err, completeOut.String())
	}

	// Phase B — post-fix assertion: worker can now claim.
	postOut, postErr := runLoomAsActor(t, scenario, "worker",
		"data", "claim", taskID,
	)
	if postErr != nil {
		t.Fatalf("worker still cannot claim after planner loom complete — LOOM-1 fix appears broken:\nloom complete output was:\n%s\nworker claim output:\n%s",
			completeOut.String(), postOut)
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
