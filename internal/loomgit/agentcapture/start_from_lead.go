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
	BaseSHA string // the commit the checkout now sits on
	Moved   bool   // the checkout's HEAD changed
}

// StartFromLead puts a daemon agent's reused checkout on the lead's current
// working-area head before a new attempt (P1.28, D23), so the attempt never
// builds on an earlier attempt's leftover commits.
func StartFromLead(ctx context.Context, source, checkout, workspace, lead, repo string) (StartResult, error) {
	return StartFromLeadAt(ctx, defaultJournal(), source, checkout, workspace, lead, repo, "")
}

// StartFromBase puts the checkout on base instead: a dependent task's frozen
// blocker revision. The same unsaved-work guards apply.
func StartFromBase(ctx context.Context, source, checkout, workspace, lead, repo, base string) (StartResult, error) {
	return StartFromLeadAt(ctx, defaultJournal(), source, checkout, workspace, lead, repo, base)
}

func defaultJournal() string {
	return filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
}

// StartFromLeadAt is StartFromLead with an explicit journal; a non-empty base
// is the commit to start from in place of the lead's head. With neither a lead
// working area for repo nor a base, the checkout is left alone. Uncommitted
// files, or commits that Loom has not saved, refuse the start with UnsavedWork,
// even when the checkout is already at the target.
func StartFromLeadAt(ctx context.Context, journalPath, source, checkout, workspace, lead, repo, base string) (StartResult, error) {
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
	tip := ""
	if area != nil {
		if tip, err = WorkingAreaTip(ctx, *area); err != nil {
			return StartResult{}, fmt.Errorf("read lead %q working area: %w", lead, err)
		}
	}
	target := base
	if target == "" {
		target = tip
	}
	if target == "" {
		return StartResult{}, nil
	}
	if source == "" {
		source = checkout
	}
	result := StartResult{HasLead: area != nil, BaseSHA: target}
	err = WithTaskCopyLease(ctx, journalPath, source, checkout, func(ctx context.Context) error {
		moved, err := moveToLead(ctx, store, checkout, workspace, target, tip)
		result.Moved = moved
		return err
	})
	return result, err
}

// moveToLead moves the checkout to target after refusing unsaved work; tip is
// the lead's head ("" without a lead working area).
func moveToLead(ctx context.Context, store *journal.SQLite, checkout, workspace, target, tip string) (bool, error) {
	runner, err := gitexec.New(checkout, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return false, err
	}
	if _, err := runner.Run(ctx, "cat-file", "-e", target+"^{commit}"); err != nil {
		return false, fmt.Errorf("start commit %s is not in the agent checkout's repository: %w", target, err)
	}
	unsaved, err := unsavedPaths(ctx, runner, checkout)
	if err != nil {
		return false, err
	}
	if len(unsaved) > 0 {
		return false, loomgit.NewError(loomgit.UnsavedWork,
			"agent checkout has uncommitted files: "+strings.Join(unsaved, ", "), nil)
	}
	out, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	head := strings.TrimSpace(string(out))
	if head == target {
		return false, nil
	}
	saved, err := headSaved(ctx, runner, store, workspace, head, target, tip)
	if err != nil {
		return false, err
	}
	if !saved {
		return false, loomgit.NewError(loomgit.UnsavedWork,
			fmt.Sprintf("agent checkout HEAD %s has commits Loom has not saved", head), nil)
	}
	args := []string{"checkout", "--detach", target}
	if branch, err := runner.Run(ctx, "symbolic-ref", "-q", "--short", "HEAD"); err == nil && strings.TrimSpace(string(branch)) != "" {
		args = []string{"checkout", "-B", strings.TrimSpace(string(branch)), target}
	}
	if _, err := runner.Run(ctx, args...); err != nil {
		return false, fmt.Errorf("move agent checkout to %s: %w", target, err)
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
// already in the target or the lead, is reachable from a Loom ref, or was
// frozen as a revision.
func headSaved(ctx context.Context, runner *gitexec.Runner, store *journal.SQLite, workspace, head string, kept ...string) (bool, error) {
	for _, commit := range kept {
		if commit == "" {
			continue
		}
		if _, err := runner.Run(ctx, "merge-base", "--is-ancestor", head, commit); err == nil {
			return true, nil
		}
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
