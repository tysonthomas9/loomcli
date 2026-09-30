package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func (s *Service) interleaveLayers(ctx context.Context, workspace, lead, base string, commits []string, tasks []loomgit.AppliedLayer) ([]loomgit.AppliedLayer, error) {
	byCommit := make(map[string]int)
	for i, layer := range tasks {
		for _, sha := range layer.Commits {
			byCommit[sha] = i
		}
	}
	var result []loomgit.AppliedLayer
	previous, activeTask := base, -1
	var own []string
	flush := func() error {
		if len(own) == 0 {
			return nil
		}
		layer, err := s.ownLayer(ctx, workspace, lead, previous, own)
		if err != nil {
			return err
		}
		result = append(result, layer)
		previous, own = layer.NewTip, nil
		return nil
	}
	for _, sha := range commits {
		if index, ok := byCommit[sha]; ok {
			if err := flush(); err != nil {
				return nil, err
			}
			if activeTask != index {
				result = append(result, tasks[index])
				activeTask = index
			}
			previous = sha
			continue
		}
		activeTask = -1
		own = append(own, sha)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) ownLayer(ctx context.Context, workspace, lead, previous string, commits []string) (loomgit.AppliedLayer, error) {
	change := ""
	for _, sha := range commits {
		message, err := git(ctx, s.runner, "show", "-s", "--format=%B", sha)
		if err != nil {
			return loomgit.AppliedLayer{}, err
		}
		for _, line := range strings.Split(message, "\n") {
			if id, ok := strings.CutPrefix(line, "Loom-Change-Id: "); ok && strings.HasPrefix(id, "own-") && change == "" {
				change = id
			}
		}
	}
	if change == "" {
		change = "own-" + commits[0][:12]
	}
	layer := loomgit.AppliedLayer{Workspace: workspace, Lead: lead, Change: change, OldTip: previous,
		NewTip: commits[len(commits)-1], Commits: append([]string(nil), commits...), Phase: "done"}
	for _, sha := range commits {
		layer.CommitDetails = append(layer.CommitDetails, loomgit.AppliedCommit{SHA: sha, Change: change})
	}
	if err := s.recordOwnLayer(ctx, &layer); err != nil {
		return loomgit.AppliedLayer{}, err
	}
	return layer, nil
}

func (s *Service) recordOwnLayer(ctx context.Context, layer *loomgit.AppliedLayer) error {
	tree, err := git(ctx, s.runner, "rev-parse", layer.NewTip+"^{tree}")
	if err != nil {
		return err
	}
	rev, err := s.store.ReserveRevision(ctx, loomgit.Revision{Workspace: layer.Workspace, Change: layer.Change,
		RequestID: "own:" + layer.Workspace + ":" + layer.Lead + ":" + layer.NewTip, Kind: "source", Operation: "own",
		Outcome: "completed", BaseSHA: layer.OldTip, TreeHash: tree, SourceHeadSHA: layer.NewTip})
	if err != nil {
		return err
	}
	ref, err := refname.RevisionHead(layer.Workspace, layer.Change, fmt.Sprint(rev.Number))
	if err != nil {
		return err
	}
	old, readErr := git(ctx, s.runner, "rev-parse", "--verify", ref)
	if readErr == nil && old != layer.NewTip {
		return loomgit.NewError(loomgit.StaleSubject, "own revision ref moved", nil)
	}
	if readErr != nil {
		if err := s.runner.UpdateRef(ctx, ref, layer.NewTip, strings.Repeat("0", len(layer.NewTip))); err != nil {
			return err
		}
	}
	rev.HeadSHA = layer.NewTip
	if err := s.store.FinishRevision(ctx, rev); err != nil {
		return err
	}
	if err := s.store.SetRevisionAuthor(ctx, rev, "lead", layer.Lead); err != nil {
		return err
	}
	layer.Revision = rev.Number
	for i := range layer.CommitDetails {
		layer.CommitDetails[i].Revision = fmt.Sprint(rev.Number)
	}
	enabled, err := s.store.LeadMayApprovePublish(ctx, layer.Workspace)
	if err != nil || !enabled {
		return err
	}
	if v, err := s.store.LatestVerdict(ctx, rev); err == nil && v.HeadSHA == rev.HeadSHA && v.Kind == "policy" && v.Reason == "own_layer" {
		return nil
	} else if err != nil && !errors.Is(err, journal.ErrNotFound) {
		return err
	}
	_, err = review.Submit(ctx, s.store, layer.Workspace, layer.Change, rev.Number, rev.HeadSHA, "approve", "", review.Actor{Kind: "lead", ID: layer.Lead})
	return err
}
