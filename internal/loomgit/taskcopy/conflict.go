package taskcopy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// PrepareConflictResolution stages only the selected revision's commits in a
// fresh task copy based on the lead tip. A conflicting cherry-pick leaves Git's
// conflict markers and sequencer in the copy for the agent to resolve. The
// lead working area and its index are never changed.
func PrepareConflictResolution(ctx context.Context, source, target, workspace, lead, taskID, repoName, change string, number int) error {
	store, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	revision, err := conflictRevision(ctx, store, workspace, lead, taskID, repoName, change, number)
	if err != nil {
		return err
	}
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	runner, err := gitexec.New(target, options)
	if err != nil {
		return err
	}
	if err := importConflictHead(ctx, runner, source, workspace, change, number, revision.HeadSHA); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "merge-base", "--is-ancestor", revision.BaseSHA, revision.HeadSHA); err != nil {
		return fmt.Errorf("conflict revision base is not an ancestor of head: %w", err)
	}
	out, err := runner.Run(ctx, "rev-list", "--reverse", "--first-parent", revision.BaseSHA+".."+revision.HeadSHA)
	if err != nil {
		return err
	}
	commits := strings.Fields(string(out))
	if len(commits) == 0 {
		return errors.New("conflict revision has no agent commits")
	}
	args := append([]string{"cherry-pick", "--no-commit"}, commits...)
	if _, err := runner.Run(ctx, args...); err != nil {
		unmerged, checkErr := runner.Run(ctx, "ls-files", "-u")
		if checkErr != nil || len(unmerged) == 0 {
			return fmt.Errorf("prepare conflict resolution: %w", errors.Join(err, checkErr))
		}
	}
	return nil
}

// ValidateConflictResolution rejects an ineligible revision before a task
// copy is created. PrepareConflictResolution repeats the check before writing.
func ValidateConflictResolution(ctx context.Context, workspace, lead, taskID, repoName, change string, number int) error {
	store, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	_, err = conflictRevision(ctx, store, workspace, lead, taskID, repoName, change, number)
	return err
}

func conflictRevision(ctx context.Context, store *journal.SQLite, workspace, lead, taskID, repoName, change string, number int) (loomgit.Revision, error) {
	owner, err := store.RepoForChange(ctx, workspace, change)
	if err != nil {
		return loomgit.Revision{}, err
	}
	if owner != repoName {
		return loomgit.Revision{}, errors.New("conflict revision belongs to a different repo")
	}
	linked, err := store.DriverChange(ctx, workspace, taskID, repoName, change)
	if err != nil {
		return loomgit.Revision{}, fmt.Errorf("conflict revision does not belong to task: %w", err)
	}
	if linked != change {
		return loomgit.Revision{}, errors.New("conflict revision does not belong to task")
	}
	revision, err := store.GetRevision(ctx, workspace, change, number)
	if err != nil {
		return loomgit.Revision{}, err
	}
	if !revision.Ready || revision.BaseSHA == "" || revision.HeadSHA == "" {
		return loomgit.Revision{}, errors.New("conflict revision is not ready")
	}
	if err := review.RequireVerdict(ctx, store, workspace, change, number, revision.HeadSHA, "apply", lead); err != nil {
		return loomgit.Revision{}, err
	}
	return revision, nil
}

func importConflictHead(ctx context.Context, runner *gitexec.Runner, source, workspace, change string, number int, head string) error {
	if _, err := runner.Run(ctx, "cat-file", "-e", head+"^{commit}"); err != nil {
		// Clone-mode task copies may not share objects with the revision source.
		ref, err := refname.RevisionHead(workspace, change, strconv.Itoa(number))
		if err != nil {
			return err
		}
		sourceRunner, err := gitexec.New(source, gitexec.Options{ReadOnly: true})
		if err != nil {
			return err
		}
		published, err := sourceRunner.Run(ctx, "rev-parse", "--verify", ref+"^{commit}")
		if err != nil {
			return fmt.Errorf("conflict revision head is unavailable from source: %w", err)
		}
		if strings.TrimSpace(string(published)) != head {
			return errors.New("conflict revision head differs from source ref")
		}
		if _, err := runner.Run(ctx, "fetch", "--no-tags", "--no-write-fetch-head", source, ref); err != nil {
			return fmt.Errorf("import conflict revision into task copy: %w", err)
		}
		if _, err := runner.Run(ctx, "cat-file", "-e", head+"^{commit}"); err != nil {
			return fmt.Errorf("imported conflict revision head is missing: %w", err)
		}
	}
	return nil
}
