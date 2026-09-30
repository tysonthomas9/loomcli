package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type StackRequest struct {
	Request
	StackID string
	Changes []string
}

type stackLayer struct {
	publication journal.Publication
	revision    loomgit.Revision
	prior       string
}

func publishStack(ctx context.Context, store Store, request StackRequest) ([]loomgit.Revision, error) {
	if request.StackID == "" || len(request.Changes) == 0 {
		return nil, errors.New("stack ID and changes are required")
	}
	runner, err := gitexec.New(request.Repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return nil, err
	}
	area, err := gitexec.New(request.WorkingArea, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return nil, err
	}
	applied, err := apply.New(store, nil, area).AppliedLog(ctx, request.Workspace, request.Lead)
	if err != nil {
		return nil, err
	}
	layers, forge, err := stackLayers(ctx, store, runner, area, request, applied)
	if err != nil {
		return nil, err
	}
	pusher := mirror.NewPusher(runner)
	if err := pushStackHeads(ctx, runner, pusher, layers); err != nil {
		return nil, err
	}
	result := make([]loomgit.Revision, 0, len(layers))
	for _, layer := range layers {
		publication := layer.publication
		if err := store.BeginPublication(ctx, publication); err != nil {
			return nil, err
		}
		if err := finishPublication(ctx, store, runner, pusher, forge, publication); err != nil {
			return nil, err
		}
		result = append(result, layer.revision)
	}
	return result, nil
}

func pushStackHeads(ctx context.Context, runner *gitexec.Runner, pusher mirror.RefPusher, layers []stackLayer) error {
	remoteBytes, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
	if err != nil {
		return err
	}
	remote := strings.TrimSpace(string(remoteBytes))
	if remote == "" {
		return errors.New("origin push remote is empty")
	}
	pushes := make([]mirror.LeasedRef, 0, len(layers))
	for _, layer := range layers {
		ref := "refs/heads/" + layer.publication.Branch
		actual, err := pusher.RemoteSHA(ctx, remote, ref)
		if err != nil {
			return err
		}
		if actual != layer.prior && actual != layer.revision.HeadSHA {
			return loomgit.NewError(loomgit.Diverged, "stack layer "+layer.publication.Change+" moved outside Loom", nil)
		}
		if actual != layer.revision.HeadSHA {
			pushes = append(pushes, mirror.LeasedRef{Ref: ref, Head: layer.revision.HeadSHA, Expected: layer.prior})
		}
	}
	if err := mirror.PushAtomic(ctx, runner, remote, pushes); err != nil {
		for _, layer := range layers {
			actual, readErr := pusher.RemoteSHA(ctx, remote, "refs/heads/"+layer.publication.Branch)
			if readErr == nil && actual != layer.prior && actual != layer.revision.HeadSHA {
				return loomgit.NewError(loomgit.Diverged, "stack layer "+layer.publication.Change+" changed during publish", err)
			}
		}
		return fmt.Errorf("atomic stack push: %w", err)
	}
	return nil
}

func stackLayers(ctx context.Context, store Store, runner, area *gitexec.Runner, request StackRequest, applied []loomgit.AppliedLayer) ([]stackLayer, Forge, error) {
	byChange := make(map[string]loomgit.AppliedLayer, len(applied))
	byHead := make(map[string]string, len(applied))
	for _, layer := range applied {
		if layer.Change != "" {
			byChange[layer.Change] = layer
			byHead[layer.NewTip] = layer.Change
		}
	}
	layers := make([]stackLayer, 0, len(request.Changes))
	seen := make(map[string]bool, len(request.Changes))
	var stackForge Forge
	for _, change := range request.Changes {
		layer, found := byChange[change]
		if !found || seen[change] {
			return nil, nil, loomgit.NewError(loomgit.StackNotLinear, "missing or repeated stack layer "+change, nil)
		}
		seen[change] = true
		if len(layers) == 0 && layer.OldTip != request.BaseSHA {
			return nil, nil, loomgit.NewError(loomgit.StackNotLinear, "first layer "+change+" does not start at the working-area base", nil)
		}
		if len(layers) > 0 && layer.OldTip != layers[len(layers)-1].revision.HeadSHA {
			return nil, nil, loomgit.NewError(loomgit.StackNotLinear, "branching layers "+layers[len(layers)-1].publication.Change+" and "+change, nil)
		}
		priorPublication, found, err := store.Publication(ctx, request.Workspace, change)
		if err != nil {
			return nil, nil, err
		}
		if found && priorPublication.StackID != "" && priorPublication.StackID != request.StackID {
			return nil, nil, loomgit.NewError(loomgit.ModeMismatch, "change belongs to another published stack", nil)
		}
		prepared, selected, err := prepareStackLayer(ctx, store, runner, area, request.Request, layer, byHead)
		if err != nil {
			return nil, nil, err
		}
		stackForge = selected
		prepared.publication.StackID = request.StackID
		if len(layers) > 0 {
			prepared.publication.Trunk = layers[len(layers)-1].publication.Branch
		}
		layers = append(layers, prepared)
	}
	return layers, stackForge, nil
}

