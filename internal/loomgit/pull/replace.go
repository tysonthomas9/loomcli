package pull

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
)

func init() { apply.RegisterLayerReplacer(replaceForApply) }

// replaceForApply is Apply of a revision whose change already has a layer in
// the lead's working area: that layer is replaced in place and the layers
// above it are replayed, so the change keeps one layer (D29 (6)).
func replaceForApply(ctx context.Context, store apply.Store, repo *pool.LocalRepo, runner *gitexec.Runner, in apply.Request) (apply.Result, error) {
	full, ok := store.(Store)
	if !ok {
		return apply.Result{}, errors.New("layer replace needs the pull journal")
	}
	areas, err := full.WorkingAreas(ctx, in.Workspace, in.Lead)
	if err != nil {
		return apply.Result{}, err
	}
	var area *journal.WorkingArea
	for index := range areas {
		if areas[index].Path == runner.Path() {
			area = &areas[index]
		}
	}
	if area == nil {
		return apply.Result{}, loomgit.NewError(loomgit.AttentionRequired, "working area is not registered for the lead", nil)
	}
	result, err := New(full, repo, runner).Restack(ctx, RestackRequest{Workspace: in.Workspace, Lead: in.Lead,
		Repo: area.Repo, BaseSHA: area.BaseSHA, RequestID: "replace:" + in.RequestID,
		ReplaceChange: in.Change, ReplaceRevision: in.Revision})
	var coded *loomgit.Error
	if len(result.Paths) > 0 && errors.As(err, &coded) && coded.Kind == loomgit.SwapHeld {
		// The lead's own edits touch the same files: held until they move.
		err = loomgit.NewError(loomgit.ApplyPending, strings.Join(result.Paths, ", "), err)
	}
	return apply.Result{HeadSHA: result.HeadSHA, Paths: result.Paths}, err
}

// replaceLocked rebuilds request.ReplaceChange's layer from ReplaceRevision.
// A revision based on a head of its own change (a review fix-up, which already
// contains the layer) is replayed onto the layer's current tip; any other (a
// retry, which replaces the earlier attempt) onto the layer below. The layers
// above are replayed onto the result. Nothing is installed on a conflict.
func (s *Service) replaceLocked(ctx context.Context, request RestackRequest, old, base string,
	layers []loomgit.AppliedLayer, result *PullResult) error {
	index := -1
	for position, layer := range layers {
		if layer.Change == request.ReplaceChange {
			index = position
		}
	}
	if index < 0 {
		return apply.ErrNoLayer
	}
	target := layers[index]
	held, err := apply.DerivesFrom(ctx, s.store, request.Workspace, target.Change, target.Revision, request.ReplaceRevision)
	if err != nil || held {
		// The layer already holds this revision (a retried or recovered apply).
		result.HeadSHA = old
		return err
	}
	revision, err := s.store.GetRevision(ctx, request.Workspace, request.ReplaceChange, request.ReplaceRevision)
	if err != nil {
		return err
	}
	if !revision.Ready || revision.Incomplete {
		return loomgit.NewError(loomgit.CaptureIncomplete, "revision capture is incomplete", nil)
	}
	if err := s.discardOrphanReplace(ctx, request); err != nil {
		return err
	}
	onto, err := s.replaceOnto(ctx, request.Workspace, target, revision)
	if err != nil {
		return err
	}
	trial, err := replay.New(s.repo).TrialMerge(ctx, revision.BaseSHA, revision.HeadSHA, onto)
	if err != nil {
		return err
	}
	if trial.ConflictCommit != "" {
		result.Paths = trial.ConflictingPaths
		return loomgit.NewError(loomgit.Conflict, strings.Join(trial.ConflictingPaths, ", ")+
			"; revision "+fmt.Sprint(revision.Number)+" of "+target.Change+" conflicts with its layer", nil)
	}
	pullRequest := PullRequest{Workspace: request.Workspace, Lead: request.Lead, Repo: request.Repo, RequestID: request.RequestID}
	replaced := pulledLayer{source: revision, trial: trial, base: target.OldTip, original: target, operation: "apply", replaced: true,
		layer: loomgit.AppliedLayer{RequestID: request.RequestID + ":layer:replaced", Workspace: request.Workspace,
			Lead: request.Lead, Change: target.Change, OldTip: target.OldTip, NewTip: trial.HeadSHA, DroppedCommits: trial.DroppedCommits}}
	above, cursor, paths, err := s.replayPullLayers(ctx, pullRequest, trial.HeadSHA, layers[index+1:])
	result.Paths, result.HeadSHA = paths, cursor
	if err != nil {
		return err
	}
	setRestackOperations(above, "", nil)
	rebuilt := append([]pulledLayer{replaced}, above...)
	return s.installRestack(ctx, request, pullRequest, old, base, rebuilt, result)
}

// replaceOnto picks where revision's commits are replayed: onto the layer's
// tip when the revision starts from a head of its own change, else onto the
// layer below it.
func (s *Service) replaceOnto(ctx context.Context, workspace string, target loomgit.AppliedLayer, revision loomgit.Revision) (string, error) {
	if revision.BaseSHA == target.NewTip {
		return target.NewTip, nil
	}
	_, err := s.store.RevisionByHead(ctx, workspace, target.Change, revision.BaseSHA)
	if errors.Is(err, journal.ErrNotFound) {
		return target.OldTip, nil
	}
	if err != nil {
		return "", err
	}
	return target.NewTip, nil
}

// recordReplacedVerdict gives the rebuilt layer's revision the verdict of the
// revision it was rebuilt from: it holds the approved layer plus that
// revision's own changes, so it is not a patch-equivalent carry.
func (s *Service) recordReplacedVerdict(ctx context.Context, source, derived loomgit.Revision) error {
	prior, err := s.store.LatestVerdict(ctx, source)
	if errors.Is(err, journal.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch prior.Kind {
	case "approve", "override", "policy", "feedback", "carried":
	default:
		return nil
	}
	_, err = s.store.RecordVerdict(ctx, loomgit.Verdict{Workspace: derived.Workspace, Change: derived.Change,
		Number: derived.Number, HeadSHA: derived.HeadSHA, Kind: "feedback", ActorKind: "system", ActorID: "loom",
		Reason: "layer_replaced", SourceVerdictID: prior.ID})
	return err
}

// discardOrphanReplace removes the revisions a replace recorded before it was
// interrupted ahead of saving its plan, so the retry can record them again.
func (s *Service) discardOrphanReplace(ctx context.Context, request RestackRequest) error {
	if _, err := s.store.RevisionByRequest(ctx, request.RequestID+":layer:replaced:derived"); errors.Is(err, journal.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	plans, err := s.store.PendingPullPlans(ctx, request.Workspace, request.Lead)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.RequestID == request.RequestID {
			return loomgit.NewError(loomgit.AttentionRequired, "unfinished layer replace requires recovery", nil)
		}
	}
	return s.store.AbortRestack(ctx, request.RequestID, request.Workspace, request.Lead, nil)
}
