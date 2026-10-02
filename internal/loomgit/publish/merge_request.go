package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// MergeRequestTTL is how long a merge request waits for a human confirmation.
const MergeRequestTTL = 30 * time.Minute

var mergeRequestNow = time.Now

// MergeActor is the identity a caller reports. In local mode it is trusted as
// reported (D28), so the human-only check is advisory.
type MergeActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type MergeRequestLayer struct {
	Change string `json:"change"`
	Head   string `json:"head"`
	PRURL  string `json:"pr_url"`
	Checks string `json:"checks"`
	Review string `json:"review"`
}

type MergeRequestView struct {
	ID            string              `json:"id"`
	StackID       string              `json:"stack_id"`
	Target        string              `json:"target"`
	Lead          string              `json:"lead"`
	Status        string              `json:"status"`
	RequestedKind string              `json:"requested_kind"`
	RequestedBy   string              `json:"requested_by"`
	ConfirmedBy   string              `json:"confirmed_by,omitempty"`
	ExpiresAt     time.Time           `json:"expires_at"`
	Audit         string              `json:"audit,omitempty"`
	Layers        []MergeRequestLayer `json:"layers"`
}

type prStatusForge interface {
	PRStatuses(context.Context, string, string, string) (map[string]stackpublish.PRStatus, error)
}

// RequestMergeLocal records a merge request pinned to the stack's current heads.
// It never merges; a human must confirm it with ConfirmMergeRequestLocal.
func RequestMergeLocal(ctx context.Context, workspace, lead, stackID, target string, requester MergeActor) (MergeRequestView, error) {
	store, err := openLocalStore()
	if err != nil {
		return MergeRequestView{}, err
	}
	defer func() { _ = store.Close() }()
	request, err := requestMerge(ctx, store, workspace, lead, stackID, target, requester)
	if err != nil {
		return MergeRequestView{}, err
	}
	forge, _, _ := localPublishProvider()
	return mergeRequestView(ctx, store, request, forge), nil
}

func MergeRequestsLocal(ctx context.Context, workspace, lead string) ([]MergeRequestView, error) {
	store, err := openLocalStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	requests, err := store.MergeRequests(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	forge, _, _ := localPublishProvider()
	views := make([]MergeRequestView, 0, len(requests))
	for _, request := range requests {
		request, err = expireMergeRequest(ctx, store, request)
		if err != nil {
			return nil, err
		}
		views = append(views, mergeRequestView(ctx, store, request, forge))
	}
	return views, nil
}

func ConfirmMergeRequestLocal(ctx context.Context, workspace, lead, id string, confirmer MergeActor) (MergeStackView, error) {
	store, err := openLocalStore()
	if err != nil {
		return MergeStackView{}, err
	}
	defer func() { _ = store.Close() }()
	forge, _, _ := localPublishProvider()
	return confirmMergeRequest(ctx, store, workspace, lead, id, confirmer, forge)
}

func requestMerge(ctx context.Context, store *journal.SQLite, workspace, lead, stackID, target string, requester MergeActor) (journal.MergeRequest, error) {
	if requester.ID == "" || (requester.Kind != "human" && (requester.Kind != "lead" || requester.ID != lead)) {
		return journal.MergeRequest{}, loomgit.NewError(loomgit.MergeNotAuthorized, "only the lead or a human may request a merge", nil)
	}
	view, err := mergeStackView(ctx, store, workspace, lead, stackID, target)
	if err != nil {
		return journal.MergeRequest{}, err
	}
	now := mergeRequestNow()
	request := journal.MergeRequest{Workspace: workspace, ID: uuid.NewString(), Lead: lead, StackID: stackID,
		Target: target, RequestedKind: requester.Kind, RequestedBy: requester.ID, Status: "pending",
		CreatedAt: now.UnixNano(), ExpiresAt: now.Add(MergeRequestTTL).UnixNano()}
	for _, layer := range view.Layers {
		request.Changes = append(request.Changes, layer.Change)
		request.Heads = append(request.Heads, layer.Head)
		if layer.Change == target {
			break
		}
	}
	return request, store.CreateMergeRequest(ctx, request)
}

func confirmMergeRequest(ctx context.Context, store *journal.SQLite, workspace, lead, id string, confirmer MergeActor, forge Forge) (MergeStackView, error) {
	request, err := store.MergeRequest(ctx, workspace, id)
	if errors.Is(err, journal.ErrNotFound) || (err == nil && request.Lead != lead) {
		return MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "merge request not found", err)
	}
	if err != nil {
		return MergeStackView{}, err
	}
	if confirmer.Kind != "human" || confirmer.ID == "" || confirmer.ID == request.Lead ||
		(request.RequestedKind != "human" && confirmer.ID == request.RequestedBy) {
		return MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "a merge request can only be confirmed by a human", nil)
	}
	if request.Status == "pending" {
		if request, err = decidePendingMergeRequest(ctx, store, request, confirmer, forge); err != nil {
			return MergeStackView{}, err
		}
	}
	switch request.Status {
	case "confirmed":
		return mergeStackRecorded(ctx, store, request, forge)
	case "stale":
		return MergeStackView{}, loomgit.NewError(loomgit.Stale, "stack head changed after the merge request; request the merge again", nil)
	default:
		return MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "merge request expired; request the merge again", nil)
	}
}

