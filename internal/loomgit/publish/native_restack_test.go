package publish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// racingNativeForge changes one PR on the provider after adoption has read the stack.
type racingNativeForge struct {
	*mergeForgeFake
	reads  int
	after  int
	change func()
}

func (forge *racingNativeForge) PullByNumber(ctx context.Context, owner, repo string, number int) (stackpublish.PR, error) {
	if forge.reads == forge.after && forge.change != nil {
		forge.change()
		forge.change = nil
	}
	forge.reads++
	return forge.mergeForgeFake.PullByNumber(ctx, owner, repo, number)
}

func TestNativeRestackAdoptionRechecksPRBeforeActing(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, item fixture, forge *mergeForgeFake, trunk string)
	}{
		{name: "new head", mutate: func(t *testing.T, item fixture, forge *mergeForgeFake, _ string) {
			forge.prs[2].HeadSHA = providerCommit(t, item, forge.prs[2].HeadSHA, "C", forge.prs[2].Head)
		}},
		{name: "retargeted", mutate: func(_ *testing.T, _ fixture, forge *mergeForgeFake, trunk string) {
			forge.prs[2].Base = trunk
		}},
		{name: "closed", mutate: func(_ *testing.T, _ fixture, forge *mergeForgeFake, _ string) {
			forge.prs[2].State = "closed"
		}},
		{name: "merged", mutate: func(_ *testing.T, _ fixture, forge *mergeForgeFake, _ string) {
			forge.prs[2].Merged = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			item, forge, offer, trunk := nativeRestackFixture(t)
			ctx := context.Background()
			before := nativeAdoptionState(t, item)
			racing := &racingNativeForge{mergeForgeFake: forge, after: 2,
				change: func() { test.mutate(t, item, forge, trunk) }}
			_, err := RestackOffer(ctx, offer, racing)
			var coded *loomgit.Error
			if !errors.As(err, &coded) || coded.Code() != string(loomgit.Stale) {
				t.Fatalf("adoption after provider change = %v, want %s", err, loomgit.Stale)
			}
			if after := nativeAdoptionState(t, item); after != before {
				t.Fatalf("stale adoption wrote state:\nbefore %+v\nafter  %+v", before, after)
			}
			offers, err := item.store.OpenRestackOffers(ctx)
			if err != nil || len(offers) != 1 || offers[0].Change != "B" {
				t.Fatalf("stale adoption left offers %+v, want B still open: %v", offers, err)
			}
		})
	}
}

func TestNativeRestackAdoptionRetriesFromFreshProviderState(t *testing.T) {
	item, forge, offer, _ := nativeRestackFixture(t)
	ctx := context.Background()
	racing := &racingNativeForge{mergeForgeFake: forge, after: 2, change: func() {
		forge.prs[2].HeadSHA = providerCommit(t, item, forge.prs[2].HeadSHA, "C", forge.prs[2].Head)
	}}
	if _, err := RestackOffer(ctx, offer, racing); err == nil {
		t.Fatal("adoption accepted a PR head that moved after it was read")
	}
	derived, err := RestackOffer(ctx, offer, racing)
	if err != nil {
		t.Fatalf("retry from fresh provider state: %v", err)
	}
	if derived <= offer.Revision {
		t.Fatalf("retry derived revision %d, want after %d", derived, offer.Revision)
	}
	for index, change := range []string{"B", "C"} {
		publication, _, err := item.store.Publication(ctx, "W", change)
		if err != nil || publication.Head != forge.prs[index+1].HeadSHA {
			t.Fatalf("publication %s head = %s, want fresh provider head %s: %v", change, publication.Head, forge.prs[index+1].HeadSHA, err)
		}
	}
	if head := git(t, item.repo, "rev-parse", "HEAD"); head != forge.prs[2].HeadSHA {
		t.Fatalf("working area = %s, want fresh provider leaf %s", head, forge.prs[2].HeadSHA)
	}
}

type nativeAdoption struct {
	head, sourceB, sourceC, publishedB, publishedC, refB, refC string
}

func nativeAdoptionState(t *testing.T, item fixture) nativeAdoption {
	t.Helper()
	ctx := context.Background()
	state := nativeAdoption{head: git(t, item.repo, "rev-parse", "HEAD")}
	for _, change := range []string{"B", "C"} {
		revision, err := item.store.SourceRevision(ctx, "W", change)
		if err != nil {
			t.Fatal(err)
		}
		publication, _, err := item.store.Publication(ctx, "W", change)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := refname.Publication("W", change)
		if err != nil {
			t.Fatal(err)
		}
		values := []string{strconv.Itoa(revision), publication.Head, git(t, item.repo, "rev-parse", ref)}
		if change == "B" {
			state.sourceB, state.publishedB, state.refB = values[0], values[1], values[2]
		} else {
			state.sourceC, state.publishedC, state.refC = values[0], values[1], values[2]
		}
	}
	return state
}

// nativeRestackFixture publishes A, B and C as a native stack, lands A, and lets the
// provider restack B and C onto the new trunk, leaving an open restack offer for B.
func nativeRestackFixture(t *testing.T) (fixture, *mergeForgeFake, journal.RestackOffer, string) {
	t.Helper()
	item := newFixture(t)
	parent := item.base
	for _, change := range []string{"A", "B", "C"} {
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
	stack := StackRequest{Request: request, StackID: "feature", Changes: []string{"A", "B", "C"}}
	if _, err := (LoomStackBackend{Store: item.store}).Publish(ctx, stack); err != nil {
		t.Fatal(err)
	}
	if err := item.store.RecordStackBackend(ctx, "W", "feature", "native"); err != nil {
		t.Fatal(err)
	}
	trunk := squashMergeLayer(t, item, "A")
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	cursor := trunk
	for index, change := range []string{"B", "C"} {
		publication, _, err := item.store.Publication(ctx, "W", change)
		if err != nil {
			t.Fatal(err)
		}
		cursor = providerCommit(t, item, cursor, change, publication.Branch)
		forge.prs[index+1].HeadSHA = cursor
	}
	forge.prs[1].Base = "develop"
	configDir := t.TempDir()
	if err := os.Symlink(filepath.Dir(item.storePath), filepath.Join(configDir, "loomgit")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	revision, err := item.store.SourceRevision(ctx, "W", "B")
	if err != nil {
		t.Fatal(err)
	}
	offer := journal.RestackOffer{Workspace: "W", Change: "B", Predecessor: "A", Repo: "repo",
		Revision: revision, TrunkSHA: trunk}
	if err := item.store.OfferRestack(ctx, offer); err != nil {
		t.Fatal(err)
	}
	return item, forge, offer, "develop"
}

// providerCommit stands in for the provider rewriting a PR branch: it commits on parent
// and force-pushes the result to branch on the remote.
func providerCommit(t *testing.T, item fixture, parent, change, branch string) string {
	t.Helper()
	tree := filepath.Join(t.TempDir(), "provider")
	git(t, item.repo, "worktree", "add", "-q", "--detach", tree, parent)
	if err := os.WriteFile(filepath.Join(tree, change), []byte(change+" from provider "+parent[:7]), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, tree, "add", change)
	git(t, tree, "commit", "-qm", change+" provider restack")
	head := git(t, tree, "rev-parse", "HEAD")
	git(t, tree, "push", "-q", "--force", "origin", "HEAD:refs/heads/"+branch)
	git(t, item.repo, "worktree", "remove", "--force", tree)
	return head
}
