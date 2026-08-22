package backends

import (
	"context"
	"strings"
	"testing"
)

// The controlled lead runtime used to build its argv from scratch, hardcoding
// each backend's permissive flag, so an interactive role carrying read_only or
// a tool list got the restriction on the daemon and agent invoker paths and
// silently lost it here. These pin the join.

func TestControlledLead_ClaudeCarriesTheToolKnobs(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv("LOOM_ALLOWED_TOOLS", "Read,Grep")
	t.Setenv("LOOM_DENIED_TOOLS", "WebSearch")
	captured := installFakeHarnessLead(t)

	handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "lead-session", "/repo", "prompt", "claude")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v, want a handled launch", handled, err)
	}
	joined := strings.Join(captured.Args, " ")
	if !strings.Contains(joined, "--allowedTools Read,Grep") {
		t.Errorf("args = %q, want the allowlist applied", joined)
	}
	if !strings.Contains(joined, "--disallowedTools WebSearch") {
		t.Errorf("args = %q, want the denylist applied", joined)
	}
}

func TestControlledLead_ClaudeReadOnlyDeniesTheWriteTools(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv("LOOM_READ_ONLY", "1")
	captured := installFakeHarnessLead(t)

	if _, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "s", "/repo", "prompt", "claude"); err != nil {
		t.Fatalf("RunControlledLeadRuntime: %v", err)
	}
	if joined := strings.Join(captured.Args, " "); !strings.Contains(joined, "--disallowedTools Write,Edit,NotebookEdit,Bash") {
		t.Fatalf("args = %q, want the read_only deny-set", joined)
	}
}

// gemini's read-only posture is an approval mode, so read_only has to select
// it here exactly as it does on the interactive path — otherwise the lead
// launches in yolo while the role says read-only.
func TestControlledLead_GeminiReadOnlySelectsPlanMode(t *testing.T) {
	clearSafetyEnv(t)
	captured := installFakeHarnessLead(t)
	if _, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "s", "/repo", "prompt", "gemini"); err != nil {
		t.Fatalf("RunControlledLeadRuntime: %v", err)
	}
	if joined := strings.Join(captured.Args, " "); joined != "--approval-mode=yolo" {
		t.Fatalf("default gemini lead args = %q", joined)
	}

	t.Setenv("LOOM_READ_ONLY", "1")
	captured = installFakeHarnessLead(t)
	if _, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "s", "/repo", "prompt", "gemini"); err != nil {
		t.Fatalf("RunControlledLeadRuntime: %v", err)
	}
	if joined := strings.Join(captured.Args, " "); joined != "--approval-mode=plan" {
		t.Fatalf("read_only gemini lead args = %q, want plan", joined)
	}
}

// A tool list on a backend with no tool vocabulary must refuse the launch, and
// it must refuse it as HANDLED. Returning not-handled would send the caller to
// the plain interactive fallback — an unrestricted launch, i.e. the exact
// outcome the refusal exists to prevent.
func TestControlledLead_UnenforceableKnobRefusesWithoutFallback(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv("LOOM_DENIED_TOOLS", "Bash")
	installFakeHarnessLead(t)

	handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "s", "/repo", "prompt", "cursor")
	if err == nil {
		t.Fatal("denied_tools on cursor must refuse the launch")
	}
	if !handled {
		t.Fatal("a refusal must be reported as handled, or the caller falls back to an unrestricted launch")
	}
	if !strings.Contains(err.Error(), "denied_tools") {
		t.Fatalf("err = %v, want it to name the knob", err)
	}
}

// read_only on a backend with no hard mechanism is soft, not fatal — the same
// degradation the supervisor gate applies — so the lead still launches.
func TestControlledLead_SoftReadOnlyStillLaunches(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv("LOOM_READ_ONLY", "1")
	installFakeHarnessLead(t)
	captured := installFakeHeadlessLead(t) // cursor leads run headless

	handled, err := RunControlledLeadRuntime(context.Background(), nil, "WS", "nova", "s", "/repo", "prompt", "cursor")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v, want a handled launch", handled, err)
	}
	if captured.BinaryPath != "cursor-agent" {
		t.Fatalf("BinaryPath = %q", captured.BinaryPath)
	}
}
