package git

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runGitSettingsAs runs `loom git-settings args` outside any workspace against a
// real local store, as env.
func runGitSettingsAs(t *testing.T, env map[string]string, args ...string) (string, error) {
	t.Helper()
	stubVerdictStore(t, env)
	var output bytes.Buffer
	gitSettingsCmd.SetArgs(append([]string{"-W", "selected"}, args...))
	gitSettingsCmd.SetOut(&output)
	gitSettingsCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() {
		gitSettingsCmd.SetArgs(nil)
		gitSettingsCmd.SetOut(nil)
		gitSettingsCmd.SetErr(nil)
	})
	err := gitSettingsCmd.Execute()
	for _, name := range []string{"workspace", "delivery", "auto-merge", "lead-may-approve"} {
		flag := gitSettingsCmd.Flags().Lookup(name)
		_ = flag.Value.Set("")
		flag.Changed = false
	}
	return output.String(), err
}

func setupGitSettingsStore(t *testing.T) {
	t.Helper()
	setupOutsideWorkspaceForPR(t)
	journalDir := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit")
	if err := os.MkdirAll(journalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, "store.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGitSettingsChangesTheThreeUISettings(t *testing.T) {
	setupGitSettingsStore(t)
	if out, err := runGitSettingsAs(t, humanEnv()); err != nil || out != "delivery: stack\nauto-merge: off\nlead-may-approve: on\n" {
		t.Fatalf("defaults %q err=%v", out, err)
	}
	out, err := runGitSettingsAs(t, humanEnv(), "--delivery", "pr-per-task", "--auto-merge", "on", "--lead-may-approve", "off")
	if err != nil || out != "delivery: pr-per-task\nauto-merge: on\nlead-may-approve: off\n" {
		t.Fatalf("set %q err=%v", out, err)
	}
	if out, err := runGitSettingsAs(t, humanEnv(), "--auto-merge", "off"); err != nil || out != "delivery: pr-per-task\nauto-merge: off\nlead-may-approve: off\n" {
		t.Fatalf("one flag changes one setting: %q err=%v", out, err)
	}
}

func TestGitSettingsLeadMayChangeDeliveryButNotPermissions(t *testing.T) {
	setupGitSettingsStore(t)
	if out, err := runGitSettingsAs(t, leadEnv(), "--delivery", "pr-per-task"); err != nil || !strings.HasPrefix(out, "delivery: pr-per-task\n") {
		t.Fatalf("lead delivery %q err=%v", out, err)
	}
	for _, flags := range [][]string{{"--auto-merge", "on"}, {"--lead-may-approve", "off"}} {
		for _, env := range []map[string]string{leadEnv(), taskAgentEnv()} {
			if _, err := runGitSettingsAs(t, env, flags...); err == nil || !strings.Contains(err.Error(), "only a human") {
				t.Fatalf("%v as %v: err=%v", flags, env, err)
			}
		}
	}
	if out, _ := runGitSettingsAs(t, humanEnv()); out != "delivery: pr-per-task\nauto-merge: off\nlead-may-approve: on\n" {
		t.Fatalf("refused change was written: %q", out)
	}
}

func TestGitSettingsRejectsUnknownValues(t *testing.T) {
	setupGitSettingsStore(t)
	for _, flags := range [][]string{{"--delivery", "trunk"}, {"--auto-merge", "when_green"}, {"--lead-may-approve", "yes"}} {
		if _, err := runGitSettingsAs(t, humanEnv(), flags...); err == nil || !strings.Contains(err.Error(), "must be") {
			t.Fatalf("%v: err=%v", flags, err)
		}
	}
}
