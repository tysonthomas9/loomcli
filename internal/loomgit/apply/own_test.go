package apply

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestAppliedLogNeedsRunnerForWorkingArea(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: f.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: f.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	_, err := New(f.store, nil, nil).AppliedLog(ctx, "W", "L")
	if err == nil || !strings.Contains(err.Error(), "working-area Git runner") {
		t.Fatalf("missing runner error = %v", err)
	}
}

func TestAppliedLogInterleavesOwnAndTaskLayers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: f.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: f.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	first := f.commit(t, "own1", "one", "first\n\nLoom-Change-Id: own-O1\nLoom-Agent: L")
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	second := f.commit(t, "own2", "two", "second\n\nLoom-Change-Id: own-O2\nLoom-Agent: L")
	plain := f.commit(t, "plain", "three", "plain terminal commit")
	log, err := f.service.AppliedLog(ctx, "W", "L")
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 3 || log[0].Change != "own-O1" || log[0].Commits[0] != first ||
		log[1].Change != "C1" || log[2].Change != "own-O2" || len(log[2].Commits) != 2 || log[2].Commits[0] != second || log[2].Commits[1] != plain {
		t.Fatalf("applied log = %+v", log)
	}
	for _, layer := range []int{0, 2} {
		rev, err := f.store.GetRevision(ctx, "W", log[layer].Change, log[layer].Revision)
		if err != nil || rev.HeadSHA != log[layer].NewTip {
			t.Fatalf("own revision = %+v, %v", rev, err)
		}
		verdict, err := f.store.LatestVerdict(ctx, rev)
		if err != nil || verdict.Kind != "policy" || verdict.Reason != "own_layer" {
			t.Fatalf("own verdict = %+v, %v", verdict, err)
		}
	}
}
