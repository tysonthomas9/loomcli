//go:build darwin

package editor

import (
	"testing"
)

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
