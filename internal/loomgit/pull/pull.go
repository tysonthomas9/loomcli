package pull

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type Store interface {
	apply.Store
	WorkspaceRepos(context.Context, string) ([]loomgit.WorkspaceRepo, error)
	IsLanded(context.Context, string, string) (bool, error)
	CompletePull(context.Context, string, string, string, string, string, []loomgit.AppliedLayer) error
	SavePullPlan(context.Context, journal.PullPlan) error
	PendingPullPlans(context.Context, string, string) ([]journal.PullPlan, error)
	DiscardPullPlan(context.Context, string) error
	AbortRestack(context.Context, string, string, string, []string) error
	RevisionByRequest(context.Context, string) (loomgit.Revision, error)
}

type Service struct {
	store              Store
	repo               *pool.LocalRepo
	runner             *gitexec.Runner
	applier            *apply.Service
	beforeCompletePull func() error
	beforeRestackSwap  func() error
	scratchParent      string
}

func New(store Store, repo *pool.LocalRepo, runner *gitexec.Runner) *Service {
	return &Service{store: store, repo: repo, runner: runner, applier: apply.New(store, repo, runner)}
}

type PullRequest struct {
	Workspace, Lead, Repo, Remote, SourceBranch, RequestID string
	RemoveChange                                           string
}

type RestackRequest struct {
	Workspace, Lead, Repo, RequestID, BaseSHA string
	Order                                     []string
	RemoveChange                              string
}

type PullResult struct {
	HeadSHA         string
	Paths           []string
	AlreadyUpToDate bool
}

type pulledLayer struct {
	source    loomgit.Revision
	trial     replay.Result
	base      string
	layer     loomgit.AppliedLayer
	original  loomgit.AppliedLayer
	operation string
}

