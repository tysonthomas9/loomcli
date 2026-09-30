//go:build darwin || linux

package driver

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"

	_ "modernc.org/sqlite"
)

func TestTaskRunnerCancelSignalsWholeProcessGroup(t *testing.T) {
	markerDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTaskRunnerSignalHelper$") //nolint:norawexec // Exercise the process-group cancellation boundary with this test binary.
	cmd.Env = append(os.Environ(), "LOOM_TEST_SIGNAL_ROLE=parent", "LOOM_TEST_SIGNAL_DIR="+markerDir)
	configureTaskRunnerProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer terminateTaskRunnerGroup(cmd)
	waitForSignalMarker(t, filepath.Join(markerDir, "child-ready"))
	cancel()
	_ = cmd.Wait()
	waitForSignalMarker(t, filepath.Join(markerDir, "child-int"))
}

func TestTaskRunnerSignalHelper(t *testing.T) {
	role := os.Getenv("LOOM_TEST_SIGNAL_ROLE")
	if role == "" {
		return
	}
	dir := os.Getenv("LOOM_TEST_SIGNAL_DIR")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT)
	defer signal.Stop(signals)
	if role == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestTaskRunnerSignalHelper$") //nolint:norawexec // The child must inherit the parent's process group.
		child.Env = append(os.Environ(), "LOOM_TEST_SIGNAL_ROLE=child")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(filepath.Join(dir, "child-ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-signals
	if role == "child" {
		if err := os.WriteFile(filepath.Join(dir, "child-int"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForSignalMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("signal marker %s did not appear", path)
}

type cancelTestResolver struct{ copy TaskWorktree }

func (r cancelTestResolver) ResolveTaskWorktree(context.Context, TaskExecRequest, string) (TaskWorktree, error) {
	return r.copy, nil
}

func TestCancelCapturesRetainedCopyAndKillsCLIChild(t *testing.T) {
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	config := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", config)
	childPID := filepath.Join(repo.dir, "child.pid")
	script := filepath.Join(t.TempDir(), "runner.sh")
	content := "#!/bin/sh\ntrap '' INT\nprintf 'partial\\n' > file.txt\nsleep 60 &\necho $! > '" + childPID + "'\nwait\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	oldDelay := taskRunnerKillDelay
	taskRunnerKillDelay = 100 * time.Millisecond
	t.Cleanup(func() { taskRunnerKillDelay = oldDelay })
	req := hostBridgeTaskExecRequest()
	req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
	req.RunnerTrustLevel = domain.DriverTrustTrusted
	copy := TaskWorktree{Path: repo.dir, SourcePath: repo.dir, RepoName: "repo", AttemptID: "attempt-1", BaseSHA: base}
	st := memstore.New()
	if _, err := st.TaskRuns().Create(context.Background(), store.TaskRunCreate{
		WorkspaceKey: req.WorkspaceKey, TaskRunID: req.TaskRunID, TaskID: req.TaskID, Status: domain.TaskRunRunning,
	}); err != nil {
		t.Fatal(err)
	}
	e := HostBridgeTaskExecutor{Store: st, WorktreePath: repo.dir,
		WorktreeResolver: cancelTestResolver{copy}, Command: []string{script}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type answer struct {
		result TaskExecResult
		err    error
	}
	done := make(chan answer, 1)
	go func() { r, err := e.ExecuteTask(ctx, req); done <- answer{r, err} }()
	var pid int
	deadline := time.After(5 * time.Second)
	for pid == 0 {
		if b, err := os.ReadFile(childPID); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		select {
		case <-deadline:
			t.Fatal("CLI child did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	select {
	case got := <-done:
		if got.err != nil || got.result.Status != domain.TaskRunCancelled || got.result.RuntimeMetadata["revision"] != "1" {
			t.Fatalf("cancel result = %+v, %v", got.result, got.err)
		}
		if got.result.RuntimeMetadata["retained_path"] != repo.dir || repo.read("file.txt") != "partial\n" {
			t.Fatalf("task copy lost: %+v", got.result)
		}
		store, err := sql.Open("sqlite", filepath.Join(config, "loomgit", "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.Close() }()
		var outcome string
		var ready int
		err = store.QueryRow(`SELECT outcome, ready FROM change_revisions WHERE workspace = ? AND change_id = ? AND number = 1`, "WS", got.result.RuntimeMetadata["change_id"]).Scan(&outcome, &ready)
		if err != nil || outcome != "cancelled" || ready != 1 {
			t.Fatalf("revision = %s/%d, %v", outcome, ready, err)
		}
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("CLI child %d survived cancellation", pid)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not finish")
	}
}

func TestCancelMarksIncompleteCaptureAndRetainsCopy(t *testing.T) {
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	repo.write("file.txt", "partial\n")
	repo.write(".env", "SECRET=do-not-capture\n")
	config := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", config)
	req := hostBridgeTaskExecRequest()
	copy := TaskWorktree{Path: repo.dir, SourcePath: repo.dir, RepoName: "repo", AttemptID: "attempt-incomplete", BaseSHA: base}
	result, err := (HostBridgeTaskExecutor{}).captureCancelledTask(req, copy)
	if err != nil || result.Status != domain.TaskRunCancelled || result.RuntimeMetadata["revision_incomplete"] != "true" {
		t.Fatalf("incomplete cancel = %+v, %v", result, err)
	}
	if repo.read("file.txt") != "partial\n" || repo.read(".env") != "SECRET=do-not-capture\n" {
		t.Fatal("retained task copy changed")
	}
	store, err := sql.Open("sqlite", filepath.Join(config, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	var incomplete int
	err = store.QueryRow(`SELECT incomplete FROM change_revisions WHERE workspace = ? AND change_id = ? AND number = 1`, "WS", result.RuntimeMetadata["change_id"]).Scan(&incomplete)
	if err != nil || incomplete != 1 {
		t.Fatalf("incomplete flag = %d, %v", incomplete, err)
	}
	if got := repo.git("show", result.RuntimeMetadata["revision_head_sha"]+":file.txt"); strings.TrimSpace(got) != "partial" {
		t.Fatalf("revision missed partial edit: %q", got)
	}
}

func TestCancelFreezesCommittedTaskCopyWork(t *testing.T) {
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	committed := repo.commitFile("file.txt", "committed edit\n", "agent work")
	config := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", config)
	req := hostBridgeTaskExecRequest()
	copy := TaskWorktree{Path: repo.dir, SourcePath: repo.dir, RepoName: "repo", AttemptID: "committed-at-cancel", BaseSHA: base}
	result, err := (HostBridgeTaskExecutor{}).captureCancelledTask(req, copy)
	if err != nil || result.RuntimeMetadata["revision"] != "1" || result.RuntimeMetadata["revision_incomplete"] != "false" {
		t.Fatalf("committed cancel = %+v, %v", result, err)
	}
	if strings.TrimSpace(repo.git("rev-parse", "HEAD")) != committed ||
		strings.TrimSpace(repo.git("show", result.RuntimeMetadata["revision_head_sha"]+":file.txt")) != "committed edit" {
		t.Fatal("cancelled revision or retained task copy lost the agent commit")
	}
}