func decidePendingMergeRequest(ctx context.Context, store *journal.SQLite, request journal.MergeRequest, confirmer MergeActor, forge Forge) (journal.MergeRequest, error) {
	request, err := expireMergeRequest(ctx, store, request)
	if err != nil || request.Status != "pending" {
		return request, err
	}
	status := "confirmed"
	view, err := mergeStackView(ctx, store, request.Workspace, request.Lead, request.StackID, request.Target)
	if err != nil && !isStaleError(err) {
		return request, err
	}
	if err != nil || !sameMergeHeads(view, request.Changes, request.Heads) {
		status = "stale"
	} else if moved, err := providerHeadsMoved(ctx, store, request, forge); err != nil {
		return request, err
	} else if moved {
		status = "stale"
	}
	err = store.DecideMergeRequest(ctx, request.Workspace, request.ID, status, confirmer.ID, mergeRequestNow().UnixNano())
	if err != nil && !errors.Is(err, journal.ErrStale) {
		return request, err
	}
	return store.MergeRequest(ctx, request.Workspace, request.ID)
}

func expireMergeRequest(ctx context.Context, store *journal.SQLite, request journal.MergeRequest) (journal.MergeRequest, error) {
	now := mergeRequestNow().UnixNano()
	if request.Status != "pending" || now < request.ExpiresAt {
		return request, nil
	}
	if err := store.DecideMergeRequest(ctx, request.Workspace, request.ID, "expired", "", now); err != nil && !errors.Is(err, journal.ErrStale) {
		return request, err
	}
	return store.MergeRequest(ctx, request.Workspace, request.ID)
}

func isStaleError(err error) bool {
	var coded *loomgit.Error
	return errors.As(err, &coded) && (coded.Kind == loomgit.Stale || coded.Kind == loomgit.StackNotLinear)
}

// sameMergeHeads reports whether the view still starts with the pinned layers.
func sameMergeHeads(view MergeStackView, changes, heads []string) bool {
	if len(view.Layers) < len(heads) || len(changes) != len(heads) {
		return false
	}
	for index, head := range heads {
		if view.Layers[index].Change != changes[index] || view.Layers[index].Head != head {
			return false
		}
	}
	return true
}

// providerHeadsMoved reports whether any pinned layer's open PR on the provider
// no longer points at the pinned SHA; a missing open PR counts as moved.
func providerHeadsMoved(ctx context.Context, store *journal.SQLite, request journal.MergeRequest, forge Forge) (bool, error) {
	forge = mergeRequestForge(ctx, forge)
	if forge == nil {
		return false, errors.New("GitHub host credential unavailable")
	}
	for index, change := range request.Changes {
		publication, found, err := store.Publication(ctx, request.Workspace, change)
		if err != nil || !found {
			return true, err
		}
		owner, repo, _ := strings.Cut(publication.Slug, "/")
		prs, err := forge.ListStackPRs(ctx, owner, repo, publication.Branch)
		if err != nil {
			return false, err
		}
		open := false
		for _, pr := range prs {
			if pr.Head == publication.Branch && pr.State == "open" && !pr.Merged {
				open = true
				if pr.HeadSHA != request.Heads[index] {
					return true, nil
				}
			}
		}
		if !open {
			return true, nil
		}
	}
	return false, nil
}

func mergeRequestForge(ctx context.Context, forge Forge) Forge {
	if forge != nil {
		return forge
	}
	if token := githubtoken.GitHub(ctx); token != "" {
		return stackpublish.NewConfiguredGitHubForge(token)
	}
	return nil
}

func mergeRequestView(ctx context.Context, store *journal.SQLite, request journal.MergeRequest, forge Forge) MergeRequestView {
	view := MergeRequestView{ID: request.ID, StackID: request.StackID, Target: request.Target, Lead: request.Lead,
		Status: request.Status, RequestedKind: request.RequestedKind, RequestedBy: request.RequestedBy,
		ConfirmedBy: request.ConfirmedBy, ExpiresAt: time.Unix(0, request.ExpiresAt).UTC()}
	statuses := map[string]stackpublish.PRStatus{}
	pinned := make([]string, len(request.Changes))
	for index, change := range request.Changes {
		layer := MergeRequestLayer{Change: change, Head: request.Heads[index], Checks: "unknown", Review: "unknown"}
		if publication, found, err := store.Publication(ctx, request.Workspace, change); err == nil && found {
			if index == 0 && request.Status == "pending" {
				statuses = mergeRequestStatuses(ctx, forge, publication.Slug)
			}
			layer.PRURL = publication.PRURL
			if status, ok := statuses[publication.Branch]; ok {
				layer.Checks, layer.Review = status.Checks, status.Review
			}
		}
		view.Layers = append(view.Layers, layer)
		pinned[index] = change + "@" + layer.Head
	}
	if request.Status == "confirmed" {
		view.Audit = fmt.Sprintf("requested by %s %s, confirmed by %s: %s", request.RequestedKind, request.RequestedBy,
			request.ConfirmedBy, strings.Join(pinned, " "))
	}
	return view
}

// mergeRequestStatuses reads check and review status for the card; it is display only.
func mergeRequestStatuses(ctx context.Context, forge Forge, slug string) map[string]stackpublish.PRStatus {
	owner, repo, _ := strings.Cut(slug, "/")
	reader, ok := mergeRequestForge(ctx, forge).(prStatusForge)
	if !ok || repo == "" {
		return nil
	}
	statuses, err := reader.PRStatuses(ctx, owner, repo, "")
	if err != nil {
		return nil
	}
	return statuses
}
