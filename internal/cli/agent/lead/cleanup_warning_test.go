package lead

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeConfigDirWith builds a throwaway CLAUDE_CONFIG_DIR holding the given
// settings.json body. t.TempDir keeps the real agent-profiles tree untouched.
func claudeConfigDirWith(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, claudeSettingsFile), []byte(body), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return dir
}

func TestClaudeCleanupWarningWhenKeyAbsent(t *testing.T) {
	dir := claudeConfigDirWith(t, `{"model":"opus","permissions":{"allow":[]}}`)

	warning := claudeCleanupWarning(dir)
	if warning == "" {
		t.Fatal("a settings.json without cleanupPeriodDays should warn")
	}
	if !strings.Contains(warning, filepath.Join(dir, claudeSettingsFile)) {
		t.Errorf("warning should name the file to edit, got %q", warning)
	}
	if !strings.Contains(warning, claudeCleanupPeriodKey) {
		t.Errorf("warning should name the setting, got %q", warning)
	}
	if strings.Contains(strings.TrimSuffix(warning, "\n"), "\n") {
		t.Errorf("warning must be a single line, got %q", warning)
	}
}

func TestClaudeCleanupWarningSilentWhenKeyPresent(t *testing.T) {
	for name, body := range map[string]string{
		"number": `{"cleanupPeriodDays":3650}`,
		"zero":   `{"cleanupPeriodDays":0}`,
		"null":   `{"cleanupPeriodDays":null}`,
	} {
		if got := claudeCleanupWarning(claudeConfigDirWith(t, body)); got != "" {
			t.Errorf("%s: want silence, got %q", name, got)
		}
	}
}

func TestClaudeCleanupWarningSilentWhenUnreadable(t *testing.T) {
	missingDir := t.TempDir()
	cases := map[string]string{
		"no config root at all":   "",
		"config root is absent":   filepath.Join(missingDir, "absent"),
		"no settings.json":        missingDir,
		"settings.json is junk":   claudeConfigDirWith(t, "{not json"),
		"settings.json is a list": claudeConfigDirWith(t, `["cleanupPeriodDays"]`),
		"settings.json is null":   claudeConfigDirWith(t, `null`),
	}
	for name, dir := range cases {
		if got := claudeCleanupWarning(dir); got != "" {
			t.Errorf("%s: want silence, got %q", name, got)
		}
	}
}

func TestClaudeCleanupWarningSilentWhenSettingsIsADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, claudeSettingsFile), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := claudeCleanupWarning(dir); got != "" {
		t.Errorf("want silence, got %q", got)
	}
}

func TestWarnClaudeTranscriptCleanupWritesOneLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDirWith(t, `{"model":"opus"}`))
	var out bytes.Buffer
	warnClaudeTranscriptCleanup(&out)
	if lines := strings.Count(out.String(), "\n"); lines != 1 {
		t.Fatalf("want exactly one line, got %d: %q", lines, out.String())
	}

	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDirWith(t, `{"cleanupPeriodDays":3650}`))
	out.Reset()
	warnClaudeTranscriptCleanup(&out)
	if out.Len() != 0 {
		t.Fatalf("want silence, got %q", out.String())
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	out.Reset()
	warnClaudeTranscriptCleanup(&out)
	if out.Len() != 0 {
		t.Fatalf("unprofiled lead should stay silent, got %q", out.String())
	}
}
