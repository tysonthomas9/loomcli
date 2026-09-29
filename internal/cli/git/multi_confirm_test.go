package git

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func confirmationForTest(input string, interactive bool) *confirmationSession {
	return &confirmationSession{reader: bufio.NewReader(strings.NewReader(input)), interactive: interactive}
}

func captureConfirmationOutput(t *testing.T, run func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	runErr := run()
	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

func TestConfirmationsConsumeOneAnswerEach(t *testing.T) {
	s := confirmationForTest("yes\nno\nyes\n", true)
	for i, want := range []bool{true, false, true} {
		if got := s.confirm("Continue?"); got != want {
			t.Fatalf("answer %d = %v, want %v", i+1, got, want)
		}
	}
}

func TestMultiCommandsRefuseNonInteractiveAndListTargets(t *testing.T) {
	for _, command := range []string{"loom reset --all", "loom sync", "loom pr --all"} {
		s := confirmationForTest("", false)
		err := s.requireInteractive(command, []string{"first", "second"})
		if err == nil || !strings.Contains(err.Error(), "first, second") || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("%s refusal = %v", command, err)
		}
	}
}

func TestResetAllConfirmsEachWorktreeAndCapturesApprovedOnes(t *testing.T) {
	root := t.TempDir()
	worktrees := make([]cli.WorktreeInfo, 0, 3)
	config := &LoomConfig{DefaultWorkspace: "W", Workspaces: map[string]WorkspaceConfig{"W": {Path: root}}}
	for _, name := range []string{"first", "second", "third"} {
		repo := resetFixture(t)
		if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(name+" edit\n"), 0600); err != nil {
			t.Fatal(err)
		}
		worktrees = append(worktrees, cli.WorktreeInfo{Name: name, Path: repo, Branch: "loom/ws/W/interactive/L"})
		ws := config.Workspaces["W"]
		ws.Repos = append(ws.Repos, RepoConfig{Name: name, Path: repo, DefaultBranch: "main"})
		config.Workspaces["W"] = ws
	}
	setupWorkspaceConfig(t, config)
	old := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = old })
	if err := resetAllWorktreesWithConfirmation("main", true, confirmationForTest("", false)); err == nil || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "third") {
		t.Fatalf("non-interactive reset refusal = %v", err)
	}
	targets, _ := buildResetTargets(worktrees, "main", true)
	output, err := captureConfirmationOutput(t, func() error {
		failed, skipped, err := executeResetAll(targets, confirmationForTest("yes\nno\nyes\n", true))
		if err != nil {
			return err
		}
		if len(failed) != 0 || len(skipped) != 1 || skipped[0] != "second" {
			t.Fatalf("failed=%v skipped=%v", failed, skipped)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output, "after capture? (y/N)") != 3 || strings.Count(output, "M README.md") != 3 {
		t.Fatalf("confirmation output missing prompts or unsaved work: %s", output)
	}
	for i, wt := range worktrees {
		want := "base"
		if i == 1 {
			want = "second edit"
		}
		if got := resetGit(t, wt.Path, "show", "HEAD:README.md"); got != want && i != 1 {
			t.Fatalf("%s HEAD = %q, want %q", wt.Name, got, want)
		}
		if i == 1 {
			data, readErr := os.ReadFile(filepath.Join(wt.Path, "README.md"))
			if readErr != nil || string(data) != "second edit\n" {
				t.Fatalf("skipped workspace changed: %q, %v", data, readErr)
			}
		} else if refs := resetGit(t, wt.Path, "for-each-ref", "--format=%(refname)", "refs/loom/ws/W/wip/L"); refs == "" {
			t.Fatalf("%s has no capture ref", wt.Name)
		}
	}
}

func TestSyncAndPRAskPerWorkspaceAndSkipDeclinedWork(t *testing.T) {
	root := t.TempDir()
	config := &LoomConfig{DefaultWorkspace: "first", Workspaces: make(map[string]WorkspaceConfig)}
	for _, name := range []string{"first", "second"} {
		repo := resetFixture(t)
		if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(name+" edit\n"), 0600); err != nil {
			t.Fatal(err)
		}
		config.Workspaces[name] = WorkspaceConfig{
			Path:  root,
			Repos: []RepoConfig{{Name: name, Path: repo, DefaultBranch: "main"}},
		}
	}
	setupWorkspaceConfig(t, config)
	old := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = old })
	for _, tc := range []struct {
		name, prompt string
		run          func(*confirmationSession) error
	}{
		{"sync", "Sync workspace", func(s *confirmationSession) error {
			return runWorkspaceSyncWithConfirmation(cli.GetDeps(nil), false, false, "", s)
		}},
		{"pr", "Create PRs for workspace", func(s *confirmationSession) error {
			return prAllWorkspacesWithConfirmation(cli.GetDeps(nil), "", s)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var approved []string
			if tc.name == "sync" {
				old := runConfirmedSync
				runConfirmedSync = func(_ *cli.Deps, resolver *cli.Resolver, _, _ bool) error {
					approved = append(approved, resolver.WorkspaceName())
					return nil
				}
				t.Cleanup(func() { runConfirmedSync = old })
			} else {
				oldCheck, oldRun := checkConfirmedGh, runConfirmedPR
				checkConfirmedGh = func(*cli.Deps) error { return nil }
				runConfirmedPR = func(_ *cli.Deps, worktrees []cli.WorktreeInfo, _, _ string) {
					approved = append(approved, worktrees[0].Workspace)
				}
				t.Cleanup(func() { checkConfirmedGh, runConfirmedPR = oldCheck, oldRun })
			}
			if err := tc.run(confirmationForTest("", false)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "first") || !strings.Contains(strings.ToLower(err.Error()), "second") {
				t.Fatalf("non-interactive refusal = %v", err)
			}
			output, err := captureConfirmationOutput(t, func() error {
				return tc.run(confirmationForTest("no\nno\n", true))
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(output, tc.prompt) != 2 || strings.Count(output, "M README.md") != 2 || !strings.Contains(output, "Skipped workspace FIRST.") || !strings.Contains(output, "Skipped workspace SECOND.") {
				t.Fatalf("per-workspace prompts missing: %s", output)
			}
			output, err = captureConfirmationOutput(t, func() error {
				return tc.run(confirmationForTest("yes\nno\n", true))
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(approved) != 1 || !strings.EqualFold(approved[0], "first") || !strings.Contains(output, "Skipped workspace SECOND.") {
				t.Fatalf("mixed answers ran %v: %s", approved, output)
			}
		})
	}
}
