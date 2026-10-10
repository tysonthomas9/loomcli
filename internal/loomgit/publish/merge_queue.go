package publish

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// The merge queue is the one way a stack merges up to a chosen PR (D38). The
// PR page's "Merge up to here" button and the lead's `loom merge <task>` both
// call QueueMergeUpTo. It starts the stack's merge machine (Loom's own or the
// provider's native stack merge), and Reconcile then lands the queued PR and
// the approved PRs below it, bottom up.

// MergeActor is who asks for a merge. In local mode it is trusted as reported
// (D28): the CLI resolves it from the agent environment (D42), the UI reports
// the browser's human.
type MergeActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// humanMergeAuthority marks a merge a human queued with Merge up to here.
const humanMergeAuthority = "human_merge"

// LeadMayMergeOff is the refusal a lead gets when it queues a merge while the
// human has Lead may merge off. Nothing is queued.
const LeadMayMergeOff = "Lead may merge is off"

var mergeQueueNow = time.Now

// queuedMerge authorizes a merge queued through QueueMergeUpTo: a human's at
// any time, the lead's only while Lead may merge is on. A lead's merge records
// the lead-merge authority, so it waits for green and stops if the human turns
// the setting off before it is sent.
type queuedMerge struct {
	Store     *journal.SQLite
	Actor     MergeActor
	SetBy     string
	RequestID string
}

func (authority queuedMerge) AuthorizeMerge(ctx context.Context, request StackRequest, _ string) error {
	if authority.Actor.Kind == "human" {
		return nil
	}
	return requireLeadMayMerge(ctx, authority.Store, request.Workspace)
}

// recorded returns the authority and setter the merge machine records.
func (authority queuedMerge) recorded() (string, string) {
	if authority.Actor.Kind == "lead" {
		return leadMergeAuthority, authority.SetBy
	}
	return humanMergeAuthority, authority.Actor.ID
}

func requireLeadMayMerge(ctx context.Context, store *journal.SQLite, workspace string) error {
	policy, err := store.LeadMayMerge(ctx, workspace)
	if err != nil {
		return err
	}
	if policy.Value != "when_green" {
		return loomgit.NewError(loomgit.MergeNotAuthorized, LeadMayMergeOff, nil)
	}
	return nil
}

// QueueMergeUpToLocal queues a merge up to change in the local journal.
func QueueMergeUpToLocal(ctx context.Context, workspace, change string, actor MergeActor) (MergeStackView, error) {
	store, err := openLocalStore()
	if err != nil {
		return MergeStackView{}, err
	}
	defer func() { _ = store.Close() }()
	forge, _, _ := localPublishProvider()
	return QueueMergeUpTo(ctx, store, forge, workspace, change, actor)
}

// QueueMergeUpTo queues "merge up to change": change's PR and every PR below it
// in its stack, bottom up. actor is who asked: a human (the button) or the
// lead (only while Lead may merge is on; otherwise it is refused with
// LeadMayMergeOff and nothing is queued). Asking again for the target already
// queued returns its progress; another target is refused until the running
// merge finishes. Every layer still needs its approval at its current head.
func QueueMergeUpTo(ctx context.Context, store *journal.SQLite, forge Forge, workspace, change string,
	actor MergeActor) (MergeStackView, error) {
	if workspace == "" || change == "" {
		return MergeStackView{}, errors.New("workspace and task are required")
	}
	if actor.ID == "" || (actor.Kind != "human" && actor.Kind != "lead") {
		return MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "only a human or the lead can merge a stack", nil)
	}
	authority := queuedMerge{Store: store, Actor: actor,
		RequestID: fmt.Sprintf("queue-merge:%s:%s:%d", workspace, change, mergeQueueNow().UnixNano())}
	if actor.Kind == "lead" {
		if err := requireLeadMayMerge(ctx, store, workspace); err != nil {
			return MergeStackView{}, err
		}
		policy, err := store.LeadMayMerge(ctx, workspace)
		if err != nil {
			return MergeStackView{}, err
		}
		authority.SetBy = policy.SetBy
	}
	publication, found, err := store.Publication(ctx, workspace, change)
	if err != nil {
		return MergeStackView{}, err
	}
	if !found || publication.StackID == "" || publication.PRNumber == 0 {
		return MergeStackView{}, loomgit.NewError(loomgit.Stale, "task "+change+" has no open stacked PR", nil)
	}
	stackID := publication.StackID
	lead, err := store.StackLead(ctx, workspace, stackID)
	if err != nil {
		return MergeStackView{}, err
	}
	if lead == "" {
		return MergeStackView{}, loomgit.NewError(loomgit.Stale, "stack "+stackID+" is not applied in a lead working area", nil)
	}
	if target, active, err := activeStackMerge(ctx, store, workspace, stackID); err != nil {
		return MergeStackView{}, err
	} else if active && target == change {
		return mergeStackView(ctx, store, workspace, lead, stackID, change)
	} else if active {
		return MergeStackView{}, loomgit.NewError(loomgit.MergeBlocked,
			"a merge up to "+target+" is already running for this stack", nil)
	}
	view, err := appliedMergeView(ctx, store, workspace, lead, stackID, change, publication)
	if err != nil {
		return view, err
	}
	request, err := mergeEntryRequest(ctx, store, workspace, lead, stackID, view, forge)
	if err != nil {
		return view, err
	}
	request.MergeAuthority = authority
	if view.Backend == "native" {
		// The provider merges the unmerged prefix up to the target.
		if request.Changes, err = unlandedChanges(ctx, store, workspace, request.Changes); err != nil {
			return view, err
		}
		err = GitHubStackBackend{Store: store}.MergeUpTo(ctx, request, change)
		if target, active, activeErr := activeStackMerge(ctx, store, workspace, stackID); err != nil &&
			activeErr == nil && active && target == change {
			// Queued; the provider could not be asked yet and Reconcile retries.
			err = nil
		}
	} else {
		err = LoomStackBackend{Store: store}.MergeUpTo(ctx, request, change)
	}
	if err != nil {
		return view, err
	}
	return mergeStackView(ctx, store, workspace, lead, stackID, change)
}

