package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestInitHelperDirectoriesNamesAndPrompts(t *testing.T) {
	oldYes, oldDir, oldNames := initYes, initWorktreesDir, initNames
	t.Cleanup(func() {
		initYes, initWorktreesDir, initNames = oldYes, oldDir, oldNames
	})

	initWorktreesDir = "custom-worktrees"
	if got := getWorktreesDirForInit(); got != "custom-worktrees" {
		t.Fatalf("worktrees dir = %q", got)
	}

	dir := t.TempDir()
	initYes = true
	newDir := filepath.Join(dir, "worktrees")
	if !createWorktreesDir(newDir) {
		t.Fatal("createWorktreesDir returned false for new dir")
	}
	if !createWorktreesDir(newDir) {
		t.Fatal("createWorktreesDir returned false for existing dir")
	}
	filePath := filepath.Join(dir, "not-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if createWorktreesDir(filePath) {
		t.Fatal("createWorktreesDir returned true for existing file")
	}

	for _, name := range []string{"", "-bad", ".", "..", "a..b", "bad/name", "bad name"} {
		if err := validateNewWorktreeName(name); err == nil {
			t.Fatalf("validateNewWorktreeName(%q) returned nil", name)
		}
	}
	if err := validateNewWorktreeName("nova_1"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
	if got := parseNames(" falcon, , nova ,,spark "); !slicesEqual(got, []string{"falcon", "nova", "spark"}) {
		t.Fatalf("parseNames = %#v", got)
	}
	if got := filterExisting([]string{"falcon", "nova", "spark"}, []string{"nova"}); !slicesEqual(got, []string{"falcon", "spark"}) {
		t.Fatalf("filterExisting = %#v", got)
	}
	idx := 0
	if got := nextDefaultName(&idx, map[string]bool{"falcon": true}); got != "nova" {
		t.Fatalf("nextDefaultName = %q", got)
	}
	idx = len(suggestedAgentNames)
	if got := nextDefaultName(&idx, nil); got != "agent11" {
		t.Fatalf("fallback default = %q", got)
	}
	if got := getFirstName(nil); got != "falcon" {
		t.Fatalf("getFirstName(nil) = %q", got)
	}
}

func TestListExistingAndCreateWorktreesBranches(t *testing.T) {
	root := t.TempDir()
	worktreesDir := filepath.Join(root, "worktrees")
	if err := os.MkdirAll(filepath.Join(worktreesDir, "falcon"), 0755); err != nil {
		t.Fatalf("mkdir falcon: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreesDir, "falcon", ".git"), []byte("gitdir: ../.git/worktrees/falcon"), 0600); err != nil {
		t.Fatalf("write git file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktreesDir, "plain-dir"), 0755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}
	if got := listExistingWorktrees(worktreesDir); !slicesEqual(got, []string{"falcon"}) {
		t.Fatalf("listExistingWorktrees = %#v", got)
	}
	if got := listExistingWorktrees(filepath.Join(root, "missing")); got != nil {
		t.Fatalf("missing list = %#v", got)
	}

	oldYes, oldNames := initYes, initNames
	t.Cleanup(func() {
		initYes, initNames = oldYes, oldNames
	})
	initYes, initNames = true, "falcon,nova"
	deps, _, execR, _, _ := NewTestDeps(t)
	execR.RunFunc = func(_ string, name string, args ...string) cli.CommandResult {
		if name != "git" {
			return cli.CommandResult{Err: errors.New("unexpected command")}
		}
		return cli.CommandResult{}
	}
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	got := createWorktrees(deps, worktreesDir)
	if !slicesEqual(got, []string{"falcon", "nova"}) {
		t.Fatalf("createWorktrees = %#v", got)
	}
	if len(execR.Calls) != 1 || !strings.Contains(strings.Join(execR.Calls[0].Args, " "), "worktree add") {
		t.Fatalf("git calls = %#v", execR.Calls)
	}

	execR.Calls = nil
	execR.RunFunc = func(_ string, _ string, args ...string) cli.CommandResult {
		if len(args) > 0 && args[len(args)-1] == "-bad" {
			return cli.CommandResult{Err: errors.New("bad"), Stderr: "fatal"}
		}
		return cli.CommandResult{}
	}
	if createSingleWorktree(deps, worktreesDir, "-bad") {
		t.Fatal("invalid worktree should not be created")
	}
	if createSingleWorktree(deps, worktreesDir, "fatal") && len(execR.Calls) == 0 {
		t.Fatal("expected git call for valid worktree")
	}
}

func TestPromptForWorktreeNamesRetriesInvalidAndExistingNames(t *testing.T) {
	MockStdin(t, "3\n")

	got := promptForWorktreeNames([]string{"falcon"})
	if !slicesEqual(got, []string{"nova", "spark", "ember"}) {
		t.Fatalf("promptForWorktreeNames = %#v", got)
	}

	used := map[string]bool{"nova": true}
	MockStdin(t, "custom\n")
	if got := promptValidWorktreeName(1, "spark", used); got != "custom" {
		t.Fatalf("promptValidWorktreeName = %q, want custom", got)
	}

	MockStdin(t, "\n")
	if got := promptValidWorktreeName(1, "spark", used); got != "spark" {
		t.Fatalf("promptValidWorktreeName default = %q, want spark", got)
	}

	MockStdin(t, "0\n")
	if got := promptForWorktreeNames(nil); got != nil {
		t.Fatalf("zero count prompt returned %#v, want nil", got)
	}
}
