package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type Result struct {
	Revision      loomgit.Revision
	PRURL         string
	PRNumber      int
	AlreadyExists bool
}

// PublishLocal resolves a recorded change and its lead working area before publishing.
func PublishLocal(ctx context.Context, workspace, lead, change string) (Result, error) {
	if workspace == "" || lead == "" || change == "" {
		return Result{}, errors.New("workspace, lead and change are required")
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
	return publishRecorded(ctx, store, cfg, workspace, lead, change, nil, "", "")
}

func publishRecorded(ctx context.Context, store *journal.SQLite, cfg *config.LoomConfig, workspace, lead, change string, forge Forge, token, slug string) (Result, error) {
	repoName, err := store.RepoForChange(ctx, workspace, change)
	if err != nil {
		return Result{}, fmt.Errorf("find repo for change: %w", err)
	}
	area, err := workingArea(ctx, store, workspace, lead, repoName)
	if err != nil {
		return Result{}, err
	}
	_, configured, found := config.WorkspaceByID(cfg, workspace)
	if !found {
		return Result{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "workspace is unavailable", nil)
	}
	for _, repo := range configured.Repos {
		if repo.Name != repoName {
			continue
		}
		prior, priorExists, err := store.Publication(ctx, workspace, change)
		if err != nil {
			return Result{}, err
		}
		revision, err := Publish(ctx, store, Request{
			Workspace: workspace, Lead: lead, Change: change,
			Repo: repo.ResolveAbsPath(configured.Path), WorkingArea: area.Path,
			BaseSHA: area.BaseSHA, RepoName: repoName, forge: forge, token: token, slug: slug,
		})
		if err != nil {
			return Result{}, err
		}
		publication, found, err := store.Publication(ctx, workspace, change)
		if err != nil {
			return Result{}, err
		}
		if !found || publication.Head != revision.HeadSHA || publication.Phase != "done" {
			return Result{}, errors.New("published PR record unavailable")
		}
		return Result{Revision: revision, PRURL: publication.PRURL, PRNumber: publication.PRNumber,
			AlreadyExists: priorExists && prior.Phase == "done" && prior.Head == revision.HeadSHA}, nil
	}
	return Result{}, loomgit.NewError(loomgit.RepoSelectionRequired, "change repo is not in the workspace", nil)
}

func workingArea(ctx context.Context, store *journal.SQLite, workspace, lead, repoName string) (*journal.WorkingArea, error) {
	areas, err := store.WorkingAreas(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	var area *journal.WorkingArea
	for index := range areas {
		if areas[index].Repo != repoName {
			continue
		}
		if area != nil {
			return nil, loomgit.NewError(loomgit.AttentionRequired, "ambiguous working area for change repo and lead", nil)
		}
		area = &areas[index]
	}
	if area == nil || area.Path == "" {
		return nil, loomgit.NewError(loomgit.AttentionRequired, "working area for change repo and lead is unavailable", nil)
	}
	return area, nil
}
