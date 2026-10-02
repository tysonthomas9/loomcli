package publish

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type MergeLayerView struct {
	Change string `json:"change"`
	Head   string `json:"head"`
	PRURL  string `json:"pr_url"`
	State  string `json:"state"`
}

type MergeStackView struct {
	StackID string           `json:"stack_id"`
	Target  string           `json:"target"`
	Backend string           `json:"backend"`
	Phase   string           `json:"phase"`
	Reason  string           `json:"reason,omitempty"`
	Layers  []MergeLayerView `json:"layers"`
}

type confirmedHumanMerge struct {
	Request journal.MergeRequest
	Store   *journal.SQLite
}

func (authority confirmedHumanMerge) AuthorizeMerge(ctx context.Context, request StackRequest, target string) error {
	recorded, err := authority.Store.MergeRequest(ctx, request.Workspace, authority.Request.ID)
	if err != nil {
		return err
	}
	if recorded.Status != "confirmed" || recorded.ConfirmedBy == "" || recorded.StackID != request.StackID || recorded.Target != target {
		return errors.New("merge request is not confirmed by a human")
	}
	if len(recorded.Heads) == 0 || len(recorded.Heads) > len(request.Changes) {
		return errors.New("confirmation must include every head through the target")
	}
	for index, change := range recorded.Changes {
		if request.Changes[index] != change {
			return loomgit.NewError(loomgit.Stale, "confirmed stack layers changed", nil)
		}
		publication, found, err := authority.Store.Publication(ctx, request.Workspace, change)
		if err != nil {
			return err
		}
		if !found || publication.StackID != request.StackID || publication.Head == "" || publication.Head != recorded.Heads[index] {
			return loomgit.NewError(loomgit.Stale, "confirmed stack head changed", nil)
		}
	}
	return nil
}

// MergeStackLocal is a human's request and confirmation in one call: heads are
// the exact heads the human saw. Agents use RequestMergeLocal instead.
func MergeStackLocal(ctx context.Context, workspace, lead, stackID, target string, heads []string, actor MergeActor) (MergeStackView, error) {
	store, err := openLocalStore()
	if err != nil {
		return MergeStackView{}, err
	}
	defer func() { _ = store.Close() }()
	forge, _, _ := localPublishProvider()
	return mergeStackConfirmed(ctx, store, workspace, lead, stackID, target, heads, actor, forge)
}

func mergeStackConfirmed(ctx context.Context, store *journal.SQLite, workspace, lead, stackID, target string, heads []string, actor MergeActor, forge Forge) (MergeStackView, error) {
	if actor.Kind != "human" || actor.ID == "" || actor.ID == lead {
		return MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "a merge can only be confirmed by a human", nil)
	}
	request, err := requestMerge(ctx, store, workspace, lead, stackID, target, actor)
	if err != nil {
		return MergeStackView{}, err
	}
	if len(heads) < len(request.Heads) {
		return MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "confirm every stack head", nil)
	}
	for index, head := range request.Heads {
		if heads[index] != head {
			if err := store.DecideMergeRequest(ctx, workspace, request.ID, "stale", actor.ID, mergeRequestNow().UnixNano()); err != nil {
				return MergeStackView{}, err
			}
			return MergeStackView{}, loomgit.NewError(loomgit.Stale, "confirmed stack head changed", nil)
		}
	}
	return confirmMergeRequest(ctx, store, workspace, lead, request.ID, actor, forge)
}

func MergeStackPreviewLocal(ctx context.Context, workspace, lead, stackID, target string) (MergeStackView, error) {
	store, err := openLocalStore()
	if err != nil {
		return MergeStackView{}, err
	}
	defer func() { _ = store.Close() }()
	return mergeStackView(ctx, store, workspace, lead, stackID, target)
}

func mergeStackRecorded(ctx context.Context, store *journal.SQLite, confirmed journal.MergeRequest, forge Forge) (MergeStackView, error) {
	workspace, lead, stackID, target := confirmed.Workspace, confirmed.Lead, confirmed.StackID, confirmed.Target
	view, err := mergeStackView(ctx, store, workspace, lead, stackID, target)
	if err != nil {
		return view, err
	}
	if !sameMergeHeads(view, confirmed.Changes, confirmed.Heads) {
		return view, loomgit.NewError(loomgit.Stale, "confirmed stack head changed", nil)
	}
	request, err := mergeEntryRequest(ctx, store, workspace, lead, stackID, view, forge)
	if err != nil {
		return view, err
	}
	request.MergeAuthority = confirmedHumanMerge{Request: confirmed, Store: store}
	var backend StackBackend = LoomStackBackend{Store: store}
	if view.Backend == "native" {
		backend = GitHubStackBackend{Store: store}
	}
	if err := backend.MergeUpTo(ctx, request, target); err != nil {
		return view, err
	}
	return mergeStackView(ctx, store, workspace, lead, stackID, target)
}

