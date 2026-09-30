package pull

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

func git(ctx context.Context, runner *gitexec.Runner, args ...string) (string, error) {
	output, err := runner.Run(ctx, args...)
	return strings.TrimSpace(string(output)), err
}

func (s *Service) appliedLog(ctx context.Context, workspace, lead, head string) ([]loomgit.AppliedLayer, error) {
	tasks, err := s.store.AppliedLog(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	areas, err := s.store.WorkingAreas(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	base := ""
	for _, area := range areas {
		if area.Path == s.runner.Path() {
			base = area.BaseSHA
			break
		}
	}
	if base == "" {
		return nil, errors.New("registered working area has no base")
	}
	raw, err := git(ctx, s.runner, "rev-list", "--first-parent", "--reverse", base+".."+head)
	if err != nil {
		return nil, err
	}
	return s.interleave(ctx, workspace, lead, base, strings.Fields(raw), tasks)
}

func (s *Service) interleave(ctx context.Context, workspace, lead, base string, commits []string, tasks []loomgit.AppliedLayer) ([]loomgit.AppliedLayer, error) {
	byCommit := make(map[string]int)
	for index, layer := range tasks {
		for _, sha := range layer.Commits {
			byCommit[sha] = index
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
	return layer, nil
}

func (s *Service) reconcilePullPlans(ctx context.Context, workspace, lead, repo string) error {
	plans, err := s.store.PendingPullPlans(ctx, workspace, lead)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.Repo != repo {
			continue
		}
		switch plan.Phase {
		case "done":
			if err := s.store.CompletePull(ctx, plan.RequestID, plan.Workspace, plan.Lead, plan.Repo, plan.BaseSHA, plan.Layers); err != nil {
				return err
			}
		case "", "not_applied":
			if err := s.store.DiscardPullPlan(ctx, plan.RequestID); err != nil {
				return err
			}
		default:
			return loomgit.NewError(loomgit.AttentionRequired, "unfinished pull swap requires recovery", nil)
		}
	}
	return nil
}
