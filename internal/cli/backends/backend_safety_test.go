package backends

import (
	"strings"
	"testing"
)

func clearSafetyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_ALLOWED_TOOLS", "")
	t.Setenv("LOOM_DENIED_TOOLS", "")
	t.Setenv("LOOM_READ_ONLY", "")
}

// The knobs used to be written to env and read by nobody; these pin the real
// flag mappings per backend.

func TestAppendClaudeSafetyArgs_ListsAndReadOnlyMerge(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv("LOOM_ALLOWED_TOOLS", "Read, Grep")
	t.Setenv("LOOM_DENIED_TOOLS", "WebSearch,Bash")
	t.Setenv("LOOM_READ_ONLY", "1")

	got := strings.Join(appendClaudeSafetyArgs(nil), " ")
	want := "--allowedTools Read,Grep --disallowedTools WebSearch,Bash,Write,Edit,NotebookEdit"
	if got != want {
		t.Fatalf("appendClaudeSafetyArgs = %q, want %q (read_only merges its deny-set, Bash deduped)", got, want)
	}
}

func TestAppendClaudeSafetyArgs_NoKnobsNoFlags(t *testing.T) {
	clearSafetyEnv(t)
	if got := appendClaudeSafetyArgs(nil); len(got) != 0 {
		t.Fatalf("no knobs must add no flags, got %q", got)
	}
}

// The prompt is positional and claude's tool flags are variadic: the lists
// must ride as ONE argv element each so they can never swallow the prompt.
func TestClaudeInteractiveArgs_PromptStaysLastUnderKnobs(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv("LOOM_READ_ONLY", "1")
	cmd := buildClaudeInteractiveCmd("/tmp/wd", "the prompt", "a")
	if last := cmd.Args[len(cmd.Args)-1]; last != "the prompt" {
		t.Fatalf("prompt must stay the final argv element, got %q (args=%q)", last, cmd.Args)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--disallowedTools Write,Edit,NotebookEdit,Bash") {
		t.Fatalf("read_only deny-set missing: %q", joined)
	}
}

func TestCodexSandboxArgs_ReadOnlySwapsTheBypass(t *testing.T) {
	clearSafetyEnv(t)
	if got := strings.Join(codexSandboxArgs(), " "); got != "--dangerously-bypass-approvals-and-sandbox" {
		t.Fatalf("default codex posture = %q", got)
	}
	t.Setenv("LOOM_READ_ONLY", "1")
	if got := strings.Join(codexSandboxArgs(), " "); got != "--sandbox read-only" {
		t.Fatalf("read_only codex posture = %q, want the OS-level read-only sandbox", got)
	}
	args := strings.Join(buildCodexNonInteractiveArgs("p"), " ")
	if strings.Contains(args, "dangerously-bypass") || !strings.Contains(args, "--sandbox read-only") {
		t.Fatalf("read_only exec args must drop the bypass: %q", args)
	}
}

func TestGeminiApprovalMode_ReadOnlyIsPlan(t *testing.T) {
	clearSafetyEnv(t)
	if got := geminiApprovalModeArg(); got != "--approval-mode=yolo" {
		t.Fatalf("default gemini mode = %q", got)
	}
	t.Setenv("LOOM_READ_ONLY", "1")
	if got := geminiApprovalModeArg(); got != "--approval-mode=plan" {
		t.Fatalf("read_only gemini mode = %q, want plan (gemini's documented read-only mode)", got)
	}
}

func TestValidateSafetyKnobs_FailClosedMatrix(t *testing.T) {
	cases := []struct {
		backend  string
		tools    bool
		readOnly bool
		ok       bool
	}{
		{"claude", true, true, true},
		{"codex", false, true, true},   // --sandbox read-only
		{"codex", true, false, false},  // no tool vocabulary
		{"gemini", false, true, true},  // --approval-mode plan
		{"gemini", true, false, false}, // upstream deprecated --allowed-tools
		{"opencode", false, true, false},
		{"cursor", true, false, false},
		{"external", false, true, false},
		{"opencode", false, false, true}, // no knobs, nothing to enforce
	}
	for _, c := range cases {
		var allowed []string
		if c.tools {
			allowed = []string{"Read"}
		}
		err := ValidateSafetyKnobs(c.backend, allowed, nil, c.readOnly)
		if (err == nil) != c.ok {
			t.Errorf("ValidateSafetyKnobs(%s, tools=%v, ro=%v) err=%v, want ok=%v",
				c.backend, c.tools, c.readOnly, err, c.ok)
		}
	}
}

func TestSupportsToolControl_MatchesTheValidator(t *testing.T) {
	for _, name := range []string{"claude", "codex", "gemini", "opencode", "cursor"} {
		fromTable := SupportsToolControl(name)
		fromValidator := ValidateSafetyKnobs(name, []string{"Read"}, nil, false) == nil
		if fromTable != fromValidator {
			t.Errorf("SupportsToolControl(%s)=%v but ValidateSafetyKnobs says %v — keep them in lockstep",
				name, fromTable, fromValidator)
		}
	}
}
