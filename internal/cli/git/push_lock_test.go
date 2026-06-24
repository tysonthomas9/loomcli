package git

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPushBranchInRepo_WaitsForRepoPushLock(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	repoPath := filepath.Join(t.TempDir(), "repo")

	held, err := acquireRepoPushLock(repoPath)
	if err != nil {
		t.Fatalf("acquire held lock: %v", err)
	}

	outputStubs := []OutputCommandStub{
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: nil, Err: nil},
		{Args: []string{"checkout", "feature"}, Err: nil},
	}
	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"remote", "get-url", "origin"}, Err: errNoRemoteForPushLockTest{}},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},
		{Name: "git", Args: []string{"log", "main..feature", "--oneline"}, Stdout: "abc commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)
	deps.Agent = &MockAgentInvoker{}

	done := make(chan error, 1)
	go func() {
		done <- pushBranchInRepo(deps, repoPath, "feature", "main", "")
	}()

	select {
	case err := <-done:
		t.Fatalf("push completed while repo integration lock was still held: %v", err)
	case <-time.After(75 * time.Millisecond):
	}

	if len(cmdMock.Calls()) != 0 {
		t.Fatalf("push executed git commands before acquiring integration lock: %#v", cmdMock.Calls())
	}

	if err := held.Release(); err != nil {
		t.Fatalf("release held lock: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("push after lock release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("push did not proceed after repo integration lock was released")
	}
}

type errNoRemoteForPushLockTest struct{}

func (errNoRemoteForPushLockTest) Error() string { return "no remote" }

var _ error = errNoRemoteForPushLockTest{}
