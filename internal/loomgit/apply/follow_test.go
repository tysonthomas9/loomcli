package apply

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func TestApprovalFollowsWorkingAreaAfterResume(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "T1", "repo", "C1"); err != nil {
		t.Fatal(err)
	}
	if _, err := review.SubmitForLead(ctx, fixture.store, "W", "C1", 1, fixture.source,
		"approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fixture.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"W": {ID: "W", Repos: []config.RepoConfig{{Name: "repo", Path: fixture.dir}}},
	}}
	if err := fixture.store.SetFollowingPaused(ctx, "W", "L", true); err != nil {
		t.Fatal(err)
	}
	result, err := followWithStore(ctx, fixture.store, cfg, "W", "L")
	if err != nil || len(result.Pending) != 1 || fixture.git(t, "rev-parse", "HEAD") != fixture.base {
		t.Fatalf("paused approval moved working area: %+v, %v", result, err)
	}
	if err := fixture.store.SetFollowingPaused(ctx, "W", "L", false); err != nil {
		t.Fatal(err)
	}
	held := func(workspace, lead string) bool { return workspace == "W" && lead == "L" }
	if err := recoverPendingWithConfig(ctx, fixture.store, func() (*config.LoomConfig, error) { return cfg, nil }, held); err != nil ||
		fixture.git(t, "rev-parse", "HEAD") != fixture.base {
		t.Fatalf("recovery followed a held-back lead: %v", err)
	}
	if err := recoverPendingWithConfig(ctx, fixture.store, func() (*config.LoomConfig, error) { return cfg, nil }, nil); err != nil ||
		fixture.git(t, "rev-parse", "HEAD") != fixture.source {
		t.Fatalf("recovery did not follow durable approval: %v", err)
	}
	events, err := fixture.store.PendingEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundApplied := false
	for _, event := range events {
		if event.Kind == "git.integrated" && strings.Contains(string(event.Payload), `"workspace_sha":"`+fixture.source+`"`) {
			foundApplied = true
		}
	}
	if !foundApplied {
		t.Fatalf("applied outbox event missing new leaf: %+v", events)
	}
	result, err = followWithStore(ctx, fixture.store, cfg, "W", "L")
	if err != nil || len(result.Applied) != 0 {
		t.Fatalf("approval followed twice: %+v, %v", result, err)
	}
	if _, err := review.SubmitForLead(ctx, fixture.store, "W", "C1", 1, fixture.source,
		"approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L"); err != nil {
		t.Fatal(err)
	}
	result, err = followWithStore(ctx, fixture.store, cfg, "W", "L")
	if err != nil || len(result.Applied) != 0 {
		t.Fatalf("repeat verdict reapplied the layer: %+v, %v", result, err)
	}
	layers, err := fixture.store.AppliedLog(ctx, "W", "L")
	if err != nil || len(layers) != 1 {
		t.Fatalf("repeat verdict changed the applied log: %+v, %v", layers, err)
	}
}

func TestApprovalFollowsDependenciesInOrder(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "T1", "repo", "C1"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DriverChange(ctx, "W", "T2", "repo", "C2"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordLocalLineage(ctx, journal.LocalLineage{Workspace: "W", Task: "T2", Repo: "repo",
		PredecessorChange: "C1", PredecessorRevision: 1, BaseSHA: fixture.source}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fixture.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(t.TempDir(), "second")
	fixture.git(t, "worktree", "add", "-q", "--detach", secondPath, fixture.source)
	second, err := gitexec.New(secondPath, gitexec.Options{GlobalConfig: os.DevNull, SystemConfig: os.DevNull,
		FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondPath, "second"), []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "second"}, {"commit", "-qm", "second"}} {
		if _, err := second.Run(ctx, args...); err != nil {
			t.Fatal(err)
		}
	}
	secondHead, err := second.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	secondSHA := strings.TrimSpace(string(secondHead))
	revision, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C2",
		RequestID: "second-source", Kind: "source", Operation: "snapshot", Outcome: "completed",
		BaseSHA: fixture.source, TreeHash: fixture.git(t, "rev-parse", secondSHA+"^{tree}"), SourceHeadSHA: secondSHA})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = secondSHA
	if err := fixture.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.SubmitForLead(ctx, fixture.store, "W", "C2", revision.Number, secondSHA,
		"approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"W": {ID: "W", Repos: []config.RepoConfig{{Name: "repo", Path: fixture.dir}}},
	}}
	result, err := followWithStore(ctx, fixture.store, cfg, "W", "L")
	if err != nil || len(result.Pending) != 1 || fixture.git(t, "rev-parse", "HEAD") != fixture.base {
		t.Fatalf("dependent approval was added first: %+v, %v", result, err)
	}
	if _, err := review.SubmitForLead(ctx, fixture.store, "W", "C1", 1, fixture.source,
		"approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L"); err != nil {
		t.Fatal(err)
	}
	result, err = followWithStore(ctx, fixture.store, cfg, "W", "L")
	if err != nil || len(result.Applied) != 2 || result.Applied[0] != "C1" || result.Applied[1] != "C2" ||
		fixture.git(t, "rev-parse", "HEAD") != secondSHA {
		t.Fatalf("dependencies were not added in order: %+v, %v", result, err)
	}
}

