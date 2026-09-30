package applyrecovery_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestReconcileJournalRecoversInterruptedApplyAtStartupAndTick(t *testing.T) {
	ctx, store, area, base := recoveryFixture(t)
	defer func() { _ = store.Close() }()
	untracked := filepath.Join(area.Path, "unfinished")
	if err := os.WriteFile(untracked, []byte("lead work"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, requestID := range []string{"startup", "tick"} {
		if err := store.SaveApplied(ctx, loomgit.AppliedLayer{
			RequestID: requestID, Workspace: "W", Lead: "L", Change: "C", Revision: 1,
			OldTip: base, NewTip: base,
		}); err != nil {
			t.Fatal(err)
		}
		if err := workspacemgr.ReconcileJournal(ctx, memstore.New()); err != nil {
			t.Fatalf("%s reconcile: %v", requestID, err)
		}
		open, err := store.OpenApplied(ctx, "W", "L")
		if err != nil || len(open) != 0 {
			t.Fatalf("%s still open: %+v, %v", requestID, open, err)
		}
		if got, err := os.ReadFile(untracked); err != nil || string(got) != "lead work" {
			t.Fatalf("%s removed untracked work: %q, %v", requestID, got, err)
		}
	}
}

func TestReconcileJournalAmbiguousApplyRequiresAttention(t *testing.T) {
	ctx, store, area, base := recoveryFixture(t)
	defer func() { _ = store.Close() }()
	second := area
	second.Repo = "other"
	if err := store.SaveWorkingAreas(ctx, []journal.WorkingArea{second}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplied(ctx, loomgit.AppliedLayer{
		RequestID: "ambiguous", Workspace: "W", Lead: "L", Change: "C", Revision: 1,
		OldTip: base, NewTip: base,
	}); err != nil {
		t.Fatal(err)
	}
	err := workspacemgr.ReconcileJournal(ctx, memstore.New())
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.AttentionRequired) {
		t.Fatalf("ambiguous recovery = %v, want attention_required", err)
	}
	open, err := store.OpenApplied(ctx, "W", "L")
	if err != nil || len(open) != 1 || open[0].Phase != "prepared" {
		t.Fatalf("ambiguous layer changed: %+v, %v", open, err)
	}
}

func TestReconcileJournalUnknownOperationDoesNotReplayApply(t *testing.T) {
	ctx, store, _, base := recoveryFixture(t)
	defer func() { _ = store.Close() }()
	if _, _, err := store.Begin(ctx, "unknown", "unclassified"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplied(ctx, loomgit.AppliedLayer{
		RequestID: "pending", Workspace: "W", Lead: "L", Change: "C", Revision: 1,
		OldTip: base, NewTip: base,
	}); err != nil {
		t.Fatal(err)
	}
	err := workspacemgr.ReconcileJournal(ctx, memstore.New())
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.AttentionRequired) {
		t.Fatalf("unknown journal = %v, want attention_required", err)
	}
	open, err := store.OpenApplied(ctx, "W", "L")
	if err != nil || len(open) != 1 || open[0].Phase != "prepared" {
		t.Fatalf("unknown journal changed apply: %+v, %v", open, err)
	}
}

func recoveryFixture(t *testing.T) (context.Context, *journal.SQLite, journal.WorkingArea, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	path := filepath.Join(root, "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "area")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	recoveryGit(t, repo, "init", "-b", "loom/ws/W/interactive/L")
	recoveryGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "base")
	base := recoveryGit(t, repo, "rev-parse", "HEAD")
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	area := journal.WorkingArea{Workspace: "W", Lead: "L", Repo: "repo", Path: repo,
		Branch: "loom/ws/W/interactive/L", BaseSHA: base, Mode: "worktree"}
	ctx := context.Background()
	if err := store.SaveWorkingAreas(ctx, []journal.WorkingArea{area}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	return ctx, store, area, base
}

func recoveryGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...) //nolint:norawexec // Disposable local Git repository is the recovery fixture.
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