func mergeStackView(ctx context.Context, store *journal.SQLite, workspace, lead, stackID, target string) (MergeStackView, error) {
	if workspace == "" || lead == "" || stackID == "" || target == "" {
		return MergeStackView{}, errors.New("workspace, lead, stack and target are required")
	}
	publication, found, err := store.Publication(ctx, workspace, target)
	if err != nil {
		return MergeStackView{}, err
	}
	if !found || publication.StackID != stackID {
		return MergeStackView{}, loomgit.NewError(loomgit.Stale, "merge target is not in stack", nil)
	}
	if recorded, found, err := recordedMergeView(ctx, store, workspace, stackID, target); err != nil || found {
		return recorded, err
	}
	return appliedMergeView(ctx, store, workspace, lead, stackID, target, publication)
}

func appliedMergeView(ctx context.Context, store *journal.SQLite, workspace, lead, stackID, target string,
	publication journal.Publication) (MergeStackView, error) {
	repoName, err := mergeRepoName(workspace, publication.Repo)
	if err != nil {
		return MergeStackView{}, err
	}
	area, err := workingArea(ctx, store, workspace, lead, repoName)
	if err != nil {
		return MergeStackView{}, err
	}
	runner, err := gitexec.New(area.Path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return MergeStackView{}, err
	}
	applied, err := apply.New(store, nil, runner).AppliedLog(ctx, workspace, lead)
	if err != nil {
		return MergeStackView{}, err
	}
	backend, err := store.StackBackend(ctx, workspace, stackID)
	if err != nil {
		return MergeStackView{}, err
	}
	if backend != "loom" && backend != "native" {
		return MergeStackView{}, loomgit.NewError(loomgit.ModeMismatch, "stack backend is unavailable", nil)
	}
	view := MergeStackView{StackID: stackID, Target: target, Backend: backend}
	if err := fillMergeLayers(ctx, store, workspace, stackID, target, applied, &view); err != nil {
		return view, err
	}
	if err := mergeViewProgress(ctx, store, workspace, &view); err != nil {
		return view, err
	}
	return view, nil
}

func recordedMergeView(ctx context.Context, store *journal.SQLite, workspace, stackID, target string) (MergeStackView, bool, error) {
	backend, err := store.StackBackend(ctx, workspace, stackID)
	if err != nil {
		return MergeStackView{}, false, err
	}
	view := MergeStackView{StackID: stackID, Target: target, Backend: backend}
	if backend == "loom" {
		merge, err := store.LoomMerge(ctx, workspace, stackID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && finishedForOtherTarget(merge, target)) {
			return view, false, nil
		}
		if err != nil {
			return view, false, err
		}
		for _, layer := range merge.Layers {
			publication, found, err := store.Publication(ctx, workspace, layer.Change)
			if err != nil {
				return view, true, err
			}
			if !found {
				return view, true, journal.ErrNotFound
			}
			view.Layers = append(view.Layers, MergeLayerView{Change: layer.Change, Head: layer.Head, PRURL: publication.PRURL, State: "pending"})
		}
	} else if backend == "native" {
		_, err := store.NativeMerge(ctx, workspace, stackID)
		if errors.Is(err, sql.ErrNoRows) {
			return view, false, nil
		}
		if err != nil {
			return view, false, err
		}
		publications, err := store.StackPublications(ctx, workspace, stackID)
		if err != nil {
			return view, true, err
		}
		for _, publication := range publications {
			view.Layers = append(view.Layers, MergeLayerView{Change: publication.Change, Head: publication.Head, PRURL: publication.PRURL, State: "pending"})
		}
	} else {
		return view, false, loomgit.NewError(loomgit.ModeMismatch, "stack backend is unavailable", nil)
	}
	if err := mergeViewProgress(ctx, store, workspace, &view); err != nil {
		return view, true, err
	}
	return view, true, nil
}

// finishedForOtherTarget reports a done merge of another target. It is history,
// not the current request, so a later target is read from the current stack.
func finishedForOtherTarget(merge journal.LoomMerge, target string) bool {
	return merge.Phase == "done" && merge.Target != target
}

