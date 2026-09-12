package localworkspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newCredentialRepo initializes a repo at root/name with the given origin URL.
func newCredentialRepo(t *testing.T, name, originURL string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	git(t, "", "init", repo)
	git(t, repo, "remote", "add", "origin", originURL)
	return repo
}

func credentialHelpers(t *testing.T, repo string) []string {
	t.Helper()
	out, err := gitMaybe(repo, "config", "--local", "--get-all", "credential.helper")
	if err != nil {
		return nil
	}
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "\n")
}

func TestEnsureCredentialHelperInstallsHelperForHTTPSRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := newCredentialRepo(t, "repo", "https://github.com/example/repo")

	if err := EnsureCredentialHelper(context.Background(), repo); err != nil {
		t.Fatalf("EnsureCredentialHelper() error = %v", err)
	}

	got := credentialHelpers(t, repo)
	want := []string{"", LoomCredentialHelper}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("credential.helper = %#v, want %#v", got, want)
	}
}

func TestEnsureCredentialHelperIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := newCredentialRepo(t, "repo", "https://github.com/example/repo")

	for i := 0; i < 2; i++ {
		if err := EnsureCredentialHelper(context.Background(), repo); err != nil {
			t.Fatalf("EnsureCredentialHelper() call %d error = %v", i+1, err)
		}
	}

	if got := credentialHelpers(t, repo); len(got) != 2 {
		t.Fatalf("credential.helper after two calls = %#v, want 2 entries", got)
	}
}

func TestEnsureCredentialHelperSkipsNonHTTPSRemotes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	for name, remote := range map[string]string{
		"ssh":       "git@github.com:example/repo.git",
		"file":      "file:///tmp/example.git",
		"localpath": "/tmp/example.git",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newCredentialRepo(t, "repo", remote)
			if err := EnsureCredentialHelper(context.Background(), repo); err != nil {
				t.Fatalf("EnsureCredentialHelper() error = %v", err)
			}
			if out, err := gitMaybe(repo, "config", "--local", "--get-all", "credential.helper"); err == nil {
				t.Fatalf("credential.helper was set for %s remote: %q", name, strings.TrimSpace(out))
			}
		})
	}
}

// TestEnsureCredentialHelperFillsFromEnvironment is the behavioral check: it
// asks git itself for the credential it would use, which is what actually
// proves the daemon's agents can push.
func TestEnsureCredentialHelperFillsFromEnvironment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := newCredentialRepo(t, "repo", "https://github.com/example/repo")
	if err := EnsureCredentialHelper(context.Background(), repo); err != nil {
		t.Fatalf("EnsureCredentialHelper() error = %v", err)
	}

	withToken, err := credentialFill(repo, "GITHUB_TOKEN=sentinel")
	if err != nil {
		t.Fatalf("git credential fill with token error = %v\n%s", err, withToken)
	}
	if !strings.Contains(withToken, "username=x-access-token") {
		t.Fatalf("git credential fill output missing username: %q", withToken)
	}
	if !strings.Contains(withToken, "password=sentinel") {
		t.Fatalf("git credential fill output missing password: %q", withToken)
	}

	withGHToken, err := credentialFill(repo, "GH_TOKEN=fallback")
	if err != nil {
		t.Fatalf("git credential fill with GH_TOKEN error = %v\n%s", err, withGHToken)
	}
	if !strings.Contains(withGHToken, "password=fallback") {
		t.Fatalf("git credential fill did not fall back to GH_TOKEN: %q", withGHToken)
	}

	// With no token in the environment there is nothing to hand out; the helper
	// must not invent one.
	withoutToken, _ := credentialFill(repo)
	if strings.Contains(withoutToken, "password=sentinel") || strings.Contains(withoutToken, "password=fallback") {
		t.Fatalf("git credential fill leaked a token with no env var set: %q", withoutToken)
	}
}

// credentialFill runs `git credential fill` for github.com in repo with the
// given extra environment entries and an otherwise token-free environment.
func credentialFill(repo string, extraEnv ...string) (string, error) {
	cmd := exec.Command("git", "credential", "fill") //nolint:norawexec,gosec // fixed test helper command.
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + repo,
		"GIT_TERMINAL_PROMPT=0",
	}, extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestCloneRepoToLeavesLocalRemoteWithoutHelper(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	target := filepath.Join(root, "clone")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "v1\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	if err := CloneRepoTo(context.Background(), remote, target); err != nil {
		t.Fatalf("CloneRepoTo() error = %v", err)
	}
	if out, err := gitMaybe(target, "config", "--local", "--get-all", "credential.helper"); err == nil {
		t.Fatalf("credential.helper was set for a local-path clone: %q", strings.TrimSpace(out))
	}
}
