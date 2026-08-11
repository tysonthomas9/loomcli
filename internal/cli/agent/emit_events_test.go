package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/events"
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

	// 1. TaskClaimed (the daemon-mode emit path; LOOM_ASSIGNED_TASK_ID
	// would be the source in production).
	emitTaskClaimedFromEnv(t.Context(), "plan-agent", "loomcli-42")

	// 2. TaskCompleted on success — duration carried, task id recovered
	// from the lock file written by the simulated `loom claim`.
	emitTaskLifecycleResult(t.Context(), "plan-agent", worktree, time.Now().Add(-2*time.Second), nil)

	// 3. TaskFailed on error — classifier kicks in, ErrorClass populated.
	emitTaskLifecycleResult(t.Context(), "plan-agent", worktree, time.Now().Add(-1*time.Second), errors.New("simulated agent failure"))

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