func prepareStackLayer(ctx context.Context, store Store, runner, area *gitexec.Runner, request Request, layer loomgit.AppliedLayer, byHead map[string]string) (stackLayer, Forge, error) {
	if _, err := area.Run(ctx, "merge-base", "--is-ancestor", request.BaseSHA, layer.NewTip); err != nil {
		return stackLayer{}, nil, loomgit.NewError(loomgit.StaleSubject, "stack layer is outside the working-area base", err)
	}
	if _, err := area.Run(ctx, "merge-base", "--is-ancestor", layer.NewTip, "HEAD"); err != nil {
		return stackLayer{}, nil, loomgit.NewError(loomgit.StaleSubject, "stack layer is no longer in the working area", err)
	}
	if _, err := runner.Run(ctx, "merge-base", "--is-ancestor", layer.OldTip, layer.NewTip); err != nil {
		return stackLayer{}, nil, loomgit.NewError(loomgit.StackNotLinear, "layer "+layer.Change+" has a mismatched base", err)
	}
	merges, err := runner.Run(ctx, "rev-list", "--min-parents=2", layer.OldTip+".."+layer.NewTip)
	if err != nil {
		return stackLayer{}, nil, err
	}
	if len(strings.TrimSpace(string(merges))) > 0 {
		return stackLayer{}, nil, loomgit.NewError(loomgit.StackNotLinear,
			"layer "+layer.Change+" has multiple parents: "+mergeParentNames(ctx, runner, strings.Fields(string(merges))[0], byHead), nil)
	}
	branch, err := refname.ChangeBranch(request.Workspace, layer.Change)
	if err != nil {
		return stackLayer{}, nil, err
	}
	revision, err := store.RevisionByHead(ctx, request.Workspace, layer.Change, layer.NewTip)
	if err != nil {
		return stackLayer{}, nil, err
	}
	request.Change = layer.Change
	if err := requireRevisionRef(ctx, runner, request, revision, layer.NewTip); err != nil {
		return stackLayer{}, nil, err
	}
	if err := review.RequireVerdict(ctx, store, request.Workspace, layer.Change, revision.Number, layer.NewTip, "publish", ""); err != nil {
		return stackLayer{}, nil, err
	}
	publication, forge, err := preflight(ctx, store, runner, request, branch, layer.NewTip)
	if err != nil {
		return stackLayer{}, nil, err
	}
	pubRef, err := refname.Publication(request.Workspace, layer.Change)
	if err != nil {
		return stackLayer{}, nil, err
	}
	prior, err := localSHA(ctx, runner, pubRef)
	if err != nil {
		return stackLayer{}, nil, err
	}
	return stackLayer{publication: publication, revision: revision, prior: prior}, forge, nil
}

func mergeParentNames(ctx context.Context, runner *gitexec.Runner, merge string, byHead map[string]string) string {
	out, err := runner.Run(ctx, "rev-list", "--parents", "-n", "1", merge)
	if err != nil {
		return merge
	}
	parents := strings.Fields(string(out))
	if len(parents) < 3 {
		return merge
	}
	names := make([]string, 0, len(parents)-1)
	for _, parent := range parents[1:] {
		name := byHead[parent]
		if name == "" {
			name = parent
		}
		names = append(names, name)
	}
	return strings.Join(names, " and ")
}
