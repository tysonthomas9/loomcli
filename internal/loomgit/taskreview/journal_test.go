package taskreview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
)

type listingIssues struct {
	fakeIssues
	listed []backend.ListOpts
}

func (l *listingIssues) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	l.listed = append(l.listed, opts)
	return []backend.IssueData{{ID: "T1"}}, nil
}

// The reconcile loop finds tasks in code review through the journal: an empty
// attempt frozen after its agent finished closes its task ("No changes"), and a
// verdict on a change with code still awaiting review leaves its task alone.
func TestSettleAllAndSettleChangeReadTheJournal(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "loomgit", "store.db")
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary Git repository validates revision objects.
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
	if _, err := driverfreeze.FreezeCaptureAt(ctx, journalPath, driverfreeze.CaptureRequest{Workspace: "W", Task: "T1",
		Repo: "repo", Attempt: "empty", Worktree: repo, Base: base, CaptureSHA: base, Outcome: "completed",
		Complete: true, SkipRetention: true}); err != nil {
		t.Fatal(err)
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

	issues := &listingIssues{fakeIssues: fakeIssues{issue: issueWith("review", backend.CodeReviewLabel)}}
	var workspaces []string
	prior := issuesFor
	issuesFor = func(ctx context.Context, workspace string) (context.Context, backend.IssueBackend) {
		workspaces = append(workspaces, workspace)
		return ctx, issues
	}
	t.Cleanup(func() { issuesFor = prior })

	if err := SettleAll(ctx, journalPath); err != nil {
		t.Fatalf("SettleAll: %v", err)
	}
	wantList := []backend.ListOpts{{Status: "review", Labels: []string{backend.CodeReviewLabel}, Limit: 1000}}
	if !reflect.DeepEqual(workspaces, []string{"W"}) || !reflect.DeepEqual(issues.listed, wantList) {
		t.Fatalf("workspaces %v listed %+v, want W's tasks in code review", workspaces, issues.listed)
	}
	if !reflect.DeepEqual(issues.closed, []string{"No changes"}) {
		t.Fatalf("closed %v, want T1 closed as No changes", issues.closed)
	}

	issues.closed, issues.updates = nil, nil
	if got, err := SettleChange(ctx, journalPath, "W", changed.Change); err != nil || got != Wait ||
		len(issues.closed)+len(issues.updates) != 0 {
		t.Fatalf("SettleChange = %q %v closed %v updates %+v, want T2 kept in review", got, err, issues.closed, issues.updates)
	}
	if got, err := SettleChange(ctx, filepath.Join(t.TempDir(), "none.db"), "W", changed.Change); err != nil || got != Wait {
		t.Fatalf("no journal: %q %v, want nothing to settle", got, err)
	}

	// A close that fails leaves T1 in code review, so the next pass closes it.
	sticky := &stickyIssues{status: "review", labels: []string{backend.CodeReviewLabel}, failCloses: 1}
	issuesFor = func(ctx context.Context, _ string) (context.Context, backend.IssueBackend) { return ctx, sticky }
	if err := SettleAll(ctx, journalPath); err == nil || sticky.status != "review" || !backend.HasCodeReviewLabel(sticky.labels) {
		t.Fatalf("failed close: %v, task %s %v; want an error and T1 still in code review", err, sticky.status, sticky.labels)
	}
	if err := SettleAll(ctx, journalPath); err != nil || sticky.status != "closed" {
		t.Fatalf("retry: %v, task %s; want T1 closed", err, sticky.status)
	}
}
