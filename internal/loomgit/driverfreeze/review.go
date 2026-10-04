package driverfreeze

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// AttemptAwaitsReview reports whether a finished run attempt froze code that
// must be reviewed before its task closes (D29). That is any revision of the
// attempt except "No changes" (P1.25), which closes the task, and except a
// fix-up of a change whose PR is already open, which updates that PR without
// a review (P2.19b). An attempt with no revision (no Loom Git copy) does not
// wait.
func AttemptAwaitsReview(ctx context.Context, workspace, attempt string) (bool, error) {
	return AttemptAwaitsReviewAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), workspace, attempt)
}

func AttemptAwaitsReviewAt(ctx context.Context, path, workspace, attempt string) (bool, error) {
	if workspace == "" || attempt == "" {
		return false, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = store.Close() }()
	revision, err := store.RevisionByRequest(ctx, "driver:"+attempt)
	if errors.Is(err, journal.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if revision.Workspace != workspace || revision.NoChanges {
		return false, nil
	}
	publication, found, err := store.Publication(ctx, workspace, revision.Change)
	if err != nil {
		return false, err
	}
	return !found || publication.Phase != "done", nil
}
