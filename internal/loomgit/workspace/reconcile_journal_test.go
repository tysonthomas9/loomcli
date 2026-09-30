package workspace

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
	err = CheckOpenEntries(ctx)
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.AttentionRequired) {
		t.Fatalf("unknown journal = %v, want attention_required", err)
	}
	got, err := st.Get(ctx, entry.ID)
	if err != nil || got.Version != entry.Version || got.Fence != entry.Fence || got.Phase != entry.Phase {
		t.Fatalf("unknown entry changed: %+v, %v", got, err)
	}
}
