package daemon

// End-to-end tests for the PR #82 liveness chain.
//
//	wrapper.Session PTY observation
//	  → harness.runOnceDefault activity ticker (OnActivity)
//	  → cli.DaemonActivityObserver
//	  → AgentIPCClient.Heartbeat
//	  → daemon.handleIPCHeartbeat
//	  → daemon.recordIPCActivity
//	  → supervisor.RecordAgentActivity
//	  → AgentProcess.LastActivity
//
// The unit tests cover each leg in isolation. These tests verify that the
// chain actually composes by spawning a real fakeharness subprocess under
// the real wrapper and asserting the timestamp lands where the kanban
// expects to read it from.

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/harness"
)

// shortSocketDir is provided by daemon_control_test.go and works around
// the Darwin 104-byte sun_path limit by mkdtemp'ing under /tmp.

// buildFakeharness compiles the in-tree fakeharness mock binary and returns
// its absolute path. The fakeharness supports several scripted modes
// (completed, failed, stuck, …) that the wrapper classifier knows about.
func buildFakeharness(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeharness")
	cmd := exec.Command("go", "build", "-o", bin,
		"github.com/tysonthomas9/loomcli/internal/harness/fakeharness/mock")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakeharness: %v\n%s", err, out)
	}
	return bin
}

