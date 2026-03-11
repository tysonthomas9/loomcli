package cli

import (
	"fmt"
	"testing"
)

// stubExecCommand replaces execCommand for the duration of the test.
func stubExecCommand(t *testing.T, fn commandExecutor) {
	t.Helper()
	orig := execCommand
	execCommand = fn
	t.Cleanup(func() { execCommand = orig })
}

func TestDefaultQueryOpenEpics_ParsesValidJSON(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: `[{"id":"epic-1","title":"Auth epic","priority":0,"issue_type":"epic","status":"open"},{"id":"epic-2","title":"Billing epic","priority":2,"issue_type":"epic","status":"open"}]`,
		}
	})

	epics, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(epics) != 2 {
		t.Fatalf("expected 2 epics, got %d", len(epics))
	}
	if epics[0].ID != "epic-1" {
		t.Errorf("epics[0].ID = %q, want epic-1", epics[0].ID)
	}
	if epics[0].Priority != 0 {
		t.Errorf("epics[0].Priority = %d, want 0", epics[0].Priority)
	}
	if epics[1].ID != "epic-2" {
		t.Errorf("epics[1].ID = %q, want epic-2", epics[1].ID)
	}
	if epics[1].Priority != 2 {
		t.Errorf("epics[1].Priority = %d, want 2", epics[1].Priority)
	}
}

func TestDefaultQueryOpenEpics_EmptyArray(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: `[]`}
	})

	epics, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(epics) != 0 {
		t.Errorf("expected empty slice, got %d epics", len(epics))
	}
}

func TestDefaultQueryOpenEpics_InvalidJSON(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "not valid json"}
	})

	epics, err := defaultQueryOpenEpics()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if epics != nil {
		t.Errorf("expected nil slice, got %v", epics)
	}
	assertContains(t, err.Error(), "failed to parse")
}

func TestDefaultQueryOpenEpics_ExecCommandFails(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{Err: fmt.Errorf("bd not found")}
	})

	epics, err := defaultQueryOpenEpics()
	if err == nil {
		t.Fatal("expected error when exec fails")
	}
	if epics != nil {
		t.Errorf("expected nil slice, got %v", epics)
	}
	assertContains(t, err.Error(), "bd list failed")
}

func TestDefaultQueryOpenEpics_CommandArgs(t *testing.T) {
	var capturedName string
	var capturedArgs []string

	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		capturedName = name
		capturedArgs = args
		return CommandResult{Stdout: `[]`}
	})

	defaultQueryOpenEpics()

	if capturedName != "bd" {
		t.Errorf("command name = %q, want bd", capturedName)
	}
	expectedArgs := []string{"list", "--type=epic", "--status=open", "--json", "--limit", "0"}
	if len(capturedArgs) != len(expectedArgs) {
		t.Fatalf("args length = %d, want %d: %v", len(capturedArgs), len(expectedArgs), capturedArgs)
	}
	for i, arg := range expectedArgs {
		if capturedArgs[i] != arg {
			t.Errorf("args[%d] = %q, want %q", i, capturedArgs[i], arg)
		}
	}
}

func TestDefaultEpicHasReadyTasks_HasTasks(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: `[{"id":"task-1","title":"Implement auth","priority":1,"issue_type":"task","status":"open"}]`,
		}
	})

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Error("expected true, got false")
	}
}

func TestDefaultEpicHasReadyTasks_NoTasks(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: `[]`}
	})

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if has {
		t.Error("expected false, got true")
	}
}

func TestDefaultEpicHasReadyTasks_InvalidJSON(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "not json"}
	})

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if has {
		t.Error("expected false on error")
	}
}

func TestDefaultEpicHasReadyTasks_ExecCommandFails(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{Err: fmt.Errorf("bd not found")}
	})

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err == nil {
		t.Fatal("expected error when exec fails")
	}
	if has {
		t.Error("expected false on error")
	}
	assertContains(t, err.Error(), "bd ready failed")
}

func TestDefaultEpicHasReadyTasks_CommandArgs(t *testing.T) {
	var capturedName string
	var capturedArgs []string

	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		capturedName = name
		capturedArgs = args
		return CommandResult{Stdout: `[]`}
	})

	defaultEpicHasReadyTasks("epic-xyz")

	if capturedName != "bd" {
		t.Errorf("command name = %q, want bd", capturedName)
	}
	expectedArgs := []string{"ready", "--parent", "epic-xyz", "--json", "--limit", "1"}
	if len(capturedArgs) != len(expectedArgs) {
		t.Fatalf("args length = %d, want %d: %v", len(capturedArgs), len(expectedArgs), capturedArgs)
	}
	for i, arg := range expectedArgs {
		if capturedArgs[i] != arg {
			t.Errorf("args[%d] = %q, want %q", i, capturedArgs[i], arg)
		}
	}
}

func TestDefaultQueryOpenEpics_UnknownFieldsForwardCompat(t *testing.T) {
	stubExecCommand(t, func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: `[{"id":"epic-1","title":"Auth","priority":1,"issue_type":"epic","status":"open","some_future_field":"value","nested":{"a":1}}]`,
		}
	})

	epics, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error with unknown fields: %v", err)
	}
	if len(epics) != 1 {
		t.Fatalf("expected 1 epic, got %d", len(epics))
	}
	if epics[0].ID != "epic-1" {
		t.Errorf("ID = %q, want epic-1", epics[0].ID)
	}
}

// assertContains is a test helper that checks if s contains substr.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if len(s) == 0 || len(substr) == 0 {
		t.Errorf("assertContains: empty string or substr")
		return
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Errorf("expected %q to contain %q", s, substr)
}
