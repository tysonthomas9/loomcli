package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type PullRequest struct {
	Workspace, Lead, Repo, Remote, SourceBranch, RequestID string
}

type PullResult struct {
	HeadSHA         string
	Paths           []string
	AlreadyUpToDate bool
}

type pulledLayer struct {
	source   loomgit.Revision
	trial    replay.Result
	base     string
	layer    loomgit.AppliedLayer
	original loomgit.AppliedLayer
}

// Pull replays the working-area layers on its recorded trunk and installs one leaf.
func (s *Service) Pull(ctx context.Context, request PullRequest) (PullResult, error) {
	if request.Workspace == "" || request.Lead == "" || request.Repo == "" || request.RequestID == "" {
		return PullResult{}, errors.New("workspace, lead, repo and request ID are required")
	}
	remote := request.Remote
	if remote == "" {
		remote = "origin"
	}
	repos, err := s.store.WorkspaceRepos(ctx, request.Workspace)
	if err != nil {
		return PullResult{}, err
	}
	trunk := ""
	for _, repo := range repos {
		if repo.Repo == request.Repo {
			trunk = repo.Trunk
			break
		}
	}
	if trunk == "" {
		return PullResult{}, loomgit.NewError(loomgit.RepoSelectionRequired, "recorded trunk is unavailable", nil)
	}
	if request.SourceBranch != "" && request.SourceBranch != trunk {
		return PullResult{}, loomgit.NewError(loomgit.Stale, "requested branch differs from recorded trunk", nil)
	}
	if _, err := s.runner.Run(ctx, "fetch", remote, trunk); err != nil {
		return PullResult{}, fmt.Errorf("fetch recorded trunk: %w", err)
	}
	return s.pullFetched(ctx, request, remote+"/"+trunk)
}

