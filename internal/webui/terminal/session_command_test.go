package terminal

import (
	"errors"
	"reflect"
	"testing"
)

func TestLaunchSpecForBackendBuildsDurableEnvelope(t *testing.T) {
	oldExecutable := currentExecutable
	currentExecutable = func() (string, error) {
		return "/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin", nil
	}
	t.Cleanup(func() { currentExecutable = oldExecutable })

	tests := []struct {
		name    string
		backend string
		want    []string
	}{
		{"shell", "shell", []string{"-l"}},
		{"claude", "claude", []string{"-c", "'/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin' lead --backend claude"}},
		{"codex", "codex", []string{"-c", "'/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin' lead --backend codex"}},
		{"opencode", "opencode", []string{"-c", "'/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin' lead --backend opencode"}},
		{"gemini", "gemini", []string{"-c", "'/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin' lead --backend gemini"}},
		{"cursor", "cursor", []string{"-c", "'/Applications/Loom.app/Contents/MacOS/loom-aarch64-apple-darwin' lead --backend cursor"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LaunchSpecForBackend(tt.backend, "/trusted/loom-data")
			if err != nil {
				t.Fatalf("LaunchSpecForBackend: %v", err)
			}
			if !reflect.DeepEqual(got.Argv, tt.want) {
				t.Errorf("Argv = %v, want %v", got.Argv, tt.want)
			}
			if got.Env["LOOM_CONFIG_DIR"] != "/trusted/loom-data" {
				t.Errorf("LOOM_CONFIG_DIR = %q", got.Env["LOOM_CONFIG_DIR"])
			}
		})
	}
}

func TestLaunchSpecForBackendRejectsUnknownIntent(t *testing.T) {
	for _, backend := range []string{"", "unknown", "codex-1"} {
		launch, err := LaunchSpecForBackend(backend, "/trusted/loom-data")
		if launch != nil || err == nil {
			t.Fatalf("LaunchSpecForBackend(%q) = (%#v, %v), want rejection", backend, launch, err)
		}
	}
}

func TestLaunchSpecForBackendUsesLoomCommandWhenExecutableUnavailable(t *testing.T) {
	oldExecutable := currentExecutable
	currentExecutable = func() (string, error) {
		return "", errors.New("boom")
	}
	t.Cleanup(func() { currentExecutable = oldExecutable })

	got, err := LaunchSpecForBackend("codex", "")
	if err != nil {
		t.Fatalf("LaunchSpecForBackend: %v", err)
	}
	want := []string{"-c", "'loom' lead --backend codex"}
	if !reflect.DeepEqual(got.Argv, want) {
		t.Fatalf("Argv = %v, want %v", got.Argv, want)
	}
	if got.Env != nil {
		t.Fatalf("Env = %#v, want nil", got.Env)
	}
}

func TestLaunchSpecForBackendQuotesExecutablePath(t *testing.T) {
	oldExecutable := currentExecutable
	currentExecutable = func() (string, error) {
		return "/tmp/Loom's App/loom", nil
	}
	t.Cleanup(func() { currentExecutable = oldExecutable })

	got, err := LaunchSpecForBackend("codex", "")
	if err != nil {
		t.Fatalf("LaunchSpecForBackend: %v", err)
	}
	want := []string{"-c", "'/tmp/Loom'\\''s App/loom' lead --backend codex"}
	if !reflect.DeepEqual(got.Argv, want) {
		t.Fatalf("Argv = %v, want %v", got.Argv, want)
	}
}

func TestIsValidTerminalBackend(t *testing.T) {
	for _, backend := range []string{"shell", "claude", "codex", "opencode", "gemini", "cursor"} {
		if !IsValidTerminalBackend(backend) {
			t.Errorf("IsValidTerminalBackend(%q) = false", backend)
		}
	}
	for _, backend := range []string{"", "unknown", "codex-1"} {
		if IsValidTerminalBackend(backend) {
			t.Errorf("IsValidTerminalBackend(%q) = true", backend)
		}
	}
}
