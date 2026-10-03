package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// ErrNoWorkingArea is the cause when the lead has no working area for the
// change repo (as opposed to an ambiguous one).
var ErrNoWorkingArea = errors.New("no working area for change repo and lead")

func ApproveLocal(ctx context.Context, workspace, lead, change string, revision int, actor review.Actor) (FollowResult, error) {
	return ApproveLocalPublishing(ctx, workspace, lead, change, revision, actor, false)
}

// ApproveLocalPublishing approves and follows; with publish it also records
// the intent to open the change's PR once applied (D29).
func ApproveLocalPublishing(ctx context.Context, workspace, lead, change string, revision int, actor review.Actor, publish bool) (FollowResult, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return FollowResult{}, err
	}
	r, err := store.GetRevision(ctx, workspace, change, revision)
	if err != nil {
		_ = store.Close()
		return FollowResult{}, err
	}
	_, err = review.SubmitForLeadPublishing(ctx, store, workspace, change, revision, r.HeadSHA, "approve", "", actor, lead, publish)
	if closeErr := store.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return FollowResult{}, err
	}
	return FollowLocal(ctx, workspace, lead)
}

func ApplyLocal(ctx context.Context, request Request) (Result, error) {
	if request.Lead == "" {
		request.Lead = "lead"
	}
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); err != nil {
		return Result{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "revision journal is unavailable", err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = store.Close() }()
	cfg, err := config.LoadConfig()
	if err != nil {
		return Result{}, err
	}
	return applyLocalWithStore(ctx, request, store, cfg)
}

func applyLocalWithStore(ctx context.Context, request Request, store *journal.SQLite, cfg *config.LoomConfig) (Result, error) {
	repoName, err := store.RepoForChange(ctx, request.Workspace, request.Change)
	if err != nil {
		return Result{}, fmt.Errorf("find repo for change: %w", err)
	}
	areas, err := store.WorkingAreas(ctx, request.Workspace, request.Lead)
	if err != nil {
		return Result{}, err
	}
	selected, err := workingAreaForRepo(areas, repoName)
	if err != nil {
		return Result{}, err
	}
	_, workspace, found := config.WorkspaceByID(cfg, request.Workspace)
	if !found {
		return Result{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "workspace is unavailable", nil)
	}
	for _, candidate := range workspace.Repos {
		if candidate.Name != repoName {
			continue
		}
		options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
		repo, err := pool.New(store, options).Admit(ctx, selected.Path)
		if err != nil {
			return Result{}, err
		}
		runner, err := gitexec.New(selected.Path, options)
		if err != nil {
			return Result{}, err
		}
		return New(store, repo, runner).Apply(ctx, request)
	}
	return Result{}, loomgit.NewError(loomgit.RepoSelectionRequired, "change repo is not in the workspace", nil)
}

func workingAreaForRepo(areas []journal.WorkingArea, repo string) (*journal.WorkingArea, error) {
	var selected *journal.WorkingArea
	for index := range areas {
		if areas[index].Repo != repo {
			continue
		}
		if selected != nil {
			return nil, loomgit.NewError(loomgit.AttentionRequired, "ambiguous working area for change repo and lead", nil)
		}
		selected = &areas[index]
	}
	if selected == nil || selected.Path == "" {
		return nil, loomgit.NewError(loomgit.AttentionRequired, "working area for change repo and lead is unavailable", ErrNoWorkingArea)
	}
	return selected, nil
}
