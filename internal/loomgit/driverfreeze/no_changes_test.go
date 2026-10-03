package driverfreeze_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type epicTaskRuns struct {
	store.TaskRunStore
	runs []*domain.TaskRun
}

func (e epicTaskRuns) List(context.Context, string, store.TaskRunFilter) ([]*domain.TaskRun, error) {
	return e.runs, nil
}

// D29 (4): an epic's PRs carry only tasks with changes. A task that changed
// nothing can never be applied, so it must not hold the epic's publication.
func TestEpicPublicationSkipsTasksWithNoChanges(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	journalPath := filepath.Join(configDir, "loomgit", "store.db")
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary Git repository validates revision objects and refs.
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "a")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	ctx := context.Background()
	empty, err := driverfreeze.FreezeCaptureAt(ctx, journalPath, driverfreeze.CaptureRequest{Workspace: "W", Task: "T1",
		Repo: "repo", Attempt: "empty", Worktree: repo, Base: base, CaptureSHA: base, Outcome: "completed",
		Complete: true, SkipRetention: true})
	if err != nil || !empty.NoChanges {
		t.Fatalf("empty attempt: %+v %v", empty, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := git("diff", "--binary", base)
	git("restore", "--worktree", ".")
	changed, err := driverfreeze.FreezeAt(ctx, journalPath, driverfreeze.Request{Workspace: "W", Task: "T2",
		Repo: "repo", Attempt: "changed", Worktree: repo, Base: base, Patch: []byte(patch + "\n"), Outcome: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	task := func(id, attempt string) *domain.TaskRun {
		return &domain.TaskRun{TaskRunID: id, TaskID: id, Status: domain.TaskRunCompleted,
			RuntimeMetadata: map[string]string{"attempt_id": attempt}}
	}
	payload := json.RawMessage(`{"leadName":"L","openPullRequest":true}`)
	record := func(runID string, tasks ...*domain.TaskRun) error {
		return driverfreeze.RecordEpicRun(ctx, epicTaskRuns{runs: tasks}, &domain.DriverRun{WorkspaceKey: "W", RunID: runID,
			Status: domain.DriverRunCompleted, Payload: payload})
	}
	if err := record("mixed", task("T1", "empty"), task("T2", "changed")); err != nil {
		t.Fatal(err)
	}
	if err := record("only-empty", task("T1", "empty")); err != nil {
		t.Fatalf("an epic whose only task changed nothing: %v", err)
	}
	s, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	pending, err := s.PendingEpicPublications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RunID != "mixed" || !slices.Equal(pending[0].Changes, []string{changed.Change}) {
		t.Fatalf("pending epic publications = %+v", pending)
	}
}
