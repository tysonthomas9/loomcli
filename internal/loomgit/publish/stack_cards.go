package publish

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// StackCard is one stack on the Pull Requests page (D37): its PRs bottom
// first, each with one state, and the stack's unfinished merge if any. Both
// publishers (GitHub native and Loom's own) produce the same card.
type StackCard struct {
	StackID string `json:"stack_id"`
	// Repo is the provider's owner/name.
	Repo    string `json:"repo"`
	Backend string `json:"backend"`
	// Note is a plain-words stack blocker or progress line, if any.
	Note   string           `json:"note,omitempty"`
	Merge  *QueuedMerge     `json:"merge,omitempty"`
	Layers []StackCardLayer `json:"layers"`
}

// StackCardLayer is one PR of a stack. State is one of draft, needs_review,
// approved, checks_failing, ready, merging, merged or diverged.
type StackCardLayer struct {
	Change   string `json:"change"`
	PRNumber int    `json:"pr_number"`
	PRURL    string `json:"pr_url"`
	State    string `json:"state"`
}

// StackCardsLocal lists the workspace's published stacks for the PR page.
func StackCardsLocal(ctx context.Context, workspace string) ([]StackCard, error) {
	store, err := openLocalStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	forge, _, _ := localPublishProvider()
	statusForge, _ := forge.(prStatusForge)
	return stackCards(ctx, store, statusForge, workspace)
}

type prStatusForge interface {
	PRStatuses(context.Context, string, string, string) (map[string]stackpublish.PRStatus, error)
}

func stackCards(ctx context.Context, store *journal.SQLite, forge prStatusForge, workspace string) ([]StackCard, error) {
	publications, err := store.PublishedStacks(ctx, workspace)
	if err != nil {
		return nil, err
	}
	cards := []StackCard{}
	for index, publication := range publications {
		if index > 0 && publications[index-1].StackID == publication.StackID {
			continue
		}
		card, err := stackCard(ctx, store, forge, workspace, publication.StackID)
		if err != nil {
			return nil, err
		}
		if len(card.Layers) > 0 {
			cards = append(cards, card)
		}
	}
	return cards, nil
}

func stackCard(ctx context.Context, store *journal.SQLite, forge prStatusForge, workspace, stackID string) (StackCard, error) {
	card := StackCard{StackID: stackID, Layers: []StackCardLayer{}}
	state, err := store.StackState(ctx, workspace, stackID)
	if err != nil {
		return card, err
	}
	card.Backend = state.Backend
	entry, merging, err := stackMergeEntry(ctx, store, workspace, stackID)
	if err != nil {
		return card, err
	}
	if merging {
		card.Merge = &entry
	}
	card.Note = stackNote(state.Status, entry, merging)
	publications, err := store.StackPublications(ctx, workspace, stackID)
	if err != nil {
		return card, err
	}
	inMerge := merging && entry.Phase != "blocked"
	for _, publication := range publications {
		if publication.PRNumber == 0 || (publication.Phase != "done" && publication.Phase != "drift") {
			continue
		}
		card.Repo = publication.Slug
		layer := StackCardLayer{Change: publication.Change, PRNumber: publication.PRNumber, PRURL: publication.PRURL}
		landed, err := store.IsLanded(ctx, workspace, publication.Change)
		if err != nil {
			return card, err
		}
		switch {
		case landed:
			layer.State = "merged"
		case inMerge:
			layer.State = "merging"
		default:
			layer.State = openLayerState(ctx, store, forge, publication)
		}
		inMerge = inMerge && publication.Change != entry.Target
		card.Layers = append(card.Layers, layer)
	}
	return card, nil
}

// openLayerState is an open PR's one state: Loom's code approval at the PR's
// head first, then what the provider says about the PR.
func openLayerState(ctx context.Context, store *journal.SQLite, forge prStatusForge, publication journal.Publication) string {
	if publication.Phase == "drift" {
		return "diverged"
	}
	status, found := providerStatus(ctx, forge, publication)
	if found && status.MergeState == "draft" {
		return "draft"
	}
	if !approvedAtHead(ctx, store, publication) {
		return "needs_review"
	}
	if !found {
		return "approved"
	}
	if status.Checks == "failing" {
		return "checks_failing"
	}
	if (status.Checks == "passing" || status.Checks == "none") && status.Mergeable != "conflicting" &&
		status.Review != "changes_requested" {
		return "ready"
	}
	return "approved"
}

func approvedAtHead(ctx context.Context, store *journal.SQLite, publication journal.Publication) bool {
	revision, err := store.RevisionByHead(ctx, publication.Workspace, publication.Change, publication.Head)
	if err != nil {
		return false
	}
	return review.RequireVerdict(ctx, store, publication.Workspace, publication.Change, revision.Number,
		revision.HeadSHA, "publish", "") == nil
}

// providerStatus reads the PR's checks and review from the provider. A
// provider that is unavailable leaves the state to Loom's own records.
func providerStatus(ctx context.Context, forge prStatusForge, publication journal.Publication) (stackpublish.PRStatus, bool) {
	parts := strings.Split(publication.Slug, "/")
	if forge == nil || len(parts) != 2 {
		return stackpublish.PRStatus{}, false
	}
	statuses, err := forge.PRStatuses(ctx, parts[0], parts[1], publication.Branch)
	if err != nil {
		return stackpublish.PRStatus{}, false
	}
	status, found := statuses[publication.Branch]
	return status, found && status.Number == publication.PRNumber
}

// stackNote says in plain words what the stack is waiting on (S11): no stack
// IDs, layer numbers or merge phases.
func stackNote(status string, merge QueuedMerge, merging bool) string {
	switch {
	case merging && merge.Phase == "blocked":
		return "Merge stopped: " + merge.Reason
	case merging && merge.Phase == "restacking":
		return "Updating PRs after a merge"
	case status == "review_required":
		return "A change was updated after a merge and needs review"
	case status == "restack_conflict":
		return "Updating PRs after a merge stopped: resolve the conflicts"
	case status == "swap_held":
		return "Updating PRs after a merge is waiting: save or move the working-area edits"
	}
	return ""
}
