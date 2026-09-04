package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/events"
)

const (
	exitWithFlushBusHelperEnv = "LOOM_TEST_EXIT_WITH_FLUSH_BUS"
	executeBusHelperEnv       = "LOOM_TEST_EXECUTE_CLOSES_BUS"
)

func TestExitWithFlushClosesAgentEventBus(t *testing.T) {
	if os.Getenv(exitWithFlushBusHelperEnv) == "1" {
		emitAgentBusFlushTestEvent(t)
		ExitWithFlush(23)
		return
	}

	eventsDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitWithFlushClosesAgentEventBus$") //nolint:norawexec // subprocess asserts os.Exit durability
	cmd.Env = agentBusChildEnv(eventsDir, exitWithFlushBusHelperEnv)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("ExitWithFlush subprocess error = %v, want exit code 23", err)
	}
	assertAgentEventBusFlushed(t, eventsDir)
}

func TestExecuteClosesAgentEventBusOnNormalReturn(t *testing.T) {
	if os.Getenv(executeBusHelperEnv) == "1" {
		emitAgentBusFlushTestEvent(t)
		os.Args = []string{os.Args[0], "--help"}
		if err := Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return
	}

	eventsDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecuteClosesAgentEventBusOnNormalReturn$") //nolint:norawexec // subprocess isolates root command globals
	cmd.Env = agentBusChildEnv(eventsDir, executeBusHelperEnv)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Execute subprocess: %v\n%s", err, out)
	}
	assertAgentEventBusFlushed(t, eventsDir)
}

func TestCloseAgentEventBusIsIdempotent(t *testing.T) {
	TestingResetAgentEventBus()
	t.Cleanup(TestingResetAgentEventBus)
	eventsDir := t.TempDir()
	t.Setenv("LOOM_EVENTS_DIR", eventsDir)

	emitAgentBusFlushTestEvent(t)
	CloseAgentEventBus(context.Background())
	CloseAgentEventBus(context.Background())

	assertAgentEventBusFlushed(t, eventsDir)
}

func emitAgentBusFlushTestEvent(t *testing.T) {
	t.Helper()
	bus := AgentEventBus()
	if bus == nil {
		t.Fatal("AgentEventBus returned nil")
	}
	event, err := events.NewEvent(events.TaskClaimed, "flush-test-agent", "", "", events.TaskClaimedData{TaskID: "flush-test-task"})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if err := bus.Emit(event); err != nil {
		t.Fatalf("AgentEventBus.Emit: %v", err)
	}
}

func agentBusChildEnv(eventsDir, helper string) []string {
	return append(os.Environ(),
		"LOOM_EVENTS_DIR="+eventsDir,
		helper+"=1",
		"LOOM_TRACE=",
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
	)
}

func assertAgentEventBusFlushed(t *testing.T, eventsDir string) {
	t.Helper()
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		t.Fatalf("read events dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat event log: %v", err)
		}
		if info.Size() > 0 {
			return
		}
	}
	t.Fatalf("no flushed JSONL event found in %s", filepath.Clean(eventsDir))
}
