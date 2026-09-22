package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// TestEmitTaskLifecycleEvents drives the new emit helpers added to plan.go
// (used by both single-task and daemon-mode plan/task paths) and asserts the
// resulting JSONL stream contains a TaskClaimed → TaskCompleted → TaskFailed
// sequence with the expected fields.
//
// We assert the JSONL side here because it is observable without standing up
// an in-memory exporter — the existing
// TestAgentEventBus_EmitsLoomTaskSpanUnderActiveContext (in
// internal/cli/agent_event_bus_test.go) covers the otelexport side of the
// same bus, and TestFullStackTrace_AgentRun_StructureAssertion (in
// internal/cli/full_stack_trace_test.go) covers the loom.task span tree
// shape. This test plugs the gap that those tests don't cover: the new
// helpers in plan.go are wired up correctly and write events to the bus.
//
// All three assertions run inside a single test case because resetting the
// singleton bus on a per-subcase basis is wasteful and the assertions don't
// interact.
func TestEmitTaskLifecycleEvents(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOOM_EVENTS_DIR", tmp)

	// Reset the singleton bus so this test's LOOM_EVENTS_DIR takes effect
	// regardless of what prior tests in the package initialized. Re-reset
	// at cleanup so subsequent tests get a fresh initializer.
	cli.TestingResetAgentEventBus()
	t.Cleanup(cli.TestingResetAgentEventBus)

	worktree := t.TempDir()
	writeLockFile(t, worktree, "T-100")
	completedTask := NewMockIssueBackend()
	completedTask.GetResult = &backend.IssueDetailData{
		IssueData: backend.IssueData{ID: "T-100", Status: "closed", Assignee: "plan-agent"},
	}
	setDefaultIssueBackend(completedTask)
	t.Cleanup(resetDefaultIssueBackend)

	// 1. TaskClaimed (the daemon-mode emit path; LOOM_ASSIGNED_TASK_ID
	// would be the source in production).
	emitTaskClaimedFromEnv("plan-agent", "loomcli-42")

	// 2. TaskCompleted on success — duration carried, task id recovered
	// from the lock file written by the simulated `loom claim`.
	emitTaskLifecycleResult("plan-agent", worktree, time.Now().Add(-2*time.Second), nil)

	// 3. TaskFailed on error — classifier kicks in, ErrorClass populated.
	emitTaskLifecycleResult("plan-agent", worktree, time.Now().Add(-1*time.Second), errors.New("simulated agent failure"))

	// Force the JSONL writer to flush. Bus.Close drains the buffer to
	// disk; reset re-creates the singleton on the next call so other
	// tests aren't poisoned.
	cli.TestingResetAgentEventBus()

	evts := readEmittedEvents(t, tmp)
	if len(evts) != 3 {
		t.Fatalf("expected 3 events, got %d (events: %+v)", len(evts), evts)
	}

	// Event 1: TaskClaimed.
	if evts[0].Type != events.TaskClaimed {
		t.Errorf("event[0]: expected type %q, got %q", events.TaskClaimed, evts[0].Type)
	}
	if evts[0].Agent != "plan-agent" {
		t.Errorf("event[0]: expected agent 'plan-agent', got %q", evts[0].Agent)
	}
	claimedData, err := evts[0].DecodeData()
	if err != nil {
		t.Fatalf("event[0] decode: %v", err)
	}
	cd, _ := claimedData.(*events.TaskClaimedData)
	if cd == nil || cd.TaskID != "loomcli-42" {
		t.Errorf("event[0]: expected TaskID 'loomcli-42', got %+v", cd)
	}

	// Event 2: TaskCompleted with non-zero Duration and lock-recovered TaskID.
	if evts[1].Type != events.TaskCompleted {
		t.Errorf("event[1]: expected type %q, got %q", events.TaskCompleted, evts[1].Type)
	}
	completedData, err := evts[1].DecodeData()
	if err != nil {
		t.Fatalf("event[1] decode: %v", err)
	}
	td, _ := completedData.(*events.TaskCompletedData)
	if td == nil {
		t.Fatalf("event[1]: expected *TaskCompletedData, got %T", completedData)
	}
	if td.TaskID != "T-100" {
		t.Errorf("event[1]: expected TaskID recovered from lock 'T-100', got %q", td.TaskID)
	}
	if td.Duration.Duration <= 0 {
		t.Errorf("event[1]: expected non-zero duration, got %s", td.Duration.Duration)
	}

	// Event 3: TaskFailed with classifier-populated ErrorClass.
	if evts[2].Type != events.TaskFailed {
		t.Errorf("event[2]: expected type %q, got %q", events.TaskFailed, evts[2].Type)
	}
	failedData, err := evts[2].DecodeData()
	if err != nil {
		t.Fatalf("event[2] decode: %v", err)
	}
	fd, _ := failedData.(*events.TaskFailedData)
	if fd == nil {
		t.Fatalf("event[2]: expected *TaskFailedData, got %T", failedData)
	}
	if fd.TaskID != "T-100" {
		t.Errorf("event[2]: expected TaskID 'T-100', got %q", fd.TaskID)
	}
	if fd.Error != "simulated agent failure" {
		t.Errorf("event[2]: expected error message preserved, got %q", fd.Error)
	}
	if fd.ErrorClass == "" {
		t.Errorf("event[2]: expected non-empty ErrorClass from classifier")
	}
}