func fillMergeLayers(ctx context.Context, store *journal.SQLite, workspace, stackID, target string, applied []loomgit.AppliedLayer, view *MergeStackView) error {
	containsTarget := false
	for _, layer := range applied {
		containsTarget = containsTarget || layer.Change == target
		entry, ok, err := store.Publication(ctx, workspace, layer.Change)
		if err != nil {
			return err
		}
		if !ok || entry.StackID != stackID || entry.Phase != "done" {
			return loomgit.NewError(loomgit.StackNotLinear, "stack publication differs from applied layers", nil)
		}
		view.Layers = append(view.Layers, MergeLayerView{Change: layer.Change, Head: entry.Head, PRURL: entry.PRURL, State: "pending"})
	}
	if !containsTarget {
		return loomgit.NewError(loomgit.Stale, "target is not in the applied stack", nil)
	}
	return nil
}

func mergeViewProgress(ctx context.Context, store *journal.SQLite, workspace string, view *MergeStackView) error {
	if view.Backend == "native" {
		return nativeMergeProgress(ctx, store, workspace, view)
	}
	merge, err := store.LoomMerge(ctx, workspace, view.StackID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && finishedForOtherTarget(merge, view.Target)) {
		return nil
	}
	if err != nil {
		return err
	}
	view.Phase, view.Reason = merge.Phase, merge.Reason
	for index := range view.Layers {
		if index < merge.Index {
			view.Layers[index].State = "done"
		}
		if index == merge.Index {
			view.Layers[index].State = merge.Phase
			if merge.Phase == "blocked" {
				for _, state := range []string{"review_required", "stale", "attention_required"} {
					if strings.Contains(merge.Reason, state) {
						view.Layers[index].State = state
						break
					}
				}
			}
		}
	}
	return nil
}

func nativeMergeProgress(ctx context.Context, store *journal.SQLite, workspace string, view *MergeStackView) error {
	merge, err := store.NativeMerge(ctx, workspace, view.StackID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	view.Phase, view.Reason = merge.Phase, merge.Reason
	for index := range view.Layers {
		if index < len(merge.Changes) {
			view.Layers[index].State = merge.Phase
		}
	}
	return nil
}

func mergeEntryRequest(ctx context.Context, store *journal.SQLite, workspace, lead, stackID string, view MergeStackView, forge Forge) (StackRequest, error) {
	publication, _, err := store.Publication(ctx, workspace, view.Target)
	if err != nil {
		return StackRequest{}, err
	}
	repoName, err := mergeRepoName(workspace, publication.Repo)
	if err != nil {
		return StackRequest{}, err
	}
	area, err := workingArea(ctx, store, workspace, lead, repoName)
	if err != nil {
		return StackRequest{}, err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return StackRequest{}, err
	}
	_, configured, found := config.WorkspaceByID(cfg, workspace)
	if !found {
		return StackRequest{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "workspace is unavailable", nil)
	}
	if forge == nil && view.Backend == "loom" {
		token := githubtoken.GitHub(ctx)
		if token == "" {
			return StackRequest{}, errors.New("GitHub host credential unavailable")
		}
		forge = stackpublish.NewConfiguredGitHubForge(token)
	}
	for _, repo := range configured.Repos {
		if repo.Name != repoName {
			continue
		}
		changes := make([]string, len(view.Layers))
		for index, layer := range view.Layers {
			changes[index] = layer.Change
		}
		return StackRequest{Request: Request{Workspace: workspace, Lead: lead, Repo: repo.ResolveAbsPath(configured.Path),
			WorkingArea: area.Path, BaseSHA: area.BaseSHA, RepoName: repoName, forge: forge, slug: publication.Slug},
			StackID: stackID, Changes: changes}, nil
	}
	return StackRequest{}, fmt.Errorf("stack repo %s is not in workspace", publication.Repo)
}

func mergeRepoName(workspace, path string) (string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return "", err
	}
	_, configured, found := config.WorkspaceByID(cfg, workspace)
	if !found {
		return "", loomgit.NewError(loomgit.WorkspaceUnsupported, "workspace is unavailable", nil)
	}
	for _, repo := range configured.Repos {
		if repo.ResolveAbsPath(configured.Path) == path {
			return repo.Name, nil
		}
	}
	return "", loomgit.NewError(loomgit.RepoSelectionRequired, "stack repository is not in workspace", nil)
}
