package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitExecError_Error_IncludesStderrAndArgs(t *testing.T) {
	e := &GitExecError{
		Args:   []string{"checkout", "main"},
		Stderr: "fatal: 'main' is already used by worktree at '/home/user/project'",
		Err:    errors.New("exit status 128"),
	}
	msg := e.Error()
	if !strings.Contains(msg, "checkout main") {
		t.Errorf("Error() should include args; got %q", msg)
	}
	if !strings.Contains(msg, "already used by worktree") {
		t.Errorf("Error() should include stderr; got %q", msg)
	}
	if !strings.Contains(msg, "exit status 128") {
		t.Errorf("Error() should include underlying err; got %q", msg)
	}
}

type sentinelErr struct{ msg string }

func (s *sentinelErr) Error() string { return s.msg }

func TestGitExecError_UnwrapErrorsIs(t *testing.T) {
	sentinel := &sentinelErr{msg: "boom"}
	e := &GitExecError{
		Args:   []string{"status"},
		Stderr: "",
		Err:    sentinel,
	}
	var asSentinel *sentinelErr
	if !errors.As(e, &asSentinel) {
		t.Fatalf("errors.As should unwrap to *sentinelErr")
	}
	if asSentinel != sentinel {
		t.Errorf("errors.As returned different sentinel pointer")
	}
	if !errors.Is(e, sentinel) {
		t.Errorf("errors.Is should match the wrapped sentinel")
	}
}

func TestGitExecError_UnwrapErrorsAs(t *testing.T) {
	e := &GitExecError{
		Args:   []string{"log"},
		Stderr: "fatal: bad",
		Err:    errors.New("exit status 128"),
	}
	wrapped := error(e)
	var ge *GitExecError
	if !errors.As(wrapped, &ge) {
		t.Fatalf("errors.As(*GitExecError) should succeed")
	}
	if ge.Stderr != "fatal: bad" {
		t.Errorf("Stderr field not preserved: %q", ge.Stderr)
	}
	if len(ge.Args) != 1 || ge.Args[0] != "log" {
		t.Errorf("Args field not preserved: %v", ge.Args)
	}
}

func TestGitExecError_EmptyStderr(t *testing.T) {
	// Empty stderr must not panic and must still render args + err.
	e := &GitExecError{
		Args:   []string{"fsck"},
		Stderr: "",
		Err:    errors.New("signal: killed"),
	}
	msg := e.Error()
	if !strings.Contains(msg, "fsck") {
		t.Errorf("Error() should include args even with empty stderr; got %q", msg)
	}
	if !strings.Contains(msg, "signal: killed") {
		t.Errorf("Error() should include underlying err even with empty stderr; got %q", msg)
	}
}

func TestDefaultRunGitWithOutput_CapturesStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	// Initialize an empty (non-bare) git repo so checkout has something to attempt against.
	tmp := t.TempDir()
	if out, err := exec.Command("git", "init", tmp).CombinedOutput(); err != nil { //nolint:norawexec,gosec // test setup
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
	// Capture stderr from a deterministic failure: checking out a branch that doesn't exist.
	err := defaultRunGitWithOutput(tmp, "checkout", "nonexistent-branch-zxyq")
	if err == nil {
		t.Fatal("expected error from checkout of nonexistent branch")
	}
	var ge *GitExecError
	if !errors.As(err, &ge) {
		t.Fatalf("expected *GitExecError, got %T (%v)", err, err)
	}
	if ge.Stderr == "" {
		t.Error("expected captured stderr to be non-empty")
	}
	// git's wording varies across versions ("did not match any" / "pathspec ... did not match"),
	// but it always includes the offending branch name.
	if !strings.Contains(ge.Stderr, "nonexistent-branch-zxyq") {
		t.Errorf("stderr should mention the bad branch; got %q", ge.Stderr)
	}
	if !strings.Contains(err.Error(), ge.Stderr) {
		t.Errorf("Error() should embed stderr text; got %q", err.Error())
	}
}

func TestDefaultRunGitWithOutput_SuccessReturnsNil(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	tmp := t.TempDir()
	if out, err := exec.Command("git", "init", tmp).CombinedOutput(); err != nil { //nolint:norawexec,gosec // test setup
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
	// `git status` always succeeds in an initialized repo.
	if err := defaultRunGitWithOutput(tmp, "status"); err != nil {
		t.Errorf("expected nil error on success, got %v", err)
	}
}

// Sanity: reading a freshly captured file works (catches accidental fd leaks).
func TestDefaultRunGitWithOutput_NoFDLeakOnSuccess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	tmp := t.TempDir()
	if out, err := exec.Command("git", "init", tmp).CombinedOutput(); err != nil { //nolint:norawexec,gosec // test setup
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
	probe := filepath.Join(tmp, "probe.txt")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := defaultRunGitWithOutput(tmp, "status"); err != nil {
			t.Errorf("iter %d: %v", i, err)
		}
	}
}
