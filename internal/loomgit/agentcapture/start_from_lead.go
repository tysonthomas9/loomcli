package agentcapture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// StartResult says where a reused agent checkout starts its next attempt.
type StartResult struct {
	HasLead bool   // the lead has a working area for the repo
	BaseSHA string // the lead's working-area head the checkout now sits on
	Moved   bool   // the checkout's HEAD changed
}

// StartFromLead puts a daemon agent's reused checkout on the lead's current
// working-area head before a new attempt (P1.28, D23), so the attempt never
// builds on an earlier attempt's leftover commits.
func StartFromLead(ctx context.Context, source, checkout, workspace, lead, repo string) (StartResult, error) {
	return StartFromLeadAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		source, checkout, workspace, lead, repo)
}

// StartFromLeadAt is StartFromLead with an explicit journal. A lead with no
// working area for repo leaves the checkout alone. Uncommitted files, or
// commits that Loom has not saved, refuse the move with UnsavedWork.
func StartFromLeadAt(ctx context.Context, journalPath, source, checkout, workspace, lead, repo string) (StartResult, error) {
	if workspace == "" || lead == "" || repo == "" || checkout == "" {
		return StartResult{}, errors.New("workspace, lead, repo and checkout are required")
	}
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		return StartResult{}, err
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return StartResult{}, err
	}
	defer func() { _ = store.Close() }()
	areas, err := store.WorkingAreas(ctx, workspace, lead)
	if err != nil {
		return StartResult{}, err
	}
	var area *journal.WorkingArea
	for i := range areas {
		if areas[i].Repo == repo {
			area = &areas[i]
		}
	}
	if area == nil {
		return StartResult{}, nil
	}
	tip, err := WorkingAreaTip(ctx, *area)
	if err != nil {
		return StartResult{}, fmt.Errorf("read lead %q working area: %w", lead, err)
	}
	if source == "" {
		source = checkout
	}
	result := StartResult{HasLead: true, BaseSHA: tip}
	if reader, err := gitexec.New(checkout, gitexec.Options{ReadOnly: true}); err == nil {
		if head, err := reader.Run(ctx, "rev-parse", "HEAD"); err == nil && strings.TrimSpace(string(head)) == tip {
			return result, nil // Already at the lead's head: no lease needed on an idle poll.
		}
	}
	err = WithTaskCopyLease(ctx, journalPath, source, checkout, func(ctx context.Context) error {
		moved, err := moveToLead(ctx, store, checkout, workspace, tip)
		result.Moved = moved
		return err
	})
	return result, err
}

func moveToLead(ctx context.Context, store *journal.SQLite, checkout, workspace, tip string) (bool, error) {
	runner, err := gitexec.New(checkout, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return false, err
	}
	if _, err := runner.Run(ctx, "cat-file", "-e", tip+"^{commit}"); err != nil {
		return false, fmt.Errorf("lead head %s is not in the agent checkout's repository: %w", tip, err)
	}
	out, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	head := strings.TrimSpace(string(out))
	if head == tip {
		return false, nil
	}
	unsaved, err := unsavedPaths(ctx, runner, checkout)
	if err != nil {
		return false, err
	}
	if len(unsaved) > 0 {
		return false, loomgit.NewError(loomgit.UnsavedWork,
			"agent checkout has uncommitted files: "+strings.Join(unsaved, ", "), nil)
	}
	saved, err := headSaved(ctx, runner, store, workspace, head, tip)
	if err != nil {
		return false, err
	}
	if !saved {
		return false, loomgit.NewError(loomgit.UnsavedWork,
			fmt.Sprintf("agent checkout HEAD %s has commits Loom has not saved", head), nil)
	}
	args := []string{"checkout", "--detach", tip}
	if branch, err := runner.Run(ctx, "symbolic-ref", "-q", "--short", "HEAD"); err == nil && strings.TrimSpace(string(branch)) != "" {
		args = []string{"checkout", "-B", strings.TrimSpace(string(branch)), tip}
	}
	if _, err := runner.Run(ctx, args...); err != nil {
		return false, fmt.Errorf("move agent checkout to lead head: %w", err)
	}
	return true, nil
}

// unsavedPaths lists tracked edits and untracked files, leaving out ignored
// paths and Loom or agent runtime files.
func unsavedPaths(ctx context.Context, runner *gitexec.Runner, checkout string) ([]string, error) {
	out, err := runner.Run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		status, path := entry[:2], entry[3:]
		if status[0] == 'R' || status[0] == 'C' {
			i++ // The next field is the rename or copy source.
		}
		if !capture.RuntimeFile(checkout, path, status != "??") {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// headSaved reports whether moving away from head loses no commit: head is
// already in the lead, is reachable from a Loom ref, or was frozen as a revision.
func headSaved(ctx context.Context, runner *gitexec.Runner, store *journal.SQLite, workspace, head, tip string) (bool, error) {
	if _, err := runner.Run(ctx, "merge-base", "--is-ancestor", head, tip); err == nil {
		return true, nil
	}
	prefix, err := refname.WorkspacePrefix(workspace)
	if err != nil {
		return false, err
	}
	refs, err := runner.Run(ctx, "for-each-ref", "--count=1", "--format=%(refname)", "--contains", head, prefix)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(refs)) != "" {
		return true, nil
	}
	return store.HasFrozenSource(ctx, workspace, head)
}

// WorkingAreaTip reads a lead working area's head, refusing one whose branch
// was switched away from the recorded lead branch.
func WorkingAreaTip(ctx context.Context, a journal.WorkingArea) (string, error) {
	if _, err := os.Stat(filepath.Join(a.Path, ".git")); err != nil {
		return "", err
	}
	r, err := gitexec.New(a.Path, gitexec.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	ref, err := r.Run(ctx, "rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(ref)) != "refs/heads/"+a.Branch {
		return "", errors.New("lead working area branch changed")
	}
	sha, err := r.Run(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(string(sha)), err
}
