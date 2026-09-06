package backends

import (
	"strings"
	"testing"
)

// role.model used to be stored, displayed, and shipped over the wire — and
// read by nothing on the daemon path. These tests pin the LOOM_AGENT_MODEL
// plumbing end to end at the arg-builder level for each backend.

func TestClaudeInteractiveArgs_CarryModel(t *testing.T) {
	t.Setenv("LOOM_AGENT_MODEL", "claude-sonnet-5")
	t.Setenv("LOOM_AGENT_EFFORT", "")
	t.Setenv("LOOM_CLAUDE_EFFORT", "")
	cmd := buildClaudeInteractiveCmd("/tmp/wd", "p", "a")
	got := strings.Join(cmd.Args, " ")
	if !strings.Contains(got, "--model claude-sonnet-5") {
		t.Fatalf("claude interactive args = %q, want --model claude-sonnet-5", got)
	}
}

func TestClaudeInteractiveArgs_NoModelWhenUnset(t *testing.T) {
	t.Setenv("LOOM_AGENT_MODEL", "")
	// The builder now falls back to the profile's provisioned baseline, so
	// "unset" means both inputs — otherwise this asserts on whatever profile
	// the shell running the suite happens to be pointed at.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	cmd := buildClaudeInteractiveCmd("/tmp/wd", "p", "a")
	if got := strings.Join(cmd.Args, " "); strings.Contains(got, "--model") {
		t.Fatalf("claude interactive args = %q, want no --model", got)
	}
}

// The interactive builder also serves the lead's LOOM_LEAD_CONTROLLED=0
// fallback, so it must reach the same launch state as harnessLeadInvocation.
func TestClaudeInteractiveArgs_CarryProvisionedModel(t *testing.T) {
	t.Setenv("LOOM_AGENT_MODEL", "")
	t.Setenv("LOOM_AGENT_EFFORT", "")
	t.Setenv("LOOM_CLAUDE_EFFORT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", writePinnedProfile(t, "settings.json", `{"model":"opus[1m]"}`))
	cmd := buildClaudeInteractiveCmd("/tmp/wd", "p", "a")
	if got := strings.Join(cmd.Args, " "); !strings.Contains(got, "--model opus[1m]") {
		t.Fatalf("claude interactive args = %q, want --model opus[1m]", got)
	}
}

func TestAppendCodexModelArgs(t *testing.T) {
	got := strings.Join(appendCodexModelArgs([]string{"exec", "--json"}, "gpt-5.3-codex"), " ")
	want := `-c model="gpt-5.3-codex" exec --json`
	if got != want {
		t.Fatalf("appendCodexModelArgs = %q, want %q", got, want)
	}
	if out := appendCodexModelArgs([]string{"exec"}, ""); strings.Join(out, " ") != "exec" {
		t.Fatalf("empty model must be a no-op, got %q", out)
	}
}

func TestOpenCodeModelArgs_FallsBackToAgentModel(t *testing.T) {
	t.Setenv("LOOM_OPENCODE_MODEL", "")
	t.Setenv("LOOM_AGENT_MODEL", "some/model")
	got := strings.Join(openCodeModelArgs(), " ")
	if got != "--model some/model" {
		t.Fatalf("openCodeModelArgs = %q, want --model some/model", got)
	}
	// The opencode-specific var still wins over the generic one.
	t.Setenv("LOOM_OPENCODE_MODEL", "specific/model")
	if got := strings.Join(openCodeModelArgs(), " "); got != "--model specific/model" {
		t.Fatalf("openCodeModelArgs = %q, want the opencode-specific override", got)
	}
}
