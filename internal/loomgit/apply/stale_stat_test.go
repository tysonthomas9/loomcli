package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// overlapFixture's approved revision also rewrites the tracked file "base".
func overlapFixture(t *testing.T) *fixture {
	return fixtureWithSource(t, func(t *testing.T, f *fixture) {
		f.commit(t, "base", "next\n", "next\n\nLoom-Change-Id: C1\nLoom-Revision: 1")
	})
}

// restoreWithNewStat puts back the exact checked-out bytes with a later mtime,
// so only the index's cached stat data is stale. No Git command runs here,
// because a status or diff outside the apply lock would refresh the index.
func restoreWithNewStat(t *testing.T, f *fixture, name, body string) {
	t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, f *fixture, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApplyRetryAfterRestoredOverlapIgnoresStaleIndexStat(t *testing.T) {
	f := overlapFixture(t)
	f.write(t, "base", "unsaved\n")
	if _, err := f.apply(t); !errors.Is(err, loomgit.NewError(loomgit.ApplyPending, "", nil)) {
		t.Fatalf("held apply: %v", err)
	}
	restoreWithNewStat(t, f, "base", "base\n")
	got, err := f.apply(t)
	if err != nil {
		t.Fatalf("retry after restoring the exact bytes: %v", err)
	}
	if got.HeadSHA != f.source || f.git(t, "rev-parse", "HEAD") != f.source || readFile(t, f, "base") != "next\n" {
		t.Fatalf("retry did not install the revision: %+v, base=%q", got, readFile(t, f, "base"))
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".git", "index.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index lock left behind: %v", err)
	}
}

func TestApplyInstallRefusalOnUntouchedCheckoutReleasesLockForRetry(t *testing.T) {
	f := overlapFixture(t)
	f.write(t, "user", "untracked\n")
	f.service.beforeReadTree = func() {
		f.service.beforeReadTree = nil
		// Same bytes, new stat after the refresh: read-tree refuses and writes nothing.
		restoreWithNewStat(t, f, "base", "base\n")
	}
	_, err := f.apply(t)
	if !errors.Is(err, loomgit.NewError(loomgit.ApplyPending, "", nil)) {
		t.Fatalf("refused install: %v", err)
	}
	if f.git(t, "rev-parse", "HEAD") != f.base || readFile(t, f, "base") != "base\n" || readFile(t, f, "user") != "untracked\n" {
		t.Fatal("refused install changed the checkout")
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".git", "index.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused install kept the user's index lock: %v", err)
	}
	if open, err := f.store.OpenApplied(context.Background(), "W", "L"); err != nil || len(open) != 0 {
		t.Fatalf("refused install left an open apply: %+v, %v", open, err)
	}
	got, err := f.apply(t)
	if err != nil || got.HeadSHA != f.source || readFile(t, f, "base") != "next\n" {
		t.Fatalf("same request retry: %+v, %v", got, err)
	}
}
