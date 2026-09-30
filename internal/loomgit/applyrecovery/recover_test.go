package applyrecovery_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/outbox"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestReconcileJournalBridgeApprovalFollowsAfterCrash(t *testing.T) {
	ctx, journalStore, area, base := recoveryFixture(t)
	defer func() { _ = journalStore.Close() }()
	root := os.Getenv("LOOM_CONFIG_DIR")
	backend, err := bootstrap.OpenStore(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	if _, err := backend.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "W", Name: "Working area"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Store.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "W", Name: "repo"}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.MutateStateCache(func(state *bootstrap.StateCache) error {
		state.Workspaces["W"] = bootstrap.WorkspaceLocalState{Path: root, Repos: map[string]string{"repo": area.Path}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(area.Path, "change")
	if err := os.WriteFile(file, []byte("approved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recoveryGit(t, area.Path, "add", "-N", "change")
	patch := recoveryGit(t, area.Path, "diff", "--binary")
	recoveryGit(t, area.Path, "reset", "--", "change")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	runnerOutput, err := json.Marshal(map[string]any{
		"status": "completed", "exit_code": 0, "patch": patch + "\n", "patch_base_ref": base,
		"runtime_metadata": map[string]string{"repo_name": "repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "runner-output.json")
	if err := os.WriteFile(outputPath, runnerOutput, 0o600); err != nil {
		t.Fatal(err)
	}
	bridge := driver.HostBridgeTaskExecutor{Store: memstore.New(), WorktreePath: area.Path,
		Command: []string{"cat", outputPath}}
	result, err := bridge.ExecuteTask(ctx, driver.TaskExecRequest{WorkspaceKey: "W", DriverRunID: "driver-run",
		TaskRunID: "task-run", TaskID: "T", WorkerProfileID: "worker", ProviderProfile: "flue-daytona",
		LeaseID: "lease", LeaseToken: "token", FencingToken: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.RuntimeMetadata["patch_back_status"] != "frozen" {
		t.Fatalf("bridge did not freeze revision: %+v", result)
	}
	revision, err := journalStore.GetRevision(ctx, "W", result.RuntimeMetadata["change_id"], 1)
	if err != nil {
		t.Fatal(err)
	}
	if layers, err := journalStore.AppliedLog(ctx, "W", "L"); err != nil || len(layers) != 0 {
		t.Fatalf("bridge bypassed review gate: %+v, %v", layers, err)
	}
	if _, err := review.SubmitForLead(ctx, journalStore, "W", revision.Change, revision.Number, revision.HeadSHA,
		"approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L"); err != nil {
		t.Fatal(err)
	}
	if got := recoveryGit(t, area.Path, "rev-parse", "HEAD"); got != base {
		t.Fatalf("approval applied before reconciliation: %s", got)
	}
	if err := workspacemgr.ReconcileJournal(ctx, memstore.New()); err != nil {
		t.Fatal(err)
	}
	layers, err := journalStore.AppliedLog(ctx, "W", "L")
	if err != nil || len(layers) != 1 || layers[0].Change != revision.Change {
		t.Fatalf("recovered approval did not apply: %+v, %v", layers, err)
	}
	if got := recoveryGit(t, area.Path, "rev-parse", "HEAD"); got != layers[0].NewTip {
		t.Fatalf("working area did not follow recovered approval: %s", got)
	}
}

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

func TestReconcileEmitsClosedApplyEventAfterCrash(t *testing.T) {
	ctx, store, _, base := recoveryFixture(t)
	t.Setenv("LOOM_EVENTS_DIR", "")
	defer func() { _ = store.Close() }()
	if err := store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: "closed", Workspace: "W", Lead: "L",
		Change: "C", Revision: 1, OldTip: base, NewTip: base}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceApplied(ctx, "closed", "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	if err := workspacemgr.ReconcileJournal(ctx, memstore.New()); err != nil {
		t.Fatal(err)
	}
	if err := workspacemgr.ReconcileJournal(ctx, memstore.New()); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "events"))
	if err != nil || len(files) != 1 {
		t.Fatalf("recovered event log: %+v, %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "events", files[0].Name()))
	if err != nil || !strings.Contains(string(data), `"type":"git.integrated"`) ||
		strings.Count(string(data), `"event_id":"loomgit:1"`) != 1 {
		t.Fatalf("recovered event: %s, %v", data, err)
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 1 || !pending[0].JSONLEmitted {
		t.Fatalf("event should await SSE acknowledgement: %+v, %v", pending, err)
	}
}

func TestLateRecoveryEmitsJSONLWithoutExpiredSSE(t *testing.T) {
	ctx, store, _, base := recoveryFixture(t)
	t.Setenv("LOOM_EVENTS_DIR", "")
	defer func() { _ = store.Close() }()
	if err := store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: "late", Workspace: "W", Lead: "L",
		Change: "C", Revision: 1, OldTip: base, NewTip: base}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceApplied(ctx, "late", "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("LOOM_CONFIG_DIR")
	db, err := sql.Open("sqlite", filepath.Join(root, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE event_outbox_delivery SET created_at=unixepoch()-601`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("old event was not expired for SSE: %+v, %v", pending, err)
	}
	for range 2 {
		if err := workspacemgr.ReconcileJournal(ctx, memstore.New()); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(filepath.Join(root, "events"))
	if err != nil || len(files) != 1 {
		t.Fatalf("late recovery log: %+v, %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "events", files[0].Name()))
	if err != nil || strings.Count(string(data), `"event_id":"loomgit:1"`) != 1 {
		t.Fatalf("late event not emitted once: %s, %v", data, err)
	}
	var broadcasts int
	if err := outbox.Dispatch(ctx, store, outbox.EmitFunc(func(context.Context, loomgit.OutboxEvent) error {
		broadcasts++
		return nil
	})); err != nil || broadcasts != 0 {
		t.Fatalf("expired event reached SSE sink: %d, %v", broadcasts, err)
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
