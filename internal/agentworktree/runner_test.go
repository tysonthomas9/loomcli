package agentworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

// formerGit is the helper 1.2a used (old internal/loomgit/loomgit.go:180-189),
// kept here as the reference for the shared runner.
func formerGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:norawexec,gosec // reference copy of the former helper.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// formerRaw is the output localworkspace's runGit returned (raw, on error too).
func formerRaw(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:norawexec,gosec // reference copy of the former helper.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func TestWorkspaceGitRunnerParity(t *testing.T) {
	repo := newRepo(t, filepath.Join(t.TempDir(), "repo"))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := map[string]struct {
		ctx  context.Context
		args []string
	}{
		"cwd":          {context.Background(), []string{"rev-parse", "--show-toplevel"}},
		"no prompt":    {context.Background(), []string{"-c", "alias.p=!echo prompt=$GIT_TERMINAL_PROMPT", "p"}},
		"error output": {context.Background(), []string{"rev-parse", "--verify", "no-such-ref"}},
		"cancelled":    {cancelled, []string{"status"}},
	}
	for name, c := range cases {
		wantOut, wantErr := formerGit(c.ctx, repo, c.args...)
		gotOut, gotErr := gitrunner.Exec{}.Run(c.ctx, repo, c.args...)
		if gotOut != wantOut || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
			t.Errorf("%s: got %q, %v; want %q, %v", name, gotOut, gotErr, wantOut, wantErr)
		}
		rawOut, rawErr := gitrunner.Output(c.ctx, repo, c.args...)
		if fmt.Sprint(rawErr) != fmt.Sprint(wantErr) || rawOut != formerRaw(c.ctx, repo, c.args...) {
			t.Errorf("%s: Output = %q, %v", name, rawOut, rawErr)
		}
		switch name {
		case "cwd":
			if !samePath(gotOut, repo) {
				t.Errorf("cwd: ran in %q, want %q", gotOut, repo)
			}
		case "no prompt":
			if gotOut != "prompt=0" {
				t.Errorf("no prompt: got %q", gotOut)
			}
		case "error output":
			if gotOut != "" || !strings.HasPrefix(rawOut, "fatal: ") {
				t.Errorf("error output: Run out %q, Output out %q; want empty and raw", gotOut, rawOut)
			}
			if gotErr == nil || !strings.Contains(gotErr.Error(), "git rev-parse --verify no-such-ref: ") {
				t.Errorf("error output: got %v", gotErr)
			}
		case "cancelled":
			if !errors.Is(gotErr, context.Canceled) {
				t.Errorf("cancelled: err = %v, want context.Canceled", gotErr)
			}
		}
	}
}

type recordingRunner struct {
	gitrunner.Exec
	calls []string
}

func (r *recordingRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	return r.Exec.Run(ctx, dir, args...)
}

func (r *recordingRunner) ran(prefix string) bool {
	for _, c := range r.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestEnsureThroughWorkspacePortUsesInjectedRunner(t *testing.T) {
	tmp := t.TempDir()
	repo := newRepo(t, filepath.Join(tmp, "repo"))
	r := &recordingRunner{}
	w, err := New(filepath.Join(tmp, "worktrees"), TargetLocal, r)
	if err != nil {
		t.Fatal(err)
	}
	var ws loomagent.Workspace = Port{W: w}
	got, err := ws.Ensure(context.Background(), loomagent.WorkspaceSpec{Key: "agt_p", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_p"})
	if err != nil || got.Branch != "loom/agent/agt_p" || got.HEAD != run(t, repo, "rev-parse", "main") {
		t.Fatalf("Ensure = %+v, %v", got, err)
	}
	if !r.ran("worktree add ") || !r.ran("rev-parse --git-dir") {
		t.Fatalf("worktree add or branch inspect did not use the injected runner: %q", r.calls)
	}
	r.calls = nil
	if _, err := ws.Ensure(context.Background(), loomagent.WorkspaceSpec{Key: "rev_p", Repo: repo, BaseRef: "main", Detached: true}); err != nil {
		t.Fatal(err)
	}
	if !r.ran("worktree add --detach ") {
		t.Fatalf("detached worktree add did not use the injected runner: %q", r.calls)
	}
	if _, err := New(tmp, TargetLocal, nil); err == nil {
		t.Fatal("want error for nil runner")
	}
	if _, err := ws.Publish(context.Background(), loomagent.PublishRequest{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Publish err = %v, want ErrNotImplemented", err)
	}
}
