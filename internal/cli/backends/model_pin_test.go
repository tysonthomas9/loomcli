package backends

import (
	"os"
	"path/filepath"
	"testing"
)

// writePinnedProfile lays out a provisioned profile root whose managed baseline
// carries the given model, and returns the directory.
func writePinnedProfile(t *testing.T, rel, baseline string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{"files":[".provisioned/` + rel + `"],"managed":["` + rel + `"],"fingerprint":"x","harness_version":"v"}`
	if err := os.WriteFile(filepath.Join(dir, ".manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	provisioned := filepath.Join(dir, ".provisioned")
	if err := os.MkdirAll(provisioned, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(provisioned, rel), []byte(baseline), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return dir
}

// Every test in this file pins BOTH environment inputs. Agent shells export
// LOOM_AGENT_MODEL, so a test that only sets the config dir passes or fails
// depending on who runs it.
func TestPinnedClaudeModelRoleModelWins(t *testing.T) {
	dir := writePinnedProfile(t, "settings.json", `{"model":"opus[1m]"}`)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("LOOM_AGENT_MODEL", "claude-sonnet-5")
	if got := pinnedClaudeModel(); got != "claude-sonnet-5" {
		t.Fatalf("pinnedClaudeModel() = %q, want the role model", got)
	}
}

func TestPinnedClaudeModelFallsBackToBaseline(t *testing.T) {
	dir := writePinnedProfile(t, "settings.json", `{"model":"opus[1m]"}`)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("LOOM_AGENT_MODEL", "")
	if got := pinnedClaudeModel(); got != "opus[1m]" {
		t.Fatalf("pinnedClaudeModel() = %q, want the provisioned baseline value", got)
	}
}

// The workspace launcher exports a RELATIVE config dir and a lead may run from
// any directory, so the resolver absolutizes before reading.
func TestPinnedClaudeModelResolvesRelativeConfigDir(t *testing.T) {
	dir := writePinnedProfile(t, "settings.json", `{"model":"opus[1m]"}`)
	t.Chdir(filepath.Dir(dir))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Base(dir))
	t.Setenv("LOOM_AGENT_MODEL", "")
	if got := pinnedClaudeModel(); got != "opus[1m]" {
		t.Fatalf("pinnedClaudeModel() = %q, want the provisioned baseline value", got)
	}
}

func TestPinnedClaudeModelUnprofiled(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("LOOM_AGENT_MODEL", "")
	if got := pinnedClaudeModel(); got != "" {
		t.Fatalf("pinnedClaudeModel() = %q, want no pin", got)
	}
}

// A profile root that exists but was never provisioned, and one whose managed
// file is absent, both resolve to "no pin" rather than failing.
func TestPinnedClaudeModelUnprovisionedRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_AGENT_MODEL", "")
	if got := pinnedClaudeModel(); got != "" {
		t.Fatalf("pinnedClaudeModel() = %q, want no pin", got)
	}
}

func TestPinnedCodexModelFallsBackToBaseline(t *testing.T) {
	dir := writePinnedProfile(t, "config.toml", "model = \"gpt-5.6-sol\"\n")
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("LOOM_AGENT_MODEL", "")
	if got := pinnedCodexModel(); got != "gpt-5.6-sol" {
		t.Fatalf("pinnedCodexModel() = %q, want the provisioned baseline value", got)
	}
}

func TestPinnedModelForDispatch(t *testing.T) {
	claudeDir := writePinnedProfile(t, "settings.json", `{"model":"opus[1m]"}`)
	codexDir := writePinnedProfile(t, "config.toml", "model = \"gpt-5.6-sol\"\n")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CODEX_HOME", codexDir)
	t.Setenv("LOOM_AGENT_MODEL", "")

	cases := map[string]string{
		"claude": "opus[1m]",
		"CLAUDE": "opus[1m]", // the caller passes an operator-supplied backend name
		"codex":  "gpt-5.6-sol",
		"gemini": "", // no provisioned profile root, nothing to pin
		"":       "",
	}
	for backend, want := range cases {
		if got := PinnedModelFor(backend); got != want {
			t.Errorf("PinnedModelFor(%q) = %q, want %q", backend, got, want)
		}
	}
}