// Restack rebuilds the working area's layers on an explicit base. When supplied,
// Order contains every current change exactly once; moved changes record reorder revisions.
func (s *Service) Restack(ctx context.Context, request RestackRequest) (PullResult, error) {
	if request.Workspace == "" || request.Lead == "" || request.Repo == "" || request.RequestID == "" || request.BaseSHA == "" {
		return PullResult{}, errors.New("workspace, lead, repo, request ID and base are required")
	}
	var result PullResult
	err := s.repo.WithLock(ctx, func(ctx context.Context) error {
		if err := s.reconcilePullPlans(ctx, request.Workspace, request.Lead, request.Repo); err != nil {
			return err
		}
		old, err := git(ctx, s.runner, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		base, err := git(ctx, s.runner, "rev-parse", "--verify", request.BaseSHA+"^{commit}")
		if err != nil {
			return err
		}
		layers, err := s.appliedLog(ctx, request.Workspace, request.Lead, old)
		if err != nil {
			return err
		}
		layers, replayBase, err := withoutLayer(layers, request.RemoveChange, base)
		if err != nil {
			return err
		}
		layers, moved, err := orderLayers(layers, request.Order)
		if err != nil {
			return err
		}
		cleanup, err := s.prepareRestackScratch(ctx, request.RemoveChange, base)
		if err != nil {
			return err
		}
		defer cleanup()
		pullRequest := PullRequest{Workspace: request.Workspace, Lead: request.Lead, Repo: request.Repo,
			RequestID: request.RequestID, RemoveChange: request.RemoveChange}
		rebuilt, cursor, paths, err := s.replayPullLayers(ctx, pullRequest, replayBase, layers)
		result.Paths, result.HeadSHA = paths, cursor
		if err != nil {
			return err
		}
		setRestackOperations(rebuilt, request.RemoveChange, moved)
		return s.installRestack(ctx, request, pullRequest, old, base, rebuilt, &result)
	})
	return result, err
}

func withoutLayer(layers []loomgit.AppliedLayer, change, base string) ([]loomgit.AppliedLayer, string, error) {
	if change == "" {
		return layers, base, nil
	}
	for index, layer := range layers {
		if layer.Change == change {
			return layers[index+1:], layer.OldTip, nil
		}
	}
	return nil, "", loomgit.NewError(loomgit.Stale, "layer is not in the working area", nil)
}

func (s *Service) prepareRestackScratch(ctx context.Context, removed, base string) (func(), error) {
	if removed != "" {
		return func() {}, nil
	}
	scratch, err := os.MkdirTemp(s.scratchParent, "loom-restack-")
	if err != nil {
		return nil, fmt.Errorf("create restack scratch: %w", err)
	}
	cleanup := func() {
		_, _ = s.runner.Run(context.Background(), "worktree", "remove", "--force", scratch)
		_ = os.Remove(scratch)
	}
	if _, err := s.runner.Run(ctx, "worktree", "add", "--detach", filepath.Clean(scratch), base); err != nil {
		cleanup()
		return nil, fmt.Errorf("create restack checkout: %w", err)
	}
	return cleanup, nil
}

func setRestackOperations(rebuilt []pulledLayer, removed string, moved map[string]bool) {
	for index := range rebuilt {
		switch {
		case removed != "":
			rebuilt[index].operation = "unapply"
		case moved[rebuilt[index].layer.Change]:
			rebuilt[index].operation = "reorder"
		default:
			rebuilt[index].operation = "restack"
		}
	}
}

func orderLayers(layers []loomgit.AppliedLayer, order []string) ([]loomgit.AppliedLayer, map[string]bool, error) {
	if len(order) == 0 {
		order = make([]string, 0, len(layers))
		for _, layer := range layers {
			order = append(order, layer.Change)
		}
	}
	if len(order) != len(layers) {
		return nil, nil, errors.New("restack order must name every layer exactly once")
	}
	byChange := make(map[string]loomgit.AppliedLayer, len(layers))
	for _, layer := range layers {
		if _, exists := byChange[layer.Change]; exists {
			return nil, nil, fmt.Errorf("restack has repeated change %q", layer.Change)
		}
		byChange[layer.Change] = layer
	}
	ordered := make([]loomgit.AppliedLayer, 0, len(layers))
	moved := make(map[string]bool, len(layers))
	for index, change := range order {
		layer, exists := byChange[change]
		if !exists {
			return nil, nil, fmt.Errorf("unknown or repeated restack layer %q", change)
		}
		delete(byChange, change)
		ordered = append(ordered, layer)
		moved[change] = layers[index].Change != change
	}
	return ordered, moved, nil
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
		layers, err := s.appliedLog(ctx, request.Workspace, request.Lead, old)
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
				loomgit.NewError(loomgit.Conflict, strings.Join(trial.ConflictingPaths, ", ")+"; create a fix-up task for "+layer.Change, nil)
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
	completed, err := s.preparePulledLayers(ctx, request, rebuilt)
	if err != nil {
		return err
	}
	if err := s.store.SavePullPlan(ctx, journal.PullPlan{RequestID: request.RequestID, Workspace: request.Workspace,
		Lead: request.Lead, Repo: request.Repo, BaseSHA: base, Layers: completed, RemoveChange: request.RemoveChange}); err != nil {
		return err
	}
	if len(rebuilt) > 0 && rebuilt[0].operation != "" && s.beforeRestackSwap != nil {
		if err := s.beforeRestackSwap(); err != nil {
			return err
		}
	}
	result.Paths, err = s.applier.SwapPrepared(ctx, request.Workspace, request.Lead, request.RequestID, old, result.HeadSHA)
	if err != nil {
		return err
	}
	if s.beforeCompletePull != nil {
		if err := s.beforeCompletePull(); err != nil {
			return err
		}
	}
	return s.store.CompletePull(ctx, request.RequestID, request.Workspace, request.Lead, request.Repo, base, completed)
}

func (s *Service) preparePulledLayers(ctx context.Context, request PullRequest, rebuilt []pulledLayer) ([]loomgit.AppliedLayer, error) {
	completed := make([]loomgit.AppliedLayer, 0, len(rebuilt))
	for index := range rebuilt {
		item := &rebuilt[index]
		if item.original.Revision == 0 {
			if err := s.applier.RecordOwnLayer(ctx, &item.original); err != nil {
				return nil, err
			}
			source, err := s.store.GetRevision(ctx, request.Workspace, item.layer.Change, item.original.Revision)
			if err != nil {
				return nil, err
			}
			item.source = source
		}
		operation := item.operation
		if operation == "" {
			operation = "pull"
		}
		derived, err := changeset.RecordDerived(ctx, s.store, s.runner, changeset.DerivedInput{
			Workspace: request.Workspace, Change: item.layer.Change, RequestID: item.layer.RequestID + ":derived",
			FromNumber: item.source.Number, Operation: operation, BaseSHA: item.base,
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
			attribution, err := s.applier.AttributeCommit(ctx, sha, item.layer.Change)
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
