package reconcile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestUnclassifiedJournalEntryRequiresAttentionWithoutTakeover(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	path := filepath.Join(root, "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	entry, _, err := st.Begin(ctx, "unknown-1", "unknown-operation")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = RunOnce(ctx, Handlers{Workspace: RecoverFunc(func(context.Context) error { called = true; return nil })})
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.AttentionRequired) || called {
		t.Fatalf("unknown journal = %v, recovery called = %v", err, called)
	}
	got, err := st.Get(ctx, entry.ID)
	if err != nil || got.Version != entry.Version || got.Fence != entry.Fence || got.Phase != entry.Phase {
		t.Fatalf("unknown entry changed: %+v, %v", got, err)
	}
}

func TestApplyJournalUsesRecoveryOwner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	path := filepath.Join(root, "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	if _, _, err := st.Begin(ctx, "apply-1", "apply"); err != nil {
		t.Fatal(err)
	}
	called := false
	err = RunOnce(ctx, Handlers{Apply: RecoverFunc(func(context.Context) error { called = true; return nil })})
	if err != nil || !called {
		t.Fatalf("apply recovery called = %v, err = %v", called, err)
	}
}

func TestLandingRunsAfterJournalRecoveryOnEveryPass(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	path := filepath.Join(root, "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	var calls int
	for range 2 {
		err := RunOnce(context.Background(), Handlers{Landing: RecoverFunc(func(context.Context) error {
			calls++
			return nil
		})})
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("landing passes = %d", calls)
	}
}