// activeStackMerge reports the target of the stack's running merge, if any.
func activeStackMerge(ctx context.Context, store *journal.SQLite, workspace, stackID string) (string, bool, error) {
	backend, err := store.StackBackend(ctx, workspace, stackID)
	if err != nil {
		return "", false, err
	}
	if backend == "native" {
		merge, err := store.NativeMerge(ctx, workspace, stackID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return merge.Target, err == nil && merge.Phase != "done" && merge.Phase != "blocked", err
	}
	merge, err := store.LoomMerge(ctx, workspace, stackID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return merge.Target, err == nil && merge.Phase != "done" && merge.Phase != "blocked", err
}

// QueuedMerge is a stack's queued, running or blocked merge, as the PR page
// shows it. A finished merge is history and is not listed.
type QueuedMerge struct {
	StackID  string `json:"stack_id"`
	Target   string `json:"target"`
	PRNumber int    `json:"pr_number,omitempty"`
	PRURL    string `json:"pr_url,omitempty"`
	Backend  string `json:"backend"`
	Phase    string `json:"phase"`
	Reason   string `json:"reason,omitempty"`
	// QueuedBy is "lead" for the lead, otherwise the human who asked.
	QueuedBy string `json:"queued_by,omitempty"`
}

// MergeQueueLocal lists the workspace's queued, running and blocked stack merges.
func MergeQueueLocal(ctx context.Context, workspace string) ([]QueuedMerge, error) {
	store, err := openLocalStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return mergeQueue(ctx, store, workspace)
}

func mergeQueue(ctx context.Context, store *journal.SQLite, workspace string) ([]QueuedMerge, error) {
	publications, err := store.PublishedStacks(ctx, workspace)
	if err != nil {
		return nil, err
	}
	queue := []QueuedMerge{}
	for index, publication := range publications {
		if index > 0 && publications[index-1].StackID == publication.StackID {
			continue
		}
		entry, found, err := stackMergeEntry(ctx, store, workspace, publication.StackID)
		if err != nil {
			return nil, err
		}
		if found {
			queue = append(queue, entry)
		}
	}
	return queue, nil
}

func stackMergeEntry(ctx context.Context, store *journal.SQLite, workspace, stackID string) (QueuedMerge, bool, error) {
	backend, err := store.StackBackend(ctx, workspace, stackID)
	if errors.Is(err, sql.ErrNoRows) {
		return QueuedMerge{}, false, nil
	}
	if err != nil {
		return QueuedMerge{}, false, err
	}
	entry := QueuedMerge{StackID: stackID, Backend: backend}
	var authority, setBy string
	if backend == "native" {
		merge, err := store.NativeMerge(ctx, workspace, stackID)
		if errors.Is(err, sql.ErrNoRows) {
			return entry, false, nil
		}
		if err != nil {
			return entry, false, err
		}
		entry.Target, entry.Phase, entry.Reason, authority, setBy = merge.Target, merge.Phase, merge.Reason, merge.Authority, merge.SetBy
	} else {
		merge, err := store.LoomMerge(ctx, workspace, stackID)
		if errors.Is(err, sql.ErrNoRows) {
			return entry, false, nil
		}
		if err != nil {
			return entry, false, err
		}
		entry.Target, entry.Phase, entry.Reason, authority, setBy = merge.Target, merge.Phase, merge.Reason, merge.Authority, merge.PolicySetBy
	}
	if entry.Phase == "done" {
		return entry, false, nil
	}
	entry.QueuedBy = setBy
	if authority == leadMergeAuthority {
		entry.QueuedBy = "lead"
	}
	if publication, found, err := store.Publication(ctx, workspace, entry.Target); err != nil {
		return entry, false, err
	} else if found {
		entry.PRNumber, entry.PRURL = publication.PRNumber, publication.PRURL
	}
	return entry, true, nil
}
