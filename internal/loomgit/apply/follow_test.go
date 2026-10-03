package apply

import (
	"context"
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