func TestRunAgentDaemonEmitsTaskLifecycleEvents(t *testing.T) {
	tests := []struct {
		name       string
		invokeErr  error
		terminal   events.EventType
		wantErrMsg string
	}{
		{name: "completed", terminal: events.TaskCompleted},
		{
			name:       "failed",
			invokeErr:  errors.New("simulated custom-role failure"),
			terminal:   events.TaskFailed,
			wantErrMsg: "simulated custom-role failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: the backend registry and AgentEventBus are process-wide.
			tmp := t.TempDir()
			eventsDir := filepath.Join(tmp, "events")
			worktree := filepath.Join(tmp, "worktree")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatalf("create worktree: %v", err)
			}

			t.Setenv("LOOM_ASSIGNED_TASK_ID", "loomcli-02")
			t.Setenv("LOOM_EVENTS_DIR", eventsDir)
			t.Setenv("LOOM_SESSION_ID", "supervisor-session")
			t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", filepath.Join(tmp, "runtime"))

			cli.TestingResetAgentEventBus()
			t.Cleanup(cli.TestingResetAgentEventBus)
			resetBackendState(t)
			completedTask := NewMockIssueBackend()
			completedTask.GetResult = &backend.IssueDetailData{
				IssueData: backend.IssueData{ID: "loomcli-02", Status: "closed", Assignee: "reviewer-1"},
			}
			setDefaultIssueBackend(completedTask)
			t.Cleanup(resetDefaultIssueBackend)

			backend := &mockBackend{name: "custom-role-lifecycle"}
			backend.nonInteractiveFunc = func(string, string, string, <-chan struct{}, *usage.Collector) error {
				// Snapshot the JSONL stream at the actual invocation boundary. This
				// must already contain task.claimed. Do not reset or close the bus:
				// this asserts that the event itself was made durable before the
				// backend began its potentially long-running invocation.
				observed := readEmittedEvents(t, eventsDir)
				if len(observed) != 1 || observed[0].Type != events.TaskClaimed {
					t.Fatalf("events at invocation = %+v, want one %s", observed, events.TaskClaimed)
				}
				data, err := observed[0].DecodeData()
				if err != nil {
					t.Fatalf("decode claimed event at invocation: %v", err)
				}
				claimed, ok := data.(*events.TaskClaimedData)
				if !ok || claimed.TaskID != "loomcli-02" {
					t.Fatalf("claimed data at invocation = %#v, want TaskID loomcli-02", data)
				}
				return tt.invokeErr
			}
			RegisterBackend(backend)
			if err := SetBackend(backend.Name()); err != nil {
				t.Fatalf("SetBackend: %v", err)
			}

			err := runAgentDaemon(worktree, "reviewer-1", func(string, *config.WorkspaceConfig) string {
				return "review the assigned task"
			})
			if !errors.Is(err, tt.invokeErr) {
				t.Fatalf("runAgentDaemon() error = %v, want %v", err, tt.invokeErr)
			}

			// Flush the process-wide writer before asserting its JSONL output.
			cli.TestingResetAgentEventBus()
			evts := readEmittedEvents(t, eventsDir)
			if len(evts) != 2 {
				t.Fatalf("expected claimed and terminal events, got %d: %+v", len(evts), evts)
			}
			if evts[0].Type != events.TaskClaimed || evts[1].Type != tt.terminal {
				t.Fatalf("event types = [%s, %s], want [%s, %s]",
					evts[0].Type, evts[1].Type, events.TaskClaimed, tt.terminal)
			}
			if evts[0].Agent != "reviewer-1" || evts[1].Agent != "reviewer-1" {
				t.Errorf("event agents = [%q, %q], want reviewer-1", evts[0].Agent, evts[1].Agent)
			}

			claimedData, err := evts[0].DecodeData()
			if err != nil {
				t.Fatalf("decode claimed event: %v", err)
			}
			claimed, ok := claimedData.(*events.TaskClaimedData)
			if !ok || claimed.TaskID != "loomcli-02" {
				t.Fatalf("claimed data = %#v, want TaskID loomcli-02", claimedData)
			}

			terminalData, err := evts[1].DecodeData()
			if err != nil {
				t.Fatalf("decode terminal event: %v", err)
			}
			switch data := terminalData.(type) {
			case *events.TaskCompletedData:
				if data.TaskID != "loomcli-02" {
					t.Errorf("completed TaskID = %q, want loomcli-02", data.TaskID)
				}
				if data.Duration.Duration <= 0 {
					t.Errorf("completed duration = %s, want > 0", data.Duration.Duration)
				}
			case *events.TaskFailedData:
				if data.TaskID != "loomcli-02" {
					t.Errorf("failed TaskID = %q, want loomcli-02", data.TaskID)
				}
				if data.Error != tt.wantErrMsg {
					t.Errorf("failed error = %q, want %q", data.Error, tt.wantErrMsg)
				}
				if data.ErrorClass == "" {
					t.Error("failed ErrorClass is empty")
				}
			default:
				t.Fatalf("terminal data type = %T, want completed or failed data", terminalData)
			}
		})
	}
}

