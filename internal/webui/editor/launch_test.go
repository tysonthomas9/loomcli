package editor

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// mockCommand returns a newCommandFn replacement that records the invocation
// and returns a Cmd whose Start either succeeds or fails based on startErr.
func mockCommand(recorded *[]string, startErr error) func(name string, args ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		*recorded = append(*recorded, name)
		*recorded = append(*recorded, args...)

		// Build a real Cmd that will either succeed or fail on Start.
		if startErr != nil {
			// Use a non-existent binary to force Start to fail.
			return exec.Command("__does_not_exist_launch_test__") //nolint:norawexec
		}
		// "true" is a no-op binary that exits immediately.
		return exec.Command("true") //nolint:norawexec
	}
}

func TestLaunchCLIMethod(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, nil)

	de := DetectedEditor{
		Editor:       Editor{ID: "vscode", DisplayName: "VS Code", IconName: "vscode"},
		ResolvedPath: "/usr/local/bin/code",
		Method:       "cli",
	}

	err := LaunchEditor(de, []string{"/tmp/foo.go", "/tmp/bar.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CLI method should call: resolvedPath, targets...
	want := []string{"/usr/local/bin/code", "/tmp/foo.go", "/tmp/bar.go"}
	if len(recorded) != len(want) {
		t.Fatalf("recorded args = %v, want %v", recorded, want)
	}
	for i, w := range want {
		if recorded[i] != w {
			t.Errorf("recorded[%d] = %q, want %q", i, recorded[i], w)
		}
	}
}

func TestLaunchAppMethodDarwin(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, nil)

	de := DetectedEditor{
		Editor:       Editor{ID: "vscode", DisplayName: "VS Code", IconName: "vscode"},
		ResolvedPath: "/Applications/Visual Studio Code.app",
		Method:       "app",
	}

	err := LaunchEditor(de, []string{"/tmp/project"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Darwin app method should call: "open", "-a", resolvedPath, targets...
	want := []string{"open", "-a", "/Applications/Visual Studio Code.app", "/tmp/project"}
	if len(recorded) != len(want) {
		t.Fatalf("recorded args = %v, want %v", recorded, want)
	}
	for i, w := range want {
		if recorded[i] != w {
			t.Errorf("recorded[%d] = %q, want %q", i, recorded[i], w)
		}
	}
}

func TestLaunchUnknownMethodReturnsError(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, nil)

	de := DetectedEditor{
		Editor:       Editor{ID: "myeditor", DisplayName: "My Editor", IconName: "myeditor"},
		ResolvedPath: "/usr/bin/myeditor",
		Method:       "magic",
	}

	err := LaunchEditor(de, []string{"/tmp/file.go"})
	if err == nil {
		t.Fatal("expected error for unknown method, got nil")
	}
	if !strings.Contains(err.Error(), "myeditor") {
		t.Errorf("error should contain editor ID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "magic") {
		t.Errorf("error should contain unknown method, got: %v", err)
	}

	// No command should have been created for an unknown method.
	if len(recorded) != 0 {
		t.Errorf("no command should be recorded for unknown method, got: %v", recorded)
	}
}

func TestLaunchStartFailureReturnsWrappedError(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, errors.New("injected"))

	de := DetectedEditor{
		Editor:       Editor{ID: "broken", DisplayName: "Broken", IconName: "broken"},
		ResolvedPath: "/usr/bin/broken",
		Method:       "cli",
	}

	err := LaunchEditor(de, []string{"/tmp/file.go"})
	if err == nil {
		t.Fatal("expected error when cmd.Start fails, got nil")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error should contain editor ID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to launch") {
		t.Errorf("error should contain 'failed to launch', got: %v", err)
	}
}

func TestLaunchEmptyTargets(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, nil)

	de := DetectedEditor{
		Editor:       Editor{ID: "vscode", DisplayName: "VS Code", IconName: "vscode"},
		ResolvedPath: "/usr/local/bin/code",
		Method:       "cli",
	}

	err := LaunchEditor(de, []string{})
	if err != nil {
		t.Fatalf("unexpected error with empty targets: %v", err)
	}

	// CLI with empty targets: just the resolved path, no additional args.
	want := []string{"/usr/local/bin/code"}
	if len(recorded) != len(want) {
		t.Fatalf("recorded args = %v, want %v", recorded, want)
	}
	if recorded[0] != want[0] {
		t.Errorf("recorded[0] = %q, want %q", recorded[0], want[0])
	}
}

func TestLaunchEmptyTargetsAppMethod(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, nil)

	de := DetectedEditor{
		Editor:       Editor{ID: "vscode", DisplayName: "VS Code", IconName: "vscode"},
		ResolvedPath: "/Applications/Visual Studio Code.app",
		Method:       "app",
	}

	err := LaunchEditor(de, []string{})
	if err != nil {
		t.Fatalf("unexpected error with empty targets: %v", err)
	}

	// Darwin app with empty targets: "open", "-a", resolvedPath.
	want := []string{"open", "-a", "/Applications/Visual Studio Code.app"}
	if len(recorded) != len(want) {
		t.Fatalf("recorded args = %v, want %v", recorded, want)
	}
	for i, w := range want {
		if recorded[i] != w {
			t.Errorf("recorded[%d] = %q, want %q", i, recorded[i], w)
		}
	}
}

func TestLaunchNilTargets(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, nil)

	de := DetectedEditor{
		Editor:       Editor{ID: "vim", DisplayName: "Vim", IconName: "vim"},
		ResolvedPath: "/usr/bin/vim",
		Method:       "cli",
	}

	err := LaunchEditor(de, nil)
	if err != nil {
		t.Fatalf("unexpected error with nil targets: %v", err)
	}

	want := []string{"/usr/bin/vim"}
	if len(recorded) != len(want) {
		t.Fatalf("recorded args = %v, want %v", recorded, want)
	}
	if recorded[0] != want[0] {
		t.Errorf("recorded[0] = %q, want %q", recorded[0], want[0])
	}
}

func TestLaunchAppMethodMultipleTargets(t *testing.T) {
	orig := newCommandFn
	t.Cleanup(func() { newCommandFn = orig })

	var recorded []string
	newCommandFn = mockCommand(&recorded, nil)

	de := DetectedEditor{
		Editor:       Editor{ID: "sublime", DisplayName: "Sublime Text", IconName: "sublime"},
		ResolvedPath: "/Applications/Sublime Text.app",
		Method:       "app",
	}

	targets := []string{"/tmp/a.go", "/tmp/b.go", "/tmp/c.go"}
	err := LaunchEditor(de, targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"open", "-a", "/Applications/Sublime Text.app", "/tmp/a.go", "/tmp/b.go", "/tmp/c.go"}
	if len(recorded) != len(want) {
		t.Fatalf("recorded args = %v, want %v", recorded, want)
	}
	for i, w := range want {
		if recorded[i] != w {
			t.Errorf("recorded[%d] = %q, want %q", i, recorded[i], w)
		}
	}
}
