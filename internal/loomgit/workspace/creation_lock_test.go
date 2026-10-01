package workspace

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestOpenCreationsSkipsActiveCreation(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	ctx := context.Background()
	session, err := BeginCloneRequest(ctx, "active", "active", "request", "main", filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	recoveries, err := OpenCreations(ctx)
	if err != nil || len(recoveries) != 0 {
		t.Fatalf("active creation recovered: %d, %v", len(recoveries), err)
	}
	if err := session.PlanClones(ctx, []Source{{Name: "repo", Path: filepath.Join(t.TempDir(), "repo")}}); err != nil {
		t.Fatalf("active journal lost ownership: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	recoveries, err = OpenCreations(ctx)
	if err != nil || len(recoveries) != 1 {
		t.Fatalf("interrupted creation recoveries = %d, %v", len(recoveries), err)
	}
	if err := recoveries[0].Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKilledCreatorIsRecovered(t *testing.T) {
	if os.Getenv("LOOM_CREATION_TEST_CHILD") == "1" {
		_, err := BeginCloneRequest(context.Background(), "killed", "killed", "request", "main", filepath.Join(config.GetConfigDir(), "workspace"))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = os.Stdout.WriteString("ready\n")
		time.Sleep(time.Hour)
		return
	}
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKilledCreatorIsRecovered$") //nolint:norawexec // A child process proves that a killed creator releases the OS lock.
	cmd.Env = append(os.Environ(), "LOOM_CREATION_TEST_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child readiness = %q, %v", line, err)
	}
	recoveries, err := OpenCreations(context.Background())
	if err != nil || len(recoveries) != 0 {
		t.Fatalf("live child adopted: %d, %v", len(recoveries), err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()
	recoveries, err = OpenCreations(context.Background())
	if err != nil || len(recoveries) != 1 {
		t.Fatalf("killed child recoveries = %d, %v", len(recoveries), err)
	}
	if err := recoveries[0].Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverySkipsCompletedSnapshot(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	ctx := context.Background()
	session, err := BeginCloneRequest(ctx, "completed", "completed", "request", "main", filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	entries, err := store.OpenEntries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("open entries = %d, %v", len(entries), err)
	}
	if err := session.RowsWritten(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	recovery, err := recoverOpenEntry(ctx, store, path, entries[0])
	if err != nil || recovery != nil {
		t.Fatalf("completed entry adopted: %v, %v", recovery, err)
	}
}

func TestRecoverySkipsAbortedSnapshot(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	ctx := context.Background()
	session, err := BeginCloneRequest(ctx, "aborted", "aborted", "request", "main", filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	entries, err := store.OpenEntries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("open entries = %d, %v", len(entries), err)
	}
	if err := session.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	recovery, err := recoverOpenEntry(ctx, store, path, entries[0])
	if err != nil || recovery != nil {
		t.Fatalf("aborted entry adopted: %v, %v", recovery, err)
	}
}
