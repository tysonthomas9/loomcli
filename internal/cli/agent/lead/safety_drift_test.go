package lead

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
)

// writeAmbientFile writes body into dir/name and returns the full path.
func writeAmbientFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// personaFileWithBlock is a realistic ambient file: a persona, then the safety
// block, then more prose. The check is a substring compare precisely so the
// block does not have to sit at the end.
func personaFileWithBlock() string {
	return "# Lead\n\nYou manage the backlog.\n\n" + agent.LeadSafetyPrompt() + "\n\nEnd of file.\n"
}

// TestCheckAmbientSafetyBlockClaudeCurrent is acceptance criterion 1: a
// profile CLAUDE.md carrying the current block passes.
func TestCheckAmbientSafetyBlockClaudeCurrent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envClaudeConfigDir, dir)
	writeAmbientFile(t, dir, "CLAUDE.md", personaFileWithBlock())

	if err := CheckAmbientSafetyBlock(backendnames.Claude, t.TempDir(), true, SuppressedByBuiltinNone); err != nil {
		t.Fatalf("a CLAUDE.md carrying the current safety block was refused: %v", err)
	}
}

// TestCheckAmbientSafetyBlockClaudeCRLF is the second half of criterion 1: an
// operator-reflowed file (CRLF line endings, trailing spaces) is NOT drift.
func TestCheckAmbientSafetyBlockClaudeCRLF(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envClaudeConfigDir, dir)
	crlf := strings.ReplaceAll(personaFileWithBlock(), "\n", "  \r\n")
	writeAmbientFile(t, dir, "CLAUDE.md", crlf)

	if err := CheckAmbientSafetyBlock(backendnames.Claude, t.TempDir(), true, SuppressedByBuiltinNone); err != nil {
		t.Fatalf("a CRLF copy of a current CLAUDE.md was refused: %v", err)
	}
}

// TestCheckAmbientSafetyBlockClaudeStale is criterion 1's refusal: the file is
// there, the block is not, and the message names BOTH repair commands.
func TestCheckAmbientSafetyBlockClaudeStale(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envClaudeConfigDir, dir)
	path := writeAmbientFile(t, dir, "CLAUDE.md", "# Lead\n\nAn old persona with no safety block at all.\n")

	err := CheckAmbientSafetyBlock(backendnames.Claude, t.TempDir(), true, SuppressedByProfileSource)
	if err == nil {
		t.Fatal("a CLAUDE.md without the safety block was accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		string(SuppressedByProfileSource),
		path,
		"does not contain the current multi-agent safety block",
		"loom lead --print-prompt",
		repairProvisionProfile,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// TestCheckAmbientSafetyBlockClaudeAbsent is criterion 1's third case.
func TestCheckAmbientSafetyBlockClaudeAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envClaudeConfigDir, dir)

	err := CheckAmbientSafetyBlock(backendnames.Claude, t.TempDir(), true, SuppressedByBuiltinNone)
	if err == nil {
		t.Fatal("a missing CLAUDE.md was accepted")
	}
	if !strings.Contains(err.Error(), "loom lead --print-prompt") {
		t.Fatalf("refusal carries no repair recipe:\n%s", err)
	}
}

// TestCheckAmbientSafetyBlockClaudeNoConfigDir is acceptance criterion 2. On
// this base nothing inside `loom lead` exports CLAUDE_CONFIG_DIR, so this is
// the path an UNPROFILED lead takes, and a bare file-not-found would send the
// operator looking for the wrong thing.
func TestCheckAmbientSafetyBlockClaudeNoConfigDir(t *testing.T) {
	t.Setenv(envClaudeConfigDir, "")

	err := CheckAmbientSafetyBlock(backendnames.Claude, t.TempDir(), true, SuppressedByBuiltinNone)
	if err == nil {
		t.Fatal("suppression with no CLAUDE_CONFIG_DIR was accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"CLAUDE_CONFIG_DIR is not set",
		"suppression requires a profiled lead",
		repairProvisionProfile,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal does not mention %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "no such file or directory") {
		t.Fatalf("refusal degraded into a bare file-not-found:\n%s", msg)
	}
}

// TestCheckAmbientSafetyBlockCodex is acceptance criterion 3: AGENTS.md in the
// dedicated lead workdir, present-with-block / present-stale / absent.
func TestCheckAmbientSafetyBlockCodex(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		dir := t.TempDir()
		writeAmbientFile(t, dir, leadAgentsFileName, personaFileWithBlock())
		if err := CheckAmbientSafetyBlock(backendnames.Codex, dir, true, SuppressedByBuiltinNone); err != nil {
			t.Fatalf("an AGENTS.md carrying the current safety block was refused: %v", err)
		}
	})

	t.Run("stale", func(t *testing.T) {
		dir := t.TempDir()
		path := writeAmbientFile(t, dir, leadAgentsFileName, "# Lead\n\nNo guardrails here.\n")
		err := CheckAmbientSafetyBlock(backendnames.Codex, dir, true, SuppressedByBuiltinNone)
		if err == nil {
			t.Fatal("a stale AGENTS.md was accepted")
		}
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("refusal does not name the file it checked:\n%s", err)
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		if err := CheckAmbientSafetyBlock(backendnames.Codex, dir, true, SuppressedByBuiltinNone); err == nil {
			t.Fatal("a missing AGENTS.md was accepted")
		}
	})
}

