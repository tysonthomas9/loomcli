package agentprofiles

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestConfiguredAgentNames(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares a runtime dir and returns it.
		setup func(t *testing.T) string
		want  []string
	}{
		{
			name: "populated profiles dir",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				mkProfiles(t, dir, "worker", "worker-2", "tester")
				return dir
			},
			want: []string{"tester", "worker", "worker-2"},
		},
		{
			name: "underscore-prefixed names are excluded",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				mkProfiles(t, dir, "_templates", "_scratch", "worker")
				return dir
			},
			want: []string{"worker"},
		},
		{
			name: "plain files are ignored",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				mkProfiles(t, dir, "worker")
				if err := os.WriteFile(filepath.Join(dir, "profiles", "README.md"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: []string{"worker"},
		},
		{
			name: "empty profiles dir yields nil",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				mkProfiles(t, dir)
				return dir
			},
			want: nil,
		},
		{
			name:  "missing profiles dir yields nil",
			setup: func(t *testing.T) string { return t.TempDir() },
			want:  nil,
		},
		{
			name:  "empty runtime dir yields nil",
			setup: func(t *testing.T) string { return "" },
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfiguredAgentNames(tt.setup(t))
			sort.Strings(got)
			if len(got) != len(tt.want) {
				t.Fatalf("ConfiguredAgentNames = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ConfiguredAgentNames = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestConfiguredAgentNamesUnreadable pins the contract that matters most: an
// unreadable profiles dir returns nil ("do not filter"), so a misconfigured
// workspace degrades to unfiltered behavior instead of reporting zero runs.
func TestConfiguredAgentNamesUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on windows")
	}
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	mkProfiles(t, dir, "worker")
	if err := os.Chmod(profiles, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(profiles, 0o700) })

	if got := ConfiguredAgentNames(dir); got != nil {
		t.Fatalf("ConfiguredAgentNames = %v, want nil for an unreadable dir", got)
	}
}

func mkProfiles(t *testing.T, runtimeDir string, agents ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(runtimeDir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if err := os.MkdirAll(filepath.Join(runtimeDir, "profiles", agent), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}
