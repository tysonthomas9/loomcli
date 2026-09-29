package git

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func resetGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // A real scratch Git repository verifies destructive reset behavior.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Reset Test", "GIT_AUTHOR_EMAIL=reset@test.invalid", "GIT_COMMITTER_NAME=Reset Test", "GIT_COMMITTER_EMAIL=reset@test.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func resetFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	if err := os.Mkdir(origin, 0700); err != nil {
		t.Fatal(err)
	}
	resetGit(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, ".gitignore"), []byte(".env\nbuild/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resetGit(t, origin, "add", ".")
	resetGit(t, origin, "commit", "-m", "base")
	repo := filepath.Join(root, "work")
	resetGit(t, root, "clone", "--local", origin, repo)
	resetGit(t, repo, "checkout", "-b", "loom/ws/W/interactive/L")
	return repo
}

func requireResetCode(t *testing.T, err error, code loomgit.Code) {
	t.Helper()
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestResetProtectedBranchNeverChangesFiles(t *testing.T) {
	repo := resetFixture(t)
	resetGit(t, repo, "checkout", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("valuable\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		_, err := ResetWorktreeResult(repo, "L", "main", force, false)
		requireResetCode(t, err, loomgit.Protected)
		data, err := os.ReadFile(filepath.Join(repo, "README.md"))
		if err != nil || string(data) != "valuable\n" {
			t.Fatalf("protected reset changed file: %q, %v", data, err)
		}
	}
}

func TestResetRejectsUnownedCheckout(t *testing.T) {
	repo := resetFixture(t)
	resetGit(t, repo, "checkout", "-b", "plain-feature")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := ResetWorktreeResult(repo, "L", "main", true, false)
	requireResetCode(t, err, loomgit.WorkspaceUnsupported)
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("unowned checkout changed: %q, %v", data, err)
	}
}

func TestResetCapturesBeforeDiscardAndNeverPushes(t *testing.T) {
	repo := resetFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("edited\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ResetWorktreeResult(repo, "L", "main", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.CaptureRef, "refs/loom/ws/W/wip/L/") {
		t.Fatalf("capture ref = %q", result.CaptureRef)
	}
	if result.Pushed || len(result.Ignored) != 1 || result.Ignored[0].Path != ".env" || result.Ignored[0].Size != 8 {
		t.Fatalf("result = %+v", result)
	}
	if got := resetGit(t, repo, "show", result.CaptureRef+":README.md"); got != "edited" {
		t.Fatalf("captured README = %q", got)
	}
	if got := resetGit(t, repo, "show", "HEAD:README.md"); got != "base" {
		t.Fatalf("reset README = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".env")); !os.IsNotExist(err) {
		t.Fatalf("ignored file remained: %v", err)
	}
}

func TestResetRealRepoDefaults(t *testing.T) {
	source := resetGit(t, ".", "rev-parse", "--show-toplevel")
	root, err := os.MkdirTemp("/tmp", "p15-real-repo-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	shallow := filepath.Join(root, "shallow")
	resetGit(t, root, "clone", "--depth", "1", "file://"+source, shallow)
	if got := resetGit(t, shallow, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("fixture is not shallow: %s", got)
	}
	repo := filepath.Join(root, "repo")
	resetGit(t, root, "clone", "--local", shallow, repo)
	origin := filepath.Join(root, "origin.git")
	resetGit(t, root, "init", "--bare", origin)
	resetGit(t, origin, "config", "receive.shallowUpdate", "true")
	resetGit(t, repo, "remote", "set-url", "origin", origin)
	resetGit(t, repo, "push", "origin", "HEAD:refs/heads/main")
	resetGit(t, repo, "checkout", "-b", "loom/ws/W/interactive/L")
	readme := filepath.Join(repo, "README.md")
	f, err := os.OpenFile(readme, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nP1.5 reset capture proof\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := ResetWorktreeResult(repo, "L", "main", false, false)
	if err != nil {
		t.Fatal(err)
	}
	stat := resetGit(t, repo, "diff", "--stat", "HEAD", result.CaptureRef)
	if !strings.Contains(stat, "README.md") {
		t.Fatalf("capture diff stat = %q", stat)
	}
	t.Logf("capture diff stat:\n%s", stat)
}

func TestResetPreservesCleanLocalCommit(t *testing.T) {
	repo := resetFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("committed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resetGit(t, repo, "commit", "-am", "local")
	before := resetGit(t, repo, "rev-parse", "HEAD")
	result, err := ResetWorktreeResult(repo, "L", "main", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := resetGit(t, repo, "rev-parse", result.CaptureRef); got != before {
		t.Fatalf("WIP = %s, want %s", got, before)
	}
	if got := resetGit(t, repo, "show", "HEAD:README.md"); got != "base" {
		t.Fatalf("reset README = %q", got)
	}
}

func TestResetRefusesIncompleteCapture(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		content    []byte
		sparse     bool
	}{
		{name: "secret", path: ".env.local", content: []byte("private\n")},
		{name: "over cap", path: "large.bin", sparse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := resetFixture(t)
			path := filepath.Join(repo, tc.path)
			if tc.sparse {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Truncate(101 << 20); err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
			} else if err := os.WriteFile(path, tc.content, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := ResetWorktreeResult(repo, "L", "main", false, false)
			requireResetCode(t, err, loomgit.CaptureIncomplete)
			if !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("missing filename: %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("reset removed %s: %v", tc.path, err)
			}
		})
	}
}

func TestResetCaptureFailureDoesNotDiscard(t *testing.T) {
	repo := resetFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	_, err := ResetWorktreeResult(repo, "L", "main", false, false)
	requireResetCode(t, err, loomgit.CaptureIncomplete)
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("file changed: %q %v", data, err)
	}
}

func TestResetRejectsPushRequest(t *testing.T) {
	repo := resetFixture(t)
	_, err := ResetWorktreeResult(repo, "L", "main", true, true)
	if err == nil || !strings.Contains(err.Error(), "cannot push") {
		t.Fatalf("error = %v", err)
	}
}

func TestResetCLIHasNoPushFlag(t *testing.T) {
	if flag := resetCmd.Flags().Lookup("push"); flag != nil {
		t.Fatalf("reset still exposes --push")
	}
}

func TestResetCLIArguments(t *testing.T) {
	for _, tc := range []struct {
		name      string
		all       bool
		args      []string
		wantError bool
	}{
		{"missing worktree", false, nil, true},
		{"all with too many args", true, []string{"main", "extra"}, true},
		{"all with target", true, []string{"main"}, false},
		{"all default target", true, nil, false},
		{"one worktree", false, []string{"falcon"}, false},
		{"worktree and target", false, []string{"falcon", "main"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetAll = tc.all
			defer func() { resetAll = false }()
			err := resetCmd.Args(&cobra.Command{}, tc.args)
			if (err != nil) != tc.wantError {
				t.Fatalf("Args(%v) = %v", tc.args, err)
			}
		})
	}
}

func TestResetConfirmationListsIgnoredPathAndSize(t *testing.T) {
	repo := resetFixture(t)
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	if err := printResetIgnored(repo); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); !strings.Contains(got, ".env (8 bytes)") {
		t.Fatalf("confirmation list = %q", got)
	}
}

func TestResetStopsRunningAgentBeforeCapture(t *testing.T) {
	repo := resetFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestResetAgentChild$") //nolint:norawexec // Isolated child owns the real lock that Reset stops.
	cmd.Env = append(os.Environ(), "RESET_AGENT_CHILD="+repo)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill(); <-done }()
	ready := bufio.NewScanner(stdout)
	if !ready.Scan() || ready.Text() != "READY" {
		t.Fatalf("child failed to acquire lock: %q", ready.Text())
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("agent edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = ResetWorktreeResult(repo, "L", "main", false, false)
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("without force: %v", err)
	}
	result, err := ResetWorktreeResult(repo, "L", "main", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := resetGit(t, repo, "show", result.CaptureRef+":README.md"); got != "agent edit" {
		t.Fatalf("capture after stop = %q", got)
	}
}

func TestResetAgentChild(t *testing.T) {
	repo := os.Getenv("RESET_AGENT_CHILD")
	if repo == "" {
		return
	}
	if err := cli.AcquireLock(repo, "reset test", "L"); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	time.Sleep(30 * time.Second)
}
