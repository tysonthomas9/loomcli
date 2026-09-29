package stacklock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestWithDefaultJournalReleasesAndScopesStacks(t *testing.T) {
	ctx := context.Background()
	called := false
	err := With(ctx, "ws", "one", func(context.Context) error {
		return With(ctx, "ws", "two", func(context.Context) error {
			called = true
			return nil
		})
	})
	if err != nil || !called {
		t.Fatalf("independent stack entry: called=%v err=%v", called, err)
	}
	if err := With(ctx, "ws", "one", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("released stack entry: %v", err)
	}
}

func TestExpiredLeaseFromDeadProcessIsRecovered(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	child := exec.Command(os.Args[0], "-test.run=^TestDeadHolderHelper$") //nolint:norawexec // Proves recovery after a real process exits.
	child.Env = append(os.Environ(), "LOOM_STACKLOCK_HELPER=1")
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("dead holder: %v: %s", err, out)
	}
	start := time.Now()
	if err := With(context.Background(), "ws", "dead", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("stale lease was not recovered: %v", err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("lease was available before the dead holder's TTL elapsed")
	}
}

func TestDeadHolderHelper(t *testing.T) {
	if os.Getenv("LOOM_STACKLOCK_HELPER") != "1" {
		return
	}
	store, err := journal.OpenSQLite(filepath.Join(bootstrap.LoomDir(), "loomgit-journal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.ClaimLease(context.Background(), "stack:ws:dead", "dead-child", 800*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestHeldLeaseTimesOutWithStackLocked(t *testing.T) {
	store := loomgitStore(t)
	ctx := context.Background()
	if _, err := store.ClaimLease(ctx, "stack:ws:held", "holder", time.Minute); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := withStore(ctx, store, "stack:ws:held", 80*time.Millisecond, time.Second, func(context.Context) error {
		t.Fatal("action ran without the lock")
		return nil
	})
	if !errors.Is(err, loomgit.NewError(loomgit.StackLocked, "", nil)) {
		t.Fatalf("error = %v, want stack_locked", err)
	}
	if time.Since(start) < 80*time.Millisecond {
		t.Fatal("did not wait for timeout")
	}
}

func loomgitStore(t *testing.T) loomgit.Store {
	t.Helper()
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "journal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
