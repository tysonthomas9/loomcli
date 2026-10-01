package publish

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func fourLayerMergeEntryFixture(t *testing.T, backend string) (fixture, *mergeForgeFake, []string) {
	t.Helper()
	item := newFixture(t)
	parent := item.base
	changes := []string{"A", "B", "C", "D"}
	for _, change := range changes {
		parent = stackRevision(t, item, change, 1, parent).HeadSHA
	}
	git(t, item.repo, "branch", "-m", "loom/ws/W/interactive/L")
	ctx := context.Background()
	if err := item.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: item.repo, Branch: "loom/ws/W/interactive/L", BaseSHA: item.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	forge := &mergeForgeFake{fakeForge: &fakeForge{}, checks: "passing"}
	request := item.request()
	request.forge = forge
	request.RepoName = "repo"
	if _, err := (LoomStackBackend{Store: item.store}).Publish(ctx, StackRequest{Request: request, StackID: "feature", Changes: changes}); err != nil {
		t.Fatal(err)
	}
	if err := item.store.RecordStackBackend(ctx, "W", "feature", backend); err != nil {
		t.Fatal(err)
	}
	heads := make([]string, len(changes))
	for index, change := range changes {
		publication, _, err := item.store.Publication(ctx, "W", change)
		if err != nil {
			t.Fatal(err)
		}
		heads[index], forge.prs[index].HeadSHA = publication.Head, publication.Head
	}
	configureLocalWorkspace(t, item)
	return item, forge, heads
}

func TestFourLayerMergeEntryUsesRecordedBackendAndExactHeads(t *testing.T) {
	for _, backend := range []string{"loom", "native"} {
		t.Run(backend, func(t *testing.T) {
			item, forge, heads := fourLayerMergeEntryFixture(t, backend)
			ctx := context.Background()
			var mergeForge Forge = forge
			if backend == "native" {
				mergeForge = &fakeMergeForge{fakeForge: forge.fakeForge, prs: forge.prs}
			}
			oldProvider := localPublishProvider
			localPublishProvider = func() (Forge, string, string) { return mergeForge, "fixture-token", "owner/repo" }
			t.Cleanup(func() { localPublishProvider = oldProvider })
			view, err := MergeStackPreviewLocal(ctx, "W", "L", "feature", "C")
			if err != nil || len(view.Layers) != 4 || view.Backend != backend {
				t.Fatalf("preview=%+v err=%v", view, err)
			}
			wrong := append([]string(nil), heads...)
			wrong[2] = "changed"
			_, err = MergeStackLocal(ctx, "W", "L", "feature", "C", wrong)
			var coded *loomgit.Error
			if !errors.As(err, &coded) || coded.Kind != loomgit.Stale {
				t.Fatalf("moved head: %v", err)
			}
			view, err = MergeStackLocal(ctx, "W", "L", "feature", "C", heads)
			if err != nil || (view.Phase != "ready" && view.Phase != "sent") {
				t.Fatalf("request=%+v err=%v", view, err)
			}
			assertBlockedMergeEntry(t, item, backend, heads)
		})
	}
}

func assertBlockedMergeEntry(t *testing.T, item fixture, backend string, heads []string) {
	t.Helper()
	ctx := context.Background()
	if backend == "native" {
		merge, err := item.store.NativeMerge(ctx, "W", "feature")
		if err != nil || merge.Target != "C" || len(merge.Changes) != 3 {
			t.Fatalf("native intent=%+v err=%v", merge, err)
		}
		if err := item.store.BlockNativeMerge(ctx, merge, "provider rejected"); err != nil {
			t.Fatal(err)
		}
	} else {
		merge, err := item.store.LoomMerge(ctx, "W", "feature")
		if err != nil || merge.Target != "C" || len(merge.Layers) != 4 {
			t.Fatalf("loom intent=%+v err=%v", merge, err)
		}
		if err := setLoomPhase(ctx, item.store, merge, "blocked", 0, "review_required: new verdict needed"); err != nil {
			t.Fatal(err)
		}
	}
	view, err := MergeStackPreviewLocal(ctx, "W", "L", "feature", "C")
	if err != nil || view.Phase != "blocked" || view.Reason == "" {
		t.Fatalf("blocked view=%+v err=%v", view, err)
	}
	if backend == "loom" && view.Layers[0].State != "review_required" {
		t.Fatalf("review state=%+v", view.Layers[0])
	}
	view, err = MergeStackLocal(ctx, "W", "L", "feature", "C", heads)
	if err != nil || view.Phase != "blocked" {
		t.Fatalf("replayed blocked request=%+v err=%v", view, err)
	}
	if _, err := item.store.NativeMerge(ctx, "W", "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing cursor: %v", err)
	}
	if err := os.Rename(item.repo, item.repo+"-away"); err != nil {
		t.Fatal(err)
	}
	view, err = MergeStackPreviewLocal(ctx, "W", "L", "feature", "C")
	if err != nil || view.Phase != "blocked" || len(view.Layers) != 4 {
		t.Fatalf("durable view=%+v err=%v", view, err)
	}
}