// TestCheckAmbientSafetyBlockCodexNotDedicated is acceptance criterion 4: in
// the os.Getwd fallback the AGENTS.md sitting there is somebody else's, so the
// refusal has to say that rather than report a missing file.
func TestCheckAmbientSafetyBlockCodexNotDedicated(t *testing.T) {
	dir := t.TempDir()
	writeAmbientFile(t, dir, leadAgentsFileName, personaFileWithBlock())

	err := CheckAmbientSafetyBlock(backendnames.Codex, dir, false, SuppressedByBuiltinNone)
	if err == nil {
		t.Fatal("suppression in a non-dedicated workdir was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not a dedicated lead workdir") {
		t.Fatalf("refusal does not say the workdir is not dedicated:\n%s", msg)
	}
	if !strings.Contains(msg, "LOOM_LEAD_WORKDIR") {
		t.Fatalf("refusal does not name the escape hatch:\n%s", msg)
	}
}

// TestLeadRunPersonaSuppressionArgv pins the runtime switch: --prompt
// builtin:none suppresses, anything else does not (outside a workspace the
// role probe answers false, which is what the empty cases assert).
func TestLeadRunPersonaSuppressionArgv(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := leadPromptFile
	t.Cleanup(func() { leadPromptFile = old })

	leadPromptFile = builtinNonePromptFlag
	reason, suppressed := leadRunPersonaSuppression(t.Context())
	if !suppressed || reason != SuppressedByBuiltinNone {
		t.Fatalf("--prompt %s did not suppress: reason=%q suppressed=%v", builtinNonePromptFlag, reason, suppressed)
	}

	leadPromptFile = "builtin:lead-profile"
	if _, suppressed := leadRunPersonaSuppression(t.Context()); suppressed {
		t.Fatal("--prompt builtin:lead-profile must NOT suppress: its persona is on argv as a pointer prompt")
	}
}

// TestNormalizeAmbientTextKeepsInteriorContent proves the normalisation is not
// so aggressive that genuinely stale text starts matching.
func TestNormalizeAmbientTextKeepsInteriorContent(t *testing.T) {
	if normalizeAmbientText("a  b\r\n") != "a  b" {
		t.Fatalf("interior whitespace or content was altered: %q", normalizeAmbientText("a  b\r\n"))
	}
	if strings.Contains(normalizeAmbientText("one\ntwo\n"), "one two") {
		t.Fatal("line breaks were collapsed; a reflow would falsely match")
	}
}

// TestRunLeadPrintPromptNeverReachesDriftCheck is acceptance criterion 6.
// The environment below is one the drift check REFUSES (persona suppressed,
// no CLAUDE_CONFIG_DIR, no dedicated workdir); if --print-prompt reached the
// check, os.Exit(1) would take this test binary down with it.
func TestRunLeadPrintPromptNeverReachesDriftCheck(t *testing.T) {
	t.Setenv(envClaudeConfigDir, "")
	t.Setenv("LOOM_LEAD_WORKDIR", "")

	output, mock := capturePrintPromptRunWithPromptFile(t, "", builtinNonePromptFlag, nil)
	if len(output) != 0 {
		t.Fatalf("--print-prompt under suppression printed %d bytes: %q", len(output), output)
	}
	if len(mock.interactiveCalls) != 0 {
		t.Fatalf("--print-prompt started a session: %d invocations", len(mock.interactiveCalls))
	}
}
