package apply

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

// LayerReplacer rebuilds the working area so that in.Change's layer holds
// in.Revision in place, replaying the layers above it. The pull package
// registers the restack that does this; apply cannot import it.
type LayerReplacer func(ctx context.Context, store Store, repo *pool.LocalRepo, runner *gitexec.Runner, in Request) (Result, error)

var layerReplacer LayerReplacer

// RegisterLayerReplacer installs the layer-replace restack. It is called once,
// from the pull package's init.
func RegisterLayerReplacer(replacer LayerReplacer) { layerReplacer = replacer }

// ErrNoLayer is what a LayerReplacer returns when, after recovering any
// unfinished restack, the change has no layer to replace; Apply then adds the
// revision as the new top layer.
var ErrNoLayer = errors.New("change has no layer in the working area")

type pullPlans interface {
	PendingPullPlans(context.Context, string, string) ([]journal.PullPlan, error)
}

// replaceExisting applies a revision of a change that already has a layer in
// the lead's working area by replacing that layer (D29 (6)), so a change never
// has two layers and its stack stays linear. An unfinished restack is handed
// to the replacer too, which recovers it before deciding.
func (s *Service) replaceExisting(ctx context.Context, in Request) (Result, bool, error) {
	if s.runner == nil || layerReplacer == nil {
		// Without the pull package linked in there is no layer replace; Apply
		// keeps its plain behavior.
		return Result{}, false, nil
	}
	pending := false
	if plans, ok := s.store.(pullPlans); ok {
		open, err := plans.PendingPullPlans(ctx, in.Workspace, in.Lead)
		if err != nil {
			return Result{}, true, err
		}
		pending = len(open) > 0
	}
	if !pending {
		layers, err := s.AppliedLog(ctx, in.Workspace, in.Lead)
		if err != nil {
			return Result{}, true, err
		}
		layer, found := lastLayer(layers, in.Change)
		if !found {
			return Result{}, false, nil
		}
		// A layer that already holds this revision is Apply's own repeat case.
		held, err := DerivesFrom(ctx, s.store, in.Workspace, in.Change, layer.Revision, in.Revision)
		if err != nil {
			return Result{}, true, err
		}
		if held {
			return Result{}, false, nil
		}
	}
	result, err := layerReplacer(ctx, s.store, s.repo, s.runner, in)
	if errors.Is(err, ErrNoLayer) {
		return Result{}, false, nil
	}
	return result, true, err
}

func lastLayer(layers []loomgit.AppliedLayer, change string) (loomgit.AppliedLayer, bool) {
	for index := len(layers) - 1; index >= 0; index-- {
		if layers[index].Change == change {
			return layers[index], true
		}
	}
	return loomgit.AppliedLayer{}, false
}

// DerivesFrom reports whether revision number of change is revision want or
// was derived from it, following derived revisions of the same change back.
func DerivesFrom(ctx context.Context, store loomgit.RevisionStore, workspace, change string, number, want int) (bool, error) {
	for hops := 0; number != want; hops++ {
		if number < want || hops > 1024 {
			return false, nil
		}
		revision, err := store.GetRevision(ctx, workspace, change, number)
		if err != nil {
			return false, err
		}
		if revision.Kind != "derived" || (revision.DerivedFromChange != "" && revision.DerivedFromChange != change) ||
			revision.DerivedFromNumber < 1 || revision.DerivedFromNumber >= number {
			return false, nil
		}
		number = revision.DerivedFromNumber
	}
	return true, nil
}
