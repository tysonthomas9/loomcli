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
	t.Setenv(envRoleInputPolicy, "")
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

func TestValidateSafetyKnobs_EnforcementMatrix(t *testing.T) {
	cases := []struct {
		backend  string
		tools    bool
		readOnly bool
		ok       bool
		soft     bool // runs, but read_only is prompt-only
	}{
		{backend: "claude", tools: true, readOnly: true, ok: true},
		{backend: "codex", readOnly: true, ok: true},                // --sandbox read-only
		{backend: "codex", tools: true, ok: false},                  // no tool vocabulary
		{backend: "gemini", readOnly: true, ok: true},               // --approval-mode plan
		{backend: "gemini", tools: true, ok: false},                 // upstream deprecated --allowed-tools
		{backend: "opencode", readOnly: true, ok: true, soft: true}, // degrades to the preamble
		{backend: "localdogfood", readOnly: true, ok: true, soft: true},
		{backend: "cursor", tools: true, ok: false},
		{backend: "external", readOnly: true, ok: true, soft: true},
		{backend: "opencode", ok: true}, // no knobs, nothing to enforce
	}
	for _, c := range cases {
		var allowed []string
		if c.tools {
			allowed = []string{"Read"}
		}
		warning, err := ValidateSafetyKnobs(c.backend, allowed, nil, c.readOnly)
		if (err == nil) != c.ok {
			t.Errorf("ValidateSafetyKnobs(%s, tools=%v, ro=%v) err=%v, want ok=%v",
				c.backend, c.tools, c.readOnly, err, c.ok)
		}
		if (warning != "") != c.soft {
			t.Errorf("ValidateSafetyKnobs(%s, tools=%v, ro=%v) warning=%q, want soft=%v",
				c.backend, c.tools, c.readOnly, warning, c.soft)
		}
	}
}

// The seeded built-in `plan` role carries read_only on every workspace, so a
// hard refusal here refuses every planner on every backend without a sandbox —
// including the deterministic test backend. It must run, and it must say that
// the restriction is prompt-deep.
func TestValidateSafetyKnobs_SeededPlannerRunsOnASoftBackend(t *testing.T) {
	warning, err := ValidateSafetyKnobs("localdogfood", nil, nil, true)
	if err != nil {
		t.Fatalf("read_only must degrade, not refuse: %v", err)
	}
	if !strings.Contains(warning, "prompt") || !strings.Contains(warning, "localdogfood") {
		t.Fatalf("warning must name the backend and say the enforcement is prompt-only; got %q", warning)
	}
}

// Tool lists have no soft equivalent, so they keep failing closed even on the
// backends where read_only now degrades.
func TestValidateSafetyKnobs_ToolListsStayFailClosed(t *testing.T) {
	for _, backend := range []string{"opencode", "cursor", "external", "localdogfood", "codex", "gemini"} {
		warning, err := ValidateSafetyKnobs(backend, []string{"Read"}, nil, false)
		if err == nil {
			t.Errorf("allowed_tools on %q must refuse the run", backend)
		}
		if warning != "" {
			t.Errorf("a refusal must not also warn (%q): %q", backend, warning)
		}
		if _, err := ValidateSafetyKnobs(backend, nil, []string{"Bash"}, false); err == nil {
			t.Errorf("denied_tools on %q must refuse the run", backend)
		}
	}
}

func TestSupportsToolControl_MatchesTheValidator(t *testing.T) {
	for _, name := range []string{"claude", "codex", "gemini", "opencode", "cursor"} {
		fromTable := SupportsToolControl(name)
		_, err := ValidateSafetyKnobs(name, []string{"Read"}, nil, false)
		if fromValidator := err == nil; fromTable != fromValidator {
			t.Errorf("SupportsToolControl(%s)=%v but ValidateSafetyKnobs says %v — keep them in lockstep",
				name, fromTable, fromValidator)
		}
	}
}

func TestSupportsHardReadOnly_MatchesTheValidator(t *testing.T) {
	for _, name := range []string{"claude", "codex", "gemini", "opencode", "cursor", "localdogfood"} {
		fromTable := SupportsHardReadOnly(name)
		warning, err := ValidateSafetyKnobs(name, nil, nil, true)
		if err != nil {
			t.Fatalf("read_only must never refuse now: %s -> %v", name, err)
		}
		if fromValidator := warning == ""; fromTable != fromValidator {
			t.Errorf("SupportsHardReadOnly(%s)=%v but the validator %s — keep them in lockstep",
				name, fromTable, map[bool]string{true: "warns", false: "does not warn"}[warning != ""])
		}
	}
}
