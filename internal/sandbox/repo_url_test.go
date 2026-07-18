package sandbox

import (
	"os/exec"
	"strings"
	"testing"
)

func TestResolveRepoURL_TransportValidation(t *testing.T) {
	absPath := t.TempDir()

	cases := []struct {
		name string
		url  string
		ok   bool
	}{
		{"http", "http://git.example.com/repo.git", true},
		{"https", "https://git.example.com/repo.git", true},
		{"file", "file:///tmp/repo.git", false},
		{"git", "git://git.example.com/repo.git", false},
		{"scp style", "git.example.com:org/repo.git", false},
		{"absolute path", absPath, false},
		{"relative path", ".", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOOM_SANDBOX_REPO_URL", tc.url)
			got, err := ResolveRepoURL(t.TempDir(), "")
			if tc.ok {
				if err != nil || got != tc.url {
					t.Fatalf("ResolveRepoURL = (%q, %v), want (%q, nil)", got, err, tc.url)
				}
				return
			}
			if err == nil {
				t.Fatalf("ResolveRepoURL(%q) unexpectedly succeeded: %q", tc.url, got)
			}
			if !strings.Contains(err.Error(), "LOOM_SANDBOX_REPO_URL") {
				t.Errorf("error %q does not name LOOM_SANDBOX_REPO_URL", err)
			}
		})
	}
}

func TestResolveRepoURL_ResolutionOrder(t *testing.T) {
	t.Setenv("LOOM_SANDBOX_HOST_GATEWAY", "gateway.internal")
	worktree := t.TempDir()
	cmd := exec.Command("git", "init") //nolint:norawexec // test creates a real local git repo
	cmd.Dir = worktree
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "remote", "add", "origin", "http://localhost:3000/origin.git") //nolint:norawexec // test creates a real local git repo
	cmd.Dir = worktree
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	t.Setenv("LOOM_SANDBOX_REPO_URL", "https://override.example/repo.git")
	got, err := ResolveRepoURL(worktree, "https://fallback.example/repo.git")
	if err != nil || got != "https://override.example/repo.git" {
		t.Fatalf("override resolution = (%q, %v)", got, err)
	}

	t.Setenv("LOOM_SANDBOX_REPO_URL", "")
	got, err = ResolveRepoURL(worktree, "https://fallback.example/repo.git")
	if err != nil || got != "http://gateway.internal:3000/origin.git" {
		t.Fatalf("origin resolution = (%q, %v)", got, err)
	}

	got, err = ResolveRepoURL(t.TempDir(), "https://fallback.example/repo.git")
	if err != nil || got != "https://fallback.example/repo.git" {
		t.Fatalf("fallback resolution = (%q, %v)", got, err)
	}
}

func TestResolveRepoURL_RewritesOnlyLoopbackHost(t *testing.T) {
	t.Setenv("LOOM_SANDBOX_REPO_URL", "")
	t.Setenv("LOOM_SANDBOX_HOST_GATEWAY", "gateway.internal")
	cases := []struct {
		name, raw, want string
	}{
		{"localhost path preserved", "http://localhost:3000/git/localhost-app.git", "http://gateway.internal:3000/git/localhost-app.git"},
		{"userinfo and port preserved", "http://user:pass@127.0.0.1:8443/repo.git", "http://user:pass@gateway.internal:8443/repo.git"},
		{"https hostname untouched", "https://git.example.com/localhost/repo.git", "https://git.example.com/localhost/repo.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveRepoURL(t.TempDir(), tc.raw)
			if err != nil || got != tc.want {
				t.Fatalf("ResolveRepoURL = (%q, %v), want (%q, nil)", got, err, tc.want)
			}
		})
	}
}