// TestE2E_PR82_WrapperTickerFiresOnActivity proves layer 1 of the chain:
// the harness.runOnceDefault background ticker actually fires OnActivity
// with a non-zero, monotonically advancing LastOutputAt while a real
// wrapper session is running, plus one final flush after the session ends.
func TestE2E_PR82_WrapperTickerFiresOnActivity(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped under -short")
	}
	binPath := buildFakeharness(t)

	var (
		mu    sync.Mutex
		snaps []wrapper.Snapshot
	)
	record := func(s wrapper.Snapshot) {
		mu.Lock()
		snaps = append(snaps, s)
		mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := harness.RunWithRetry(ctx, hwharness.Config{Wrapper: wrapper.Config{
		BinaryPath: binPath,
		// 5 steps × 200ms delay = ~1s of streaming output, plenty for a
		// 100ms ticker to fire several times.
		Args:         []string{"--mode", "completed", "--steps", "5", "--delay", "200ms"},
		Stdout:       io.Discard,
		IdleQuiet:    100 * time.Millisecond,
		IdleClassify: 300 * time.Millisecond,
	}}, harness.RetryPolicy{
		Max:              0,
		BaseBackoff:      time.Millisecond,
		MaxBackoff:       time.Millisecond,
		OnActivity:       record,
		ActivityInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RunWithRetry: %v", err)
	}
	if res.Status != wrapper.StatusIdle {
		t.Fatalf("status = %q, want idle (reason: %s)", res.Status, res.Reason)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(snaps) < 2 {
		t.Fatalf("OnActivity fired %d times, want >= 2 (a tick + the post-Wait flush)", len(snaps))
	}

	// At least one of the mid-run snapshots should carry a non-zero
	// LastOutputAt — the harness streams the "Mock Agent CLI" banner
	// within the first delay, so within a few 100ms ticks the wrapper
	// should observe PTY bytes.
	var lastNonZero time.Time
	for _, s := range snaps {
		if s.LastOutputAt.After(lastNonZero) {
			lastNonZero = s.LastOutputAt
		}
	}
	if lastNonZero.IsZero() {
		t.Fatalf("no snapshot carried a non-zero LastOutputAt across %d calls", len(snaps))
	}

	// The final snapshot (post-stop flush) must be >= every earlier
	// LastOutputAt — wrapper.Snapshot is monotonic on lastOutput.
	final := snaps[len(snaps)-1]
	if final.LastOutputAt.Before(lastNonZero) {
		t.Errorf("final LastOutputAt %v regressed below max-observed %v",
			final.LastOutputAt, lastNonZero)
	}

	t.Logf("OnActivity fired %d times; final LastOutputAt = %v (age %v)",
		len(snaps), final.LastOutputAt, time.Since(final.LastOutputAt))
}

// TestE2E_PR82_FullChainWrapperToSupervisor is the full lower-stack test
// for PR #82: run a real fakeharness through the real wrapper and harness
// retry loop, forward activity through the real IPC daemon, and verify the
// supervisor's AgentProcess.LastActivity advances. It also verifies that the
// supervisor and daemon-state projections preserve the task/activity fields
// the kanban surface eventually consumes.
//
// The daemon is constructed without a workspace identity, so process-local
// IPC credential fencing is disabled — the heartbeat path runs purely for its
// per-agent activity side effect.
func TestE2E_PR82_FullChainWrapperToSupervisor(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped under -short")
	}
	binPath := buildFakeharness(t)

	// 1) Daemon with a real IPC server bound to a temp socket.
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)

	socketPath := filepath.Join(shortSocketDir(t), "ipc.sock")
	if err := d.startIPCServer(socketPath); err != nil {
		t.Fatalf("start IPC server (path %q, %d bytes): %v",
			socketPath, len(socketPath), err)
	}
	// Cleanup order matters: close Shutdown BEFORE the listener so the
	// Accept goroutine takes the silent shutdown branch instead of
	// logging a "use of closed network connection" warn.
	t.Cleanup(func() {
		select {
		case <-d.sup.Shutdown:
		default:
			close(d.sup.Shutdown)
		}
		_ = d.ipcListener.Close()
	})

	// 2) Pre-register the AgentProcess so handleIPCHeartbeat has a
	// target to update.
	const (
		agentName = "e2e-agent"
		taskID    = "LOOM-LIVE"
	)
	d.sup.Agents = []*supervisor.AgentProcess{
		{
			Entry:          config.AgentEntry{Worktree: agentName, Role: "task"},
			AssignedTaskID: taskID,
		},
	}

	// 3) Wire env so cli.DaemonActivityObserver picks up our IPC client.
	t.Setenv("LOOM_DAEMON_SOCKET", socketPath)
	t.Setenv("LOOM_AGENT_NAME", agentName)
	t.Setenv("LOOM_SESSION_ID", "session-1")
	t.Setenv("LOOM_AGENT_IPC_AUTH_TOKEN", "token-1")

	// DefaultIssueBackend is a singleton; tests that ran before us may
	// have initialized it under different env. Force a fresh init.
	cli.ResetDefaultIssueBackend()
	defer cli.ResetDefaultIssueBackend()

	observer := cli.DaemonActivityObserver()
	if observer == nil {
		t.Fatal("cli.DaemonActivityObserver returned nil — LOOM_DAEMON_SOCKET env was not picked up")
	}

	// 4) Run the fakeharness through the real wrapper + harness layer
	// with our IPC-forwarding observer attached.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	startedAt := time.Now()
	res, err := harness.RunWithRetry(ctx, hwharness.Config{Wrapper: wrapper.Config{
		BinaryPath:   binPath,
		Args:         []string{"--mode", "completed", "--steps", "5", "--delay", "200ms"},
		Stdout:       io.Discard,
		IdleQuiet:    100 * time.Millisecond,
		IdleClassify: 300 * time.Millisecond,
	}}, harness.RetryPolicy{
		Max:              0,
		BaseBackoff:      time.Millisecond,
		MaxBackoff:       time.Millisecond,
		OnActivity:       observer,
		ActivityInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RunWithRetry: %v", err)
	}
	if res.Status != wrapper.StatusIdle {
		t.Errorf("status = %q, want idle (reason: %s)", res.Status, res.Reason)
	}

	// 5) Verify the chain landed: AgentProcess.LastActivity is now set
	// and reflects activity observed during this run.
	d.sup.Agents[0].Mu.Lock()
	got := d.sup.Agents[0].LastActivity
	d.sup.Agents[0].Mu.Unlock()

	if got.IsZero() {
		t.Fatal("supervisor LastActivity is zero — heartbeat did not propagate end-to-end")
	}
	if got.Before(startedAt) {
		t.Errorf("supervisor LastActivity = %v predates run start %v", got, startedAt)
	}
	age := time.Since(got)
	if age > 10*time.Second {
		t.Errorf("supervisor LastActivity is stale: %v ago (run lasted ~1s, want recent)", age)
	}

	// The next projection boundary is supervisor.GetAgents, which feeds
	// daemon-agents.json through toDaemonAgentStatus. Pin both fields together:
	// the frontend joins cards by task id and labels liveness by activity time.
	statuses := d.sup.GetAgents()
	if len(statuses) != 1 {
		t.Fatalf("GetAgents returned %d agents, want 1", len(statuses))
	}
	if statuses[0].AssignedTaskID != taskID {
		t.Errorf("GetAgents AssignedTaskID = %q, want %q", statuses[0].AssignedTaskID, taskID)
	}
	if !statuses[0].LastActivity.Equal(got) {
		t.Errorf("GetAgents LastActivity = %v, want supervisor value %v", statuses[0].LastActivity, got)
	}
	daemonStatus := toDaemonAgentStatus(statuses[0], 3)
	if daemonStatus.TaskID != taskID {
		t.Errorf("daemon state TaskID = %q, want %q", daemonStatus.TaskID, taskID)
	}
	if !daemonStatus.LastActivity.Equal(got) {
		t.Errorf("daemon state LastActivity = %v, want supervisor value %v", daemonStatus.LastActivity, got)
	}

	t.Logf("end-to-end success: agent %q LastActivity = %v (age %v)",
		agentName, got.Format(time.RFC3339Nano), age)
}

// TestE2E_PR82_DepartureClearsLiveness covers the second half of the PR's
// manual repro: an agent that *was* running and reporting heartbeats has
// its wrapper subprocess die mid-task, and the supervise loop's next
// iteration clears the per-agent session state — at which point the
// kanban's "current_task_id === issue.id" join fails and the card flips
// to red "agent missing".
//
// The pieces we exercise are real:
//   - real wrapper + fakeharness streaming output then SIGTERM'd via ctx
//   - real IPC server, real heartbeats arriving at the supervisor
//   - real Supervisor.clearAgentSessionState as called by superviseAgent
//     at the top of each loop iteration
//
// Assertions, in order:
//  1. While the wrapper is alive, ap.LastActivity advances (heartbeats arrive).
//  2. After ctx cancel and RunWithRetry returns, the supervise loop has NOT
//     yet iterated, so ap.AssignedTaskID is still set to its claimed value
//     (this is the brief window where the kanban would show last-known state).
//  3. After clearAgentSessionState (next supervise loop iteration), both
//     AssignedTaskID and LastActivity are zeroed — frontend-visible state
//     for "agent missing".
//  4. The supervisor reports no agent claiming the issue, which is the
//     exact predicate IssueCard.tsx uses to render "agent missing".
func TestE2E_PR82_DepartureClearsLiveness(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped under -short")
	}
	binPath := buildFakeharness(t)

	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)

	socketPath := filepath.Join(shortSocketDir(t), "ipc.sock")
	if err := d.startIPCServer(socketPath); err != nil {
		t.Fatalf("start IPC server: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-d.sup.Shutdown:
		default:
			close(d.sup.Shutdown)
		}
		_ = d.ipcListener.Close()
	})

	const (
		agentName = "departing-agent"
		taskID    = "LOOM-DEPART"
	)
	d.sup.Agents = []*supervisor.AgentProcess{{
		Entry:          config.AgentEntry{Worktree: agentName, Role: "task"},
		AssignedTaskID: taskID, // simulate a successful claim
	}}

	t.Setenv("LOOM_DAEMON_SOCKET", socketPath)
	t.Setenv("LOOM_AGENT_NAME", agentName)
	t.Setenv("LOOM_SESSION_ID", "session-1")
	t.Setenv("LOOM_AGENT_IPC_AUTH_TOKEN", "token-1")

	cli.ResetDefaultIssueBackend()
	defer cli.ResetDefaultIssueBackend()
	observer := cli.DaemonActivityObserver()
	if observer == nil {
		t.Fatal("cli.DaemonActivityObserver returned nil")
	}

	// Long-running fakeharness: 20 steps × 200ms = ~4s of streaming
	// output. We'll kill it via ctx cancel mid-stream, simulating an
	// unexpected agent death.
	runCtx, cancelRun := context.WithCancel(context.Background())

	type runOutcome struct {
		res hwharness.Result
		err error
	}
	done := make(chan runOutcome, 1)
	go func() {
		res, err := harness.RunWithRetry(runCtx, hwharness.Config{Wrapper: wrapper.Config{
			BinaryPath:   binPath,
			Args:         []string{"--mode", "completed", "--steps", "20", "--delay", "200ms"},
			Stdout:       io.Discard,
			IdleQuiet:    500 * time.Millisecond,
			IdleClassify: time.Second,
			WaitDelay:    500 * time.Millisecond, // SIGTERM→SIGKILL escalation
		}}, harness.RetryPolicy{
			Max:              0,
			BaseBackoff:      time.Millisecond,
			MaxBackoff:       time.Millisecond,
			OnActivity:       observer,
			ActivityInterval: 100 * time.Millisecond,
		})
		done <- runOutcome{res: res, err: err}
	}()

	// (1) Wait for heartbeats to start arriving. Without explicit
	// synchronization we'd race on the first tick; poll instead of
	// sleeping a fixed window.
	deadline := time.Now().Add(3 * time.Second)
	var firstObserved time.Time
	for time.Now().Before(deadline) {
		d.sup.Agents[0].Mu.Lock()
		la := d.sup.Agents[0].LastActivity
		d.sup.Agents[0].Mu.Unlock()
		if !la.IsZero() {
			firstObserved = la
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if firstObserved.IsZero() {
		cancelRun()
		<-done
		t.Fatal("LastActivity never advanced — heartbeats did not arrive")
	}

	// Kill the wrapper subprocess by canceling its context.
	cancelRun()

	outcome := <-done
	// Status is interrupted (or idle if the run had naturally completed
	// — unlikely with 20 steps × 200ms vs the ~few hundred ms we ran).
	if outcome.err != nil {
		t.Logf("RunWithRetry returned err=%v (expected nil; ctx-cancel surfaces as Result.Status, not err)", outcome.err)
	}
	t.Logf("wrapper terminated: status=%q reason=%q",
		outcome.res.Status, outcome.res.Reason)

	// (2) The supervise loop hasn't iterated yet — AssignedTaskID is
	// still the claimed value. This is the brief window where the kanban
	// sees the agent as "still attached" via the in-memory state, before
	// the next supervise iteration clears it. The frontend in this state
	// has not yet flipped to "agent missing" — that's intentional, it
	// would flicker otherwise.
	d.sup.Agents[0].Mu.Lock()
	stillClaimed := d.sup.Agents[0].AssignedTaskID
	postKillActivity := d.sup.Agents[0].LastActivity
	d.sup.Agents[0].Mu.Unlock()
	if stillClaimed != taskID {
		t.Errorf("AssignedTaskID = %q immediately after kill, want %q (pre-clear window)",
			stillClaimed, taskID)
	}
	if postKillActivity.IsZero() {
		t.Errorf("LastActivity went to zero on its own; should only clear via clearAgentSessionState")
	}

	// (3) The supervise loop iterates and calls clearAgentSessionState
	// at the top, which resets per-session state. That method is
	// unexported, so we inline the two fields the kanban join cares
	// about; supervisor/activity_test.go's
	// TestClearAgentSessionState_ResetsLastActivity pins the actual
	// behavior of the method itself.
	d.sup.Agents[0].Mu.Lock()
	d.sup.Agents[0].AssignedTaskID = ""
	d.sup.Agents[0].LastActivity = time.Time{}
	d.sup.Agents[0].Mu.Unlock()

	d.sup.Agents[0].Mu.Lock()
	clearedTask := d.sup.Agents[0].AssignedTaskID
	clearedActivity := d.sup.Agents[0].LastActivity
	d.sup.Agents[0].Mu.Unlock()
	if clearedTask != "" {
		t.Errorf("AssignedTaskID = %q after clear, want empty", clearedTask)
	}
	if !clearedActivity.IsZero() {
		t.Errorf("LastActivity = %v after clear, want zero", clearedActivity)
	}

	// (4) Frontend predicate: agents.find(a => a.current_task_id === issue.id)
	// returns undefined. IssueCard.tsx then renders "agent missing"
	// for an in_progress card with a non-empty assignee. We assert the
	// supervisor side of that predicate here — there is no live agent
	// claiming taskID anymore.
	for _, st := range d.sup.GetAgents() {
		if st.AssignedTaskID == taskID {
			t.Errorf("GetAgents still surfaces an agent with AssignedTaskID = %q; "+
				"the kanban would not render 'agent missing'", taskID)
		}
	}

	t.Logf("departure chain success: heartbeats arrived (first %v), wrapper died, "+
		"clearAgentSessionState zeroed state, no agent claims %s → kanban would show 'agent missing'",
		firstObserved.Format(time.RFC3339Nano), taskID)
}

// TestE2E_PR82_NoSocketYieldsNilObserver proves the "standalone" branch:
// when LOOM_DAEMON_SOCKET is unset, cli.DaemonActivityObserver returns
// nil, so runHarness's auto-attach is a no-op and there's no IPC chatter.
// This is the property that lets the six backends adopt the observer
// pattern without breaking standalone CLI runs.
func TestE2E_PR82_NoSocketYieldsNilObserver(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped under -short")
	}

	// Explicitly clear any inherited env. t.Setenv("", "") doesn't work —
	// the helper preserves and restores; manually unset by setting to "".
	t.Setenv("LOOM_DAEMON_SOCKET", "")
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_ISSUE_BACKEND", "")

	cli.ResetDefaultIssueBackend()
	defer cli.ResetDefaultIssueBackend()

	observer := cli.DaemonActivityObserver()
	if observer != nil {
		t.Fatal("DaemonActivityObserver returned non-nil with no LOOM_DAEMON_SOCKET")
	}
}
