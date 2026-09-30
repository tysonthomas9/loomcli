package tsruntime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
)

func TestLeafPatchRequiresTaskAndRepoIdentityBeforeApplying(t *testing.T) {
	for _, tc := range []struct {
		name, patch, task, repo, missing string
	}{
		{"no patch", "", "", "", ""},
		{"missing task", "patch", "", "source-repo", "LOOM_ASSIGNED_TASK_ID"},
		{"missing repo", "patch", "TASK-1", "", "LOOM_WORKTREE_REPO"},
		{"both present", "patch", "TASK-1", "source-repo", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLeafPatchIdentity(tc.patch, tc.task, tc.repo)
			if tc.missing == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.missing != "" && (err == nil || !strings.Contains(err.Error(), tc.missing)) {
				t.Fatalf("error = %v, want %s", err, tc.missing)
			}
		})
	}
}

func TestLeafPatchUsesResolvedWorkspaceRepoNotAgentDirectory(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS")
	t.Setenv("LOOM_WORKTREE_REPO", "source-repo")
	workDir := "/workspace/worktrees/source-repo/agent"
	if got := leafPatchRepoName(workDir); got != "source-repo" {
		t.Fatalf("leaf repo key = %q, want bridge repo_name source-repo", got)
	}
	if err := validateLeafPatchIdentity("patch", "TASK-1", leafPatchRepoName(workDir)); err != nil {
		t.Fatal(err)
	}
}

func TestLeafPatchCommitUsesBridgeChangeIDForBoundAndUnboundAgents(t *testing.T) {
	git := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real scratch Git checkout verifies the committed Change ID.
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=Leaf Test", "GIT_AUTHOR_EMAIL=leaf@test.invalid",
			"GIT_COMMITTER_NAME=Leaf Test", "GIT_COMMITTER_EMAIL=leaf@test.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	oldBundle, oldRunner := resolveTaskRunnerBundle, runTaskRunner
	t.Cleanup(func() { resolveTaskRunnerBundle, runTaskRunner = oldBundle, oldRunner })
	resolveTaskRunnerBundle = func() (string, error) { return "fixture", nil }
	for _, bound := range []bool{true, false} {
		name := "unbound"
		if bound {
			name = "bound"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
			t.Setenv("LOOM_WORKSPACE", "WS")
			t.Setenv("LOOM_ASSIGNED_TASK_ID", "T1")
			t.Setenv("LOOM_WORKTREE_REPO", "source-repo")
			if bound {
				t.Setenv("LOOM_AGENT_REPO", "source-repo")
			} else {
				t.Setenv("LOOM_AGENT_REPO", "")
			}
			workDir := filepath.Join(t.TempDir(), "agent")
			if err := os.Mkdir(workDir, 0o700); err != nil {
				t.Fatal(err)
			}
			git(t, workDir, "init", "-b", "main")
			file := filepath.Join(workDir, "README.md")
			if err := os.WriteFile(file, []byte("base\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			git(t, workDir, "add", "README.md")
			git(t, workDir, "commit", "-m", "base")
			base := git(t, workDir, "rev-parse", "HEAD")
			if err := os.WriteFile(file, []byte("edited\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			patch := git(t, workDir, "diff", "--", "README.md") + "\n"
			git(t, workDir, "checkout", "--", "README.md")
			runTaskRunner = func(context.Context, driver.BundledRunnerOptions) (json.RawMessage, error) {
				return json.Marshal(map[string]string{"status": "completed", "patch": patch, "base_ref": base})
			}
			if err := (agentInvoker{}).InvokeNonInteractive(workDir, "task", "agent", nil, nil); err != nil {
				t.Fatal(err)
			}
			bridgeChangeID, err := driverfreeze.ChangeForTask(context.Background(), "WS", "T1", "source-repo")
			if err != nil {
				t.Fatal(err)
			}
			message := git(t, workDir, "show", "-s", "--format=%B", "HEAD")
			if !strings.Contains(message, "Loom-Change-Id: "+bridgeChangeID) {
				t.Fatalf("leaf commit did not use bridge key %s: %s", bridgeChangeID, message)
			}
		})
	}
}

func TestFailedLeafFreezesReturnedPatchWithoutChangingHost(t *testing.T) {
	for _, tc := range []struct{ name, status, errorClass, outcome string }{
		{"failed", "failed", "local_agent_failed", "failed"},
		{"timeout", "failed", "timeout", "timeout"},
		{"cancelled", "cancelled", "driver_cancelled", "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := func(t *testing.T, dir string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...) //nolint:norawexec // Scratch repository proves failure revisions use the returned patch.
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
					"GIT_AUTHOR_NAME=Leaf Test", "GIT_AUTHOR_EMAIL=leaf@test.invalid",
					"GIT_COMMITTER_NAME=Leaf Test", "GIT_COMMITTER_EMAIL=leaf@test.invalid")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			oldBundle, oldRunner := resolveTaskRunnerBundle, runTaskRunner
			t.Cleanup(func() { resolveTaskRunnerBundle, runTaskRunner = oldBundle, oldRunner })
			resolveTaskRunnerBundle = func() (string, error) { return "fixture", nil }
			t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
			t.Setenv("LOOM_WORKSPACE", "WS")
			t.Setenv("LOOM_ASSIGNED_TASK_ID", "T1")
			t.Setenv("LOOM_WORKTREE_REPO", "source-repo")
			t.Setenv("LOOM_TASK_RUN_ID", "failed-attempt")
			workDir := filepath.Join(t.TempDir(), "agent")
			if err := os.Mkdir(workDir, 0o700); err != nil {
				t.Fatal(err)
			}
			git(t, workDir, "init", "-b", "main")
			file := filepath.Join(workDir, "README.md")
			if err := os.WriteFile(file, []byte("base\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			git(t, workDir, "add", "README.md")
			git(t, workDir, "commit", "-m", "base")
			base := git(t, workDir, "rev-parse", "HEAD")
			if err := os.WriteFile(file, []byte("edited\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			patch := git(t, workDir, "diff", "--", "README.md") + "\n"
			git(t, workDir, "checkout", "--", "README.md")
			runTaskRunner = func(context.Context, driver.BundledRunnerOptions) (json.RawMessage, error) {
				return json.Marshal(map[string]string{"status": tc.status, "errorClass": tc.errorClass, "errorMessage": "agent stopped", "patch": patch, "base_ref": base})
			}
			if err := (agentInvoker{}).InvokeNonInteractive(workDir, "task", "agent", nil, nil); err == nil {
				t.Fatal("failed run returned nil")
			}
			if got := git(t, workDir, "status", "--porcelain"); got != "" {
				t.Fatalf("host worktree changed: %s", got)
			}
			change, err := driverfreeze.ChangeForTask(context.Background(), "WS", "T1", "source-repo")
			if err != nil {
				t.Fatal(err)
			}
			head := git(t, workDir, "rev-parse", "refs/loom/ws/WS/change/"+change+"/1/head")
			if diff := git(t, workDir, "diff", base+".."+head); !strings.Contains(diff, "+edited") {
				t.Fatalf("revision omitted returned patch: %s", diff)
			}
			if _, err := driverfreeze.Freeze(context.Background(), driverfreeze.Request{
				Workspace: "WS", Task: "T1", Repo: "source-repo", Attempt: "failed-attempt",
				Worktree: workDir, Base: base, Patch: []byte(patch), Outcome: tc.outcome,
			}); err != nil {
				t.Fatalf("frozen outcome was not %s: %v", tc.outcome, err)
			}
		})
	}
}
