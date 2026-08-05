//go:build unix

package workspacemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunWorkspaceGitContextCancelsGitProcessGroup(t *testing.T) {
	binDir := t.TempDir()
	started := filepath.Join(t.TempDir(), "started")
	survived := filepath.Join(t.TempDir(), "survived")
	script := "#!/bin/sh\n" +
		"touch '" + started + "'\n" +
		"(sleep 0.4; touch '" + survived + "') &\n" +
		"wait\n"
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
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake Git process did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	fenceErr := errors.New("test owner fence lost")
	cancel(fenceErr)
	if err := <-result; !errors.Is(err, fenceErr) {
		t.Fatalf("Git cancellation error = %v, want fence cause", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(survived); !os.IsNotExist(err) {
		t.Fatalf("Git child survived process-group cancellation: stat error = %v", err)
	}
}