// A re-approval of the same revision after Unapply re-arms the follow, so the
// lead gets the change back instead of the approval being silently dropped.
func TestReapprovalAfterUnapplyFollowsAgain(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "T1", "repo", "C1"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fixture.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"W": {ID: "W", Repos: []config.RepoConfig{{Name: "repo", Path: fixture.dir}}},
	}}
	approve := func() {
		t.Helper()
		if _, err := review.SubmitForLead(ctx, fixture.store, "W", "C1", 1, fixture.source,
			"approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L"); err != nil {
			t.Fatal(err)
		}
	}
	approve()
	if result, err := followWithStore(ctx, fixture.store, cfg, "W", "L"); err != nil || len(result.Applied) != 1 {
		t.Fatalf("first follow: %+v, %v", result, err)
	}
	fixture.git(t, "reset", "-q", "--hard", fixture.base)
	db, err := sql.Open("sqlite", fixture.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `UPDATE applied_layers SET phase='unapplied' WHERE change_id='C1'`); err != nil {
		t.Fatal(err)
	}
	approve()
	result, err := followWithStore(ctx, fixture.store, cfg, "W", "L")
	if err != nil || len(result.Applied) != 1 || fixture.git(t, "rev-parse", "HEAD") != fixture.source {
		t.Fatalf("re-approval after Unapply was not followed: %+v, %v", result, err)
	}
}

func followFixture(t *testing.T) (*fixture, *config.LoomConfig) {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.store.DriverChange(ctx, "W", "T1", "repo", "C1"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: f.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: f.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	return f, &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"W": {ID: "W", Repos: []config.RepoConfig{{Name: "repo", Path: f.dir}}},
	}}
}

func approveForLead(t *testing.T, f *fixture) int64 {
	t.Helper()
	v, err := review.SubmitForLead(context.Background(), f.store, "W", "C1", 1, f.source,
		"approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L")
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

// A follow whose request was unapplied before the follow was marked applied
// settles as spent, with a reason, instead of being retried on every pass.
func TestFollowSettlesUnappliedRequestAsSpent(t *testing.T) {
	f, cfg := followFixture(t)
	ctx := context.Background()
	verdict := approveForLead(t, f)
	if _, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: fmt.Sprintf("approval:%d", verdict)}); err != nil {
		t.Fatal(err)
	}
	f.git(t, "reset", "-q", "--hard", f.base)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `UPDATE applied_layers SET phase='unapplied' WHERE change_id='C1'`); err != nil {
		t.Fatal(err)
	}
	result, err := followWithStore(ctx, f.store, cfg, "W", "L")
	if err != nil || len(result.Applied) != 0 || len(result.Pending) != 0 || len(result.Spent) != 1 ||
		result.Spent[0].Change != "C1" || !strings.Contains(result.Spent[0].Reason, "unapplied") || f.git(t, "rev-parse", "HEAD") != f.base {
		t.Fatalf("spent request: %+v, %v", result, err)
	}
	if status, reason, err := f.store.ApprovalFollowState(ctx, "W", "L", "C1", 1); err != nil || status != "spent" || reason != result.Spent[0].Reason {
		t.Fatalf("follow state: %q %q, %v", status, reason, err)
	}
	if pending, err := f.store.PendingApprovals(ctx, "W", "L"); err != nil || len(pending) != 0 {
		t.Fatalf("spent request still pending: %+v, %v", pending, err)
	}
	approveForLead(t, f)
	result, err = followWithStore(ctx, f.store, cfg, "W", "L")
	if err != nil || len(result.Applied) != 1 || f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatalf("newer approval after a spent request: %+v, %v", result, err)
	}
	if status, reason, err := f.store.ApprovalFollowState(ctx, "W", "L", "C1", 1); err != nil || status != "applied" || reason != "" {
		t.Fatalf("re-armed follow state: %q %q, %v", status, reason, err)
	}
}

// A crash after Apply but before the follow is marked applied, then a newer
// approval: the lead already holds the revision, so no second layer is added.
func TestNewerApprovalOfHeldRevisionAddsNoSecondLayer(t *testing.T) {
	f, cfg := followFixture(t)
	ctx := context.Background()
	verdict := approveForLead(t, f)
	if _, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: fmt.Sprintf("approval:%d", verdict)}); err != nil {
		t.Fatal(err)
	}
	approveForLead(t, f)
	if _, err := followWithStore(ctx, f.store, cfg, "W", "L"); err != nil || f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatalf("follow: %v", err)
	}
	layers, err := f.store.AppliedLog(ctx, "W", "L")
	if err != nil || len(layers) != 1 {
		t.Fatalf("held revision got a second layer: %+v, %v", layers, err)
	}
	if pending, err := f.store.PendingApprovals(ctx, "W", "L"); err != nil || len(pending) != 0 {
		t.Fatalf("follow not settled: %+v, %v", pending, err)
	}
}
