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

func TestEnsureWorkingAreaSeparatesLeadsAndKeepsTheirWork(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, source, "init", "-b", "main")
	gitDeleteTest(t, source, "config", "user.name", "Tester")
	gitDeleteTest(t, source, "config", "user.email", "tester@example.test")
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, source, "add", "file")
	gitDeleteTest(t, source, "commit", "-m", "base")
	base := gitDeleteTest(t, source, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	st, err := journal.OpenSQLite(filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, _, err := st.Begin(ctx, "workspace-create:W", "ensure_workspace")
	if err != nil {
		t.Fatal(err)
	}
	entry, err = st.Advance(ctx, entry, "checkouts_added", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err = st.Advance(ctx, entry, "rows_written", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{{Workspace: "W", Repo: "repo", Trunk: "main", WorkspaceBranch: "loom/ws/W/interactive/lead", BaseSHA: base}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(root, "workspace")
	sources := []WorkingAreaSource{{Name: "repo", Path: source}}
	a, err := EnsureWorkingArea(ctx, "W", "L1", wsDir, sources)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EnsureWorkingArea(ctx, "W", "L2", wsDir, sources)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := EnsureWorkingArea(ctx, "W", "L3", wsDir, []WorkingAreaSource{{Name: "repo", Path: source, Mode: "clone"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitDeleteTest(t, clone[0].Path, "branch", "--show-current"); got != "loom/ws/W/interactive/L3" {
		t.Fatalf("clone branch = %q", got)
	}
	_, err = EnsureWorkingArea(ctx, "W", "L4", wsDir, []WorkingAreaSource{{Name: "repo", Path: source}, {Name: "missing", Path: source}})
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.TaskCopyCreateFailed {
		t.Fatalf("failed area = %v", err)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "worktrees", "repo", "L4")); !os.IsNotExist(err) {
		t.Fatalf("failed lead checkout retained: %v", err)
	}
	if a[0].Path == b[0].Path || gitDeleteTest(t, a[0].Path, "branch", "--show-current") != "loom/ws/W/interactive/L1" || gitDeleteTest(t, b[0].Path, "branch", "--show-current") != "loom/ws/W/interactive/L2" {
		t.Fatalf("areas: %+v %+v", a, b)
	}
	if err := os.WriteFile(filepath.Join(a[0].Path, "only-l1"), []byte("work"), 0600); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, a[0].Path, "add", "only-l1")
	gitDeleteTest(t, a[0].Path, "commit", "-m", "own")
	untracked := filepath.Join(a[0].Path, "unfinished-l1")
	if err := os.WriteFile(untracked, []byte("work in progress"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b[0].Path, "only-l1")); !os.IsNotExist(err) {
		t.Fatalf("L1 work visible in L2: %v", err)
	}
	again, err := EnsureWorkingArea(ctx, "W", "L1", wsDir, sources)
	if err != nil || again[0].Path != a[0].Path || gitDeleteTest(t, again[0].Path, "rev-parse", "HEAD") == base {
		t.Fatalf("lost L1 work: %+v %v", again, err)
	}
	if got, err := os.ReadFile(untracked); err != nil || string(got) != "work in progress" {
		t.Fatalf("cold working-area reuse lost untracked work: %q, %v", got, err)
	}
	if _, err := EnsureWorkingArea(ctx, "W", "L1", wsDir, []WorkingAreaSource{{Name: "other", Path: source}}); err == nil {
		t.Fatal("reused working area for a different repo")
	}
}
