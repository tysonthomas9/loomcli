package publish

import (
	"context"
	"strings"
	"testing"
)

// cardForge is the fixture's provider fake, under either publisher.
func cardForge(t *testing.T) *mergeForgeFake {
	t.Helper()
	switch forge, _, _ := localPublishProvider(); typed := forge.(type) {
	case *mergeForgeFake:
		return typed
	case leadNativeForge:
		return typed.mergeForgeFake
	}
	t.Fatal("unexpected provider fake")
	return nil
}

func cardStates(t *testing.T) (StackCard, string) {
	t.Helper()
	cards, err := StackCardsLocal(context.Background(), "W")
	if err != nil || len(cards) != 1 {
		t.Fatalf("cards=%+v err=%v", cards, err)
	}
	states := make([]string, len(cards[0].Layers))
	for index, layer := range cards[0].Layers {
		states[index] = layer.Change + ":" + layer.State
	}
	return cards[0], strings.Join(states, " ")
}

// The PR page's stack card lists the PRs bottom up with one state each, for
// both publishers, and follows a Merge up to here as it runs.
func TestStackCardFollowsMergeUpToHere(t *testing.T) {
	for _, backend := range []string{"loom", "native"} {
		t.Run(backend, func(t *testing.T) {
			item, _ := leadMergeFixture(t, backend)
			forge := cardForge(t)
			forge.prChecks = map[int]string{forge.prs[1].Number: "failing"}
			forge.mergeStates = map[int]string{forge.prs[3].Number: "draft"}
			card, states := cardStates(t)
			if states != "A:ready B:checks_failing C:ready D:draft" || card.Backend != backend ||
				card.Repo != "owner/repo" || card.Merge != nil || card.Layers[0].PRURL == "" {
				t.Fatalf("before: %s card=%+v", states, card)
			}
			forge.prChecks = nil
			ctx := context.Background()
			if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
				t.Fatal(err)
			}
			if _, err := QueueMergeUpToLocal(ctx, "W", "C", tyson); err != nil {
				t.Fatal(err)
			}
			card, states = cardStates(t)
			if states != "A:merged B:merging C:merging D:draft" || card.Merge == nil || card.Merge.Target != "C" {
				t.Fatalf("merging: %s card=%+v", states, card)
			}
			for _, change := range []string{"B", "C"} {
				if err := item.store.MarkLanded(ctx, "W", change, "merge_commit"); err != nil {
					t.Fatal(err)
				}
			}
			publication, _, err := item.store.Publication(ctx, "W", "D")
			if err != nil {
				t.Fatal(err)
			}
			if err := item.store.RecordPublicationDrift(ctx, publication, "0123456789abcdef0123456789abcdef01234567"); err != nil {
				t.Fatal(err)
			}
			if _, states = cardStates(t); !strings.HasPrefix(states, "A:merged B:merged C:merged D:diverged") {
				t.Fatalf("after: %s", states)
			}
		})
	}
}

func TestStackCardNeedsReviewWithoutApprovalAtHead(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	ctx := context.Background()
	publication, _, err := item.store.Publication(ctx, "W", "B")
	if err != nil {
		t.Fatal(err)
	}
	publication.Head = strings.Repeat("e", 40)
	if state := openLayerState(ctx, item.store, nil, publication); state != "needs_review" {
		t.Fatalf("state=%s", state)
	}
}

func TestStackNoteUsesPlainWords(t *testing.T) {
	for _, tc := range []struct {
		status string
		merge  QueuedMerge
		want   string
	}{
		{"", QueuedMerge{Phase: "blocked", Reason: "PR 2 required checks are unavailable"}, "Merge stopped: PR 2 required checks are unavailable"},
		{"", QueuedMerge{Phase: "restacking"}, "Updating PRs after a merge"},
		{"restack_conflict", QueuedMerge{}, "Updating PRs after a merge stopped: resolve the conflicts"},
	} {
		if got := stackNote(tc.status, tc.merge, tc.merge.Phase != ""); got != tc.want {
			t.Fatalf("note=%q want %q", got, tc.want)
		}
	}
}
