//go:build unix

package workspacemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunWorkspaceGitContextCancelsGitProcessGroup(t *testing.T) {
	binDir := t.TempDir()
	childPIDPath := filepath.Join(t.TempDir(), "child-pid")
	script := "#!/bin/sh\n" +
		"sleep 30 &\n" +
		"child=$!\n" +
		"printf '%s\\n' \"$child\" > '" + childPIDPath + "'\n" +
		"wait \"$child\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancelCause(t.Context())
	result := make(chan error, 1)
	workDir := t.TempDir()
	go func() {
		_, err := runWorkspaceGitContext(ctx, workDir, "worktree", "add")
		result <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	childPID := 0
	for {
		pidBytes, err := os.ReadFile(childPIDPath)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			if err != nil || childPID <= 0 {
				t.Fatalf("invalid fake Git child PID %q: %v", pidBytes, err)
			}
			break
		}
		if !os.IsNotExist(err) {
			t.Fatalf("read fake Git child PID: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("fake Git child process did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	fenceErr := errors.New("test owner fence lost")
	cancel(fenceErr)
	if err := <-result; !errors.Is(err, fenceErr) {
		t.Fatalf("Git cancellation error = %v, want fence cause", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if err != nil {
			t.Fatalf("probe fake Git child %d: %v", childPID, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Git child %d survived process-group cancellation", childPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
