package interaction

import (
	"reflect"
	"testing"
)

func TestLaunchSpecForBackendBuildsDurableEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		want    []string
	}{
		{"shell", "shell", []string{"-l"}},
		{"claude", "claude", []string{"-c", "'loom' lead --backend claude"}},
		{"codex", "codex", []string{"-c", "'loom' lead --backend codex"}},
		{"opencode", "opencode", []string{"-c", "'loom' lead --backend opencode"}},
		{"gemini", "gemini", []string{"-c", "'loom' lead --backend gemini"}},
		{"cursor", "cursor", []string{"-c", "'loom' lead --backend cursor"}},
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

func TestLaunchSpecForBackendUsesPrivateAdapterPinnedLoomCommand(t *testing.T) {
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