func TestEmitTaskLifecycleResult_EmitsCompletedOnlyAfterClaimReleased(t *testing.T) {
	tests := []struct {
		name            string
		status          string
		wantCompleted   int
		flushBeforeRead bool
	}{
		{
			name:            "claim still held",
			status:          "in_progress",
			wantCompleted:   0,
			flushBeforeRead: true,
		},
		{
			name:          "claim released",
			status:        "closed",
			wantCompleted: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventsDir := t.TempDir()
			t.Setenv("LOOM_EVENTS_DIR", eventsDir)
			cli.TestingResetAgentEventBus()
			t.Cleanup(cli.TestingResetAgentEventBus)

			worktree := t.TempDir()
			writeLockFile(t, worktree, "loomcli-02")
			issueBackend := NewMockIssueBackend()
			issueBackend.GetResult = &backend.IssueDetailData{
				IssueData: backend.IssueData{
					ID:       "loomcli-02",
					Status:   tt.status,
					Assignee: "plan-agent",
				},
			}
			setDefaultIssueBackend(issueBackend)
			t.Cleanup(resetDefaultIssueBackend)

			emitTaskLifecycleResult("plan-agent", worktree, time.Now().Add(-time.Second), nil)
			if tt.flushBeforeRead {
				cli.TestingResetAgentEventBus()
			}

			var completed int
			for _, evt := range readEmittedEvents(t, eventsDir) {
				if evt.Type == events.TaskCompleted {
					completed++
				}
			}
			if completed != tt.wantCompleted {
				t.Fatalf("TaskCompleted count = %d, want %d", completed, tt.wantCompleted)
			}
		})
	}
}

const seedCustomDaemonResumeLockEnv = "LOOM_TEST_SEED_CUSTOM_DAEMON_RESUME_LOCK"

