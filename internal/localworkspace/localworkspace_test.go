package localworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateWorktreeName(t *testing.T) {
	t.Parallel()

	validNames := []string{
		"falcon",
		"feature-auth",
		"agent_1",
		"release.2026",
		"v1",
	}
	for _, name := range validNames {
		t.Run("valid_"+name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateWorktreeName(name); err != nil {
				t.Fatalf("ValidateWorktreeName(%q) = %v, want nil", name, err)
			}
		})
	}

	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "reserved", value: "__workspace__", wantErr: "reserved"},
		{name: "reserved_case_insensitive", value: "__WORKSPACE__", wantErr: "reserved"},
		{name: "length_cap", value: strings.Repeat("a", maxWorktreeNameLength+1), wantErr: "100 characters"},
		{name: "leading_dash", value: "-flag", wantErr: "must not start with '-'"},
		{name: "current_dir", value: ".", wantErr: "must not be '.' or '..'"},
		{name: "parent_dir", value: "..", wantErr: "must not be '.' or '..'"},
		{name: "traversal", value: "../secret", wantErr: "must not contain '..'"},
		{name: "embedded_dotdot", value: "a..b", wantErr: "must not contain '..'"},
		{name: "slash", value: "feature/auth", wantErr: "invalid characters"},
		{name: "space", value: "feature auth", wantErr: "invalid characters"},
		{name: "tilde", value: "feature~auth", wantErr: "invalid characters"},
		{name: "caret", value: "feature^auth", wantErr: "invalid characters"},
		{name: "question", value: "feature?auth", wantErr: "invalid characters"},
		{name: "star", value: "feature*auth", wantErr: "invalid characters"},
		{name: "open_bracket", value: "feature[auth", wantErr: "invalid characters"},
		{name: "reflog", value: "feature@{auth", wantErr: "must not contain '@{'"},
		{name: "leading_dot", value: ".hidden", wantErr: "must not start or end with '.'"},
		{name: "trailing_dot", value: "hidden.", wantErr: "must not start or end with '.'"},
		{name: "trailing_lock", value: "feature.lock", wantErr: "must not end with '.lock'"},
		{name: "all_whitespace", value: " \t ", wantErr: "blank"},
		{name: "empty", value: "", wantErr: "cannot be empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateWorktreeName(tc.value)
			if err == nil {
				t.Fatalf("ValidateWorktreeName(%q) = nil, want error containing %q", tc.value, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateWorktreeName(%q) = %v, want error containing %q", tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestTerminalGroupRootPath(t *testing.T) {
	t.Parallel()

	ws := filepath.Join(t.TempDir(), "workspace")
	got, err := TerminalGroupRootPath(ws, "feature-auth")
	if err != nil {
		t.Fatalf("TerminalGroupRootPath() error = %v", err)
	}
	absWS, err := filepath.Abs(ws)
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	want := filepath.Join(absWS, ".loom", "terminal-worktrees", "feature-auth")
	if got != want {
		t.Fatalf("TerminalGroupRootPath() = %q, want %q", got, want)
	}

	escapingNames := []string{
		"../escape",
		filepath.Join("..", "escape"),
		"feature/auth",
	}
	for _, name := range escapingNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got, err := TerminalGroupRootPath(ws, name); err == nil {
				t.Fatalf("TerminalGroupRootPath(%q) = %q, want error", name, got)
			}
		})
	}
}

func TestIsGitLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	linked := filepath.Join(root, "linked")

	git(t, "", "init", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "README.md"), "hello\n")
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "init")
	git(t, repo, "worktree", "add", "-b", "feature", linked)

	if IsGitLinkedWorktree(repo) {
		t.Fatalf("IsGitLinkedWorktree(source repo) = true, want false")
	}
	if !IsGitLinkedWorktree(linked) {
		t.Fatalf("IsGitLinkedWorktree(linked worktree) = false, want true")
	}
	if IsGitLinkedWorktree(filepath.Join(root, "missing")) {
		t.Fatalf("IsGitLinkedWorktree(missing) = true, want false")
	}
}

func TestEnsureGitWorktreeFromBranchCtxCancelsGitInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake git is POSIX-only")
	}

	fakeDir := t.TempDir()
	marker := filepath.Join(fakeDir, "started")
	fakeGit := filepath.Join(fakeDir, "git")
	script := "#!/bin/sh\nprintf started > \"$FAKE_GIT_STARTED\"\nexec sleep 10\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GIT_STARTED", marker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- EnsureGitWorktreeFromBranchCtx(ctx, t.TempDir(), filepath.Join(t.TempDir(), "target"), "worker", "origin", "main")
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, statErr := os.Stat(marker); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake git was not invoked")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancelStart := time.Now()
	cancel()

	var err error
	select {
	case err = <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("EnsureGitWorktreeFromBranchCtx() did not return after context cancellation")
	}
	elapsed := time.Since(cancelStart)
	if err == nil {
		t.Fatalf("EnsureGitWorktreeFromBranchCtx() = nil, want context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureGitWorktreeFromBranchCtx() error = %v, want context canceled", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("EnsureGitWorktreeFromBranchCtx() took %s, command was not killed promptly", elapsed)
	}
}

func TestEnsureGitWorktreeFromBranchUsesFetchedDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

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

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")

	writeFile(t, filepath.Join(seed, "base.txt"), "v2\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "advance")
	git(t, seed, "push", "origin", "main")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "main"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "v2\n" {
		t.Fatalf("target base.txt = %q, want fetched v2", got)
	}
}

func TestEnsureGitWorktreeFromBranchFallsBackToLocalDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "main\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "-b", "browser-e2e")
	writeFile(t, filepath.Join(repo, "base.txt"), "local branch\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "local branch")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "browser-e2e"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "local branch\n" {
		t.Fatalf("target base.txt = %q, want local branch content", got)
	}
}

func TestGitRemoteURL(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const url = "https://github.com/owner/repo.git"
	dir := t.TempDir()
	git(t, "", "init", dir)
	git(t, dir, "remote", "add", "origin", url)

	got, err := GitRemoteURL(dir, "origin")
	if err != nil {
		t.Fatalf("GitRemoteURL: %v", err)
	}
	if got != url {
		t.Errorf("GitRemoteURL = %q, want %q", got, url)
	}

	// Empty remote name defaults to origin.
	if got, err := GitRemoteURL(dir, ""); err != nil || got != url {
		t.Errorf("GitRemoteURL(\"\") = %q, %v; want %q", got, err, url)
	}

	// A non-git directory is reported as an error (the "not a usable checkout" signal).
	if _, err := GitRemoteURL(t.TempDir(), "origin"); err == nil {
		t.Error("GitRemoteURL on a non-git dir should return an error")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper commands.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