func (s *Service) pullFetched(ctx context.Context, request PullRequest, trunkRef string) (PullResult, error) {
	var result PullResult
	err := s.repo.WithLock(ctx, func(ctx context.Context) error {
		if err := s.reconcilePullPlans(ctx, request.Workspace, request.Lead, request.Repo); err != nil {
			return err
		}
		old, err := git(ctx, s.runner, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		base, err := git(ctx, s.runner, "rev-parse", trunkRef)
		if err != nil {
			return err
		}
		layers, err := s.appliedLog(ctx, request.Workspace, request.Lead, false)
		if err != nil {
			return err
		}
		rebuilt, cursor, paths, err := s.replayPullLayers(ctx, request, base, layers)
		result.Paths, result.HeadSHA = paths, cursor
		if err != nil {
			return err
		}
		result.AlreadyUpToDate = cursor == old && base == old
		if result.AlreadyUpToDate {
			return nil
		}
		return s.installPull(ctx, request, old, base, rebuilt, &result)
	})
	return result, err
}

func (s *Service) replayPullLayers(ctx context.Context, request PullRequest, base string,
	layers []loomgit.AppliedLayer) ([]pulledLayer, string, []string, error) {
	cursor := base
	var rebuilt []pulledLayer
	engine := replay.New(s.repo)
	for index, layer := range layers {
		landed, err := s.store.IsLanded(ctx, request.Workspace, layer.Change)
		if err != nil {
			return nil, "", nil, err
		}
		if landed {
			continue
		}
		var source loomgit.Revision
		if layer.Revision == 0 {
			source = loomgit.Revision{Workspace: request.Workspace, Change: layer.Change,
				BaseSHA: layer.OldTip, HeadSHA: layer.NewTip}
		} else {
			source, err = s.store.GetRevision(ctx, request.Workspace, layer.Change, layer.Revision)
			if err != nil {
				return nil, "", nil, err
			}
		}
		trial, err := engine.TrialMerge(ctx, source.BaseSHA, source.HeadSHA, cursor)
		if err != nil {
			return nil, "", nil, err
		}
		if trial.ConflictCommit != "" {
			return nil, "", trial.ConflictingPaths,
				loomgit.NewError(loomgit.Conflict, strings.Join(trial.ConflictingPaths, ", "), nil)
		}
		rebuilt = append(rebuilt, pulledLayer{source: source, trial: trial, base: cursor, original: layer,
			layer: loomgit.AppliedLayer{RequestID: fmt.Sprintf("%s:layer:%d", request.RequestID, index),
				Workspace: request.Workspace, Lead: request.Lead, Change: layer.Change,
				OldTip: cursor, NewTip: trial.HeadSHA, DroppedCommits: trial.DroppedCommits}})
		cursor = trial.HeadSHA
	}
	return rebuilt, cursor, nil, nil
}

func (s *Service) installPull(ctx context.Context, request PullRequest, old, base string, rebuilt []pulledLayer, result *PullResult) error {
	return s.withPullIndexLock(ctx, request, old, func(branch, indexPath string, lockOwned, keepLock *bool) error {
		completed, err := s.preparePulledLayers(ctx, request, rebuilt)
		if err != nil {
			return err
		}
		result.Paths, err = s.pendingPaths(ctx, old, result.HeadSHA)
		if err != nil {
			return err
		}
		if len(result.Paths) > 0 {
			return loomgit.NewError(loomgit.SwapHeld, strings.Join(result.Paths, ", "), nil)
		}
		return s.finishPull(ctx, request, old, base, result.HeadSHA, branch, indexPath, completed, lockOwned, keepLock)
	})
}

func (s *Service) withPullIndexLock(ctx context.Context, request PullRequest, old string,
	action func(string, string, *bool, *bool) error) error {
	branch, err := git(ctx, s.runner, "symbolic-ref", "HEAD")
	if err != nil {
		return err
	}
	want, err := refname.InteractiveBranch(request.Workspace, request.Lead)
	if err != nil {
		return err
	}
	if branch != "refs/heads/"+want {
		return loomgit.NewError(loomgit.Stale, "working area branch differs from lead", nil)
	}
	indexPath, err := git(ctx, s.runner, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(indexPath+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // Git resolves the active index; O_EXCL reserves its lock.
	if errors.Is(err, os.ErrExist) {
		return loomgit.NewError(loomgit.SwapHeld, "working area index is locked", err)
	}
	if err != nil {
		return err
	}
	lockOwned, keepLock := true, false
	defer func() {
		_ = lock.Close()
		if lockOwned && !keepLock {
			_ = os.Remove(indexPath + ".lock")
		}
	}()
	actual, err := git(ctx, s.runner, "rev-parse", "HEAD")
	if err != nil || actual != old {
		return errors.Join(errHeadMoved, err)
	}
	return action(branch, indexPath, &lockOwned, &keepLock)
}

func (s *Service) preparePulledLayers(ctx context.Context, request PullRequest, rebuilt []pulledLayer) ([]loomgit.AppliedLayer, error) {
	completed := make([]loomgit.AppliedLayer, 0, len(rebuilt))
	for index := range rebuilt {
		item := &rebuilt[index]
		if item.original.Revision == 0 {
			if err := s.recordOwnLayer(ctx, &item.original); err != nil {
				return nil, err
			}
			source, err := s.store.GetRevision(ctx, request.Workspace, item.layer.Change, item.original.Revision)
			if err != nil {
				return nil, err
			}
			item.source = source
		}
		derived, err := changeset.RecordDerived(ctx, s.store, s.runner, changeset.DerivedInput{
			Workspace: request.Workspace, Change: item.layer.Change, RequestID: item.layer.RequestID + ":derived",
			FromNumber: item.source.Number, Operation: "pull", BaseSHA: item.base,
			HeadSHA: item.trial.HeadSHA, Outcome: item.source.Outcome})
		if err != nil {
			return nil, err
		}
		item.layer.Revision = derived.Number
		commits, err := git(ctx, s.runner, "rev-list", "--reverse", "--first-parent", item.base+".."+item.trial.HeadSHA)
		if err != nil {
			return nil, err
		}
		item.layer.Commits = strings.Fields(commits)
		for _, sha := range item.layer.Commits {
			attribution, err := s.attribute(ctx, sha, item.layer.Change)
			if err != nil {
				return nil, err
			}
			item.layer.CommitDetails = append(item.layer.CommitDetails, attribution)
		}
		if _, _, err := review.CarryForward(ctx, s.store, s.runner, item.source, derived, item.trial); err != nil {
			return nil, err
		}
		completed = append(completed, item.layer)
	}
	return completed, nil
}

func (s *Service) finishPull(ctx context.Context, request PullRequest, old, base, next, branch, indexPath string,
	completed []loomgit.AppliedLayer, lockOwned, keepLock *bool) error {
	aggregate := loomgit.AppliedLayer{RequestID: request.RequestID, Workspace: request.Workspace,
		Lead: request.Lead, Change: "pull", OldTip: old, NewTip: next}
	if err := s.store.SavePullPlan(ctx, journal.PullPlan{RequestID: request.RequestID, Workspace: request.Workspace,
		Lead: request.Lead, Repo: request.Repo, BaseSHA: base, Layers: completed}); err != nil {
		return err
	}
	if err := s.store.SaveApplied(ctx, aggregate); err != nil {
		return err
	}
	if err := s.install(ctx, branch, indexPath, old, next, request.RequestID, lockOwned, keepLock); err != nil {
		return err
	}
	if s.beforeCompletePull != nil {
		if err := s.beforeCompletePull(); err != nil {
			return err
		}
	}
	return s.store.CompletePull(ctx, request.RequestID, request.Workspace, request.Lead, request.Repo, base, completed)
}