func TestRunAgentDaemonResumesAndClearsCompletedSession(t *testing.T) {
	if os.Getenv(seedCustomDaemonResumeLockEnv) == "1" {
		worktree := os.Getenv("LOOM_TEST_RESUME_WORKTREE")
		if err := cli.AcquireLock(worktree, "agent", "reviewer-1"); err != nil {
			t.Fatalf("seed AcquireLock: %v", err)
		}
		if err := cli.UpdateLockTask(worktree, "loomcli-02", ""); err != nil {
			t.Fatalf("seed UpdateLockTask: %v", err)
		}
		if err := cli.UpdateLockClaudeSessionID(worktree, "claude-resume-02"); err != nil {
			t.Fatalf("seed UpdateLockClaudeSessionID: %v", err)
		}
		return // TestMain exits, leaving this process-owned lock stale for the parent.
	}

	worktree := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunAgentDaemonResumesAndClearsCompletedSession$") //nolint:norawexec // subprocess produces a genuine stale lock
	cmd.Env = append(os.Environ(),
		seedCustomDaemonResumeLockEnv+"=1",
		"LOOM_TEST_RESUME_WORKTREE="+worktree,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed stale daemon lock: %v\n%s", err, out)
	}

	t.Setenv("LOOM_ASSIGNED_TASK_ID", "loomcli-02")
	t.Setenv("LOOM_EVENTS_DIR", filepath.Join(t.TempDir(), "events"))
	t.Setenv("LOOM_SESSION_ID", "supervisor-session")
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", filepath.Join(t.TempDir(), "runtime"))
	cli.TestingResetAgentEventBus()
	t.Cleanup(cli.TestingResetAgentEventBus)
	backends.ClearResumeSessionID()
	t.Cleanup(backends.ClearResumeSessionID)

	completedTask := NewMockIssueBackend()
	completedTask.GetResult = &backend.IssueDetailData{
		IssueData: backend.IssueData{ID: "loomcli-02", Status: "closed", Assignee: "reviewer-1"},
	}
	setDefaultIssueBackend(completedTask)
	t.Cleanup(resetDefaultIssueBackend)

	resetBackendState(t)
	mock := &mockBackend{name: "custom-role-resume"}
	mock.nonInteractiveFunc = func(string, string, string, <-chan struct{}, *usage.Collector) error {
		if got := backends.GetResumeSessionID(); got != "claude-resume-02" {
			t.Errorf("armed resume session = %q, want claude-resume-02", got)
		}
		return nil
	}
	RegisterBackend(mock)
	if err := SetBackend(mock.Name()); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}

	if err := runAgentDaemon(worktree, "reviewer-1", func(string, *config.WorkspaceConfig) string {
		return "review the assigned task"
	}); err != nil {
		t.Fatalf("runAgentDaemon: %v", err)
	}
	info, err := cli.ReadLockFile(worktree)
	if err != nil || info == nil {
		t.Fatalf("ReadLockFile: info=%+v err=%v", info, err)
	}
	if info.ClaudeSessionID != "" {
		t.Errorf("ClaudeSessionID = %q, want cleared after completed run", info.ClaudeSessionID)
	}
}

// readEmittedEvents reads every JSONL line under dir and decodes each into
// an events.Event.
func readEmittedEvents(t *testing.T, dir string) []events.Event {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read events dir: %v", err)
	}
	var out []events.Event
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, ent.Name()))
		if err != nil {
			t.Fatalf("open jsonl: %v", err)
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var evt events.Event
			if err := json.Unmarshal(line, &evt); err != nil {
				_ = f.Close()
				t.Fatalf("unmarshal event: %v (line %q)", err, string(line))
			}
			out = append(out, evt)
		}
		_ = f.Close()
	}
	return out
}

// writeLockFile drops a minimal LockInfo into worktree/.loom-agent.lock so
// emitTaskLifecycleResult's cli.ReadLockFile call recovers the task id the
// way a real agent run would after a self-claim.
func writeLockFile(t *testing.T, worktree, taskID string) {
	t.Helper()
	info := cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "plan-agent",
		TaskID:    taskID,
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, cli.LockFileName), data, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}
