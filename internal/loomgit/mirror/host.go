package mirror

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func storePath() string { return filepath.Join(config.GetConfigDir(), "loomgit", "store.db") }

// RunOnce discovers committed local workspace repositories and retries their
// refs. A missing store is normal on hosts that have never used Loom Git v2.
func RunOnce(ctx context.Context) error {
	if _, err := os.Stat(storePath()); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	store, err := journal.OpenSQLite(storePath())
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureMirrorSchema(ctx); err != nil {
		return err
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		return err
	}
	seen := make(map[string]string)
	for workspace, local := range cache.Workspaces {
		repos, err := store.WorkspaceRepos(ctx, workspace)
		if err != nil {
			return err
		}
		for _, repo := range repos {
			path := localworkspace.RepoPath(local, repo.Repo)
			if path != "" {
				seen[path] = repo.BaseSHA
			}
		}
	}
	rows, err := store.MirrorRecords(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, ok := seen[row.Repo]; !ok {
			seen[row.Repo] = ""
		}
	}
	var failures []error
	for repo, base := range seen {
		if err := SyncRepo(ctx, store, repo, base); err != nil {
			failures = append(failures, fmt.Errorf("repo %s: %w", repo, err))
		}
	}
	return errors.Join(failures...)
}

// Status is a read-only snapshot suitable for the workspace command.
func Status(ctx context.Context) ([]journal.MirrorRecord, error) {
	if _, err := os.Stat(storePath()); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	store, err := journal.OpenSQLite(storePath())
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureMirrorSchema(ctx); err != nil {
		return nil, err
	}
	return store.MirrorRecords(ctx)
}
