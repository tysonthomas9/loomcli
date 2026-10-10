package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// verdictTarget is one revision a verdict command will decide, pinned to the
// head it showed (S4).
type verdictTarget struct {
	Change, Repo, HeadSHA string
	Number                int
}

var verdictTaskRevisions = func(ctx context.Context, workspace, task string) ([]review.TaskRevision, error) {
	store, err := review.OpenLocal()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return store.TaskRevisions(ctx, workspace, task)
}

var verdictRevision = func(ctx context.Context, workspace, change string, number int) (loomgit.Revision, error) {
	store, err := review.OpenLocal()
	if err != nil {
		return loomgit.Revision{}, err
	}
	defer func() { _ = store.Close() }()
	return store.Revision(ctx, workspace, change, number)
}

var verdictTaskForChange = func(ctx context.Context, workspace, change string) (string, error) {
	store, err := review.OpenLocal()
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	return store.TaskForChange(ctx, workspace, change)
}

// resolveVerdictTargets reads `<task>`, `<change> <revision>` or
// --change/--revision into the revisions to decide, each pinned to its
// current head. For a task that is the newest revision of each change (one
// per repo). It also returns the task, when known.
func resolveVerdictTargets(ctx context.Context, workspace string, args []string, change string, revision int) ([]verdictTarget, string, error) {
	if change == "" && len(args) == 2 {
		change = args[0]
		number, err := strconv.Atoi(args[1])
		if err != nil || number < 1 {
			return nil, "", fmt.Errorf("revision must be a positive number")
		}
		revision = number
	}
	if change != "" {
		if len(args) == 1 || revision < 1 {
			return nil, "", fmt.Errorf("give a task, or a change with its revision (--change and --revision must be used together)")
		}
		r, err := verdictRevision(ctx, workspace, change, revision)
		if err != nil {
			return nil, "", err
		}
		task, err := verdictTaskForChange(ctx, workspace, change)
		if err != nil && !review.IsNotFound(err) {
			return nil, "", err
		}
		return []verdictTarget{{Change: change, Number: revision, HeadSHA: r.HeadSHA}}, task, nil
	}
	if len(args) != 1 || revision > 0 {
		return nil, "", fmt.Errorf("give a task, or a change with its revision")
	}
	task := args[0]
	revisions, err := verdictTaskRevisions(ctx, workspace, task)
	if err != nil {
		return nil, "", err
	}
	var targets []verdictTarget
	seen := map[string]bool{}
	for _, r := range revisions {
		if seen[r.ChangeID] {
			continue
		}
		seen[r.ChangeID] = true
		targets = append(targets, verdictTarget{Change: r.ChangeID, Repo: r.Repo, Number: r.Number, HeadSHA: r.HeadSHA})
	}
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("task %s has no recorded revision to review", task)
	}
	return targets, task, nil
}

// printVerdictPlan says exactly what the command is about to decide, by whom.
func printVerdictPlan(out io.Writer, verb string, actor commandActor, lead string, targets []verdictTarget) error {
	if _, err := fmt.Fprintf(out, "%s as %s %s, for lead %s:\n", verb, actor.Kind, actor.ID, lead); err != nil {
		return err
	}
	for _, target := range targets {
		repo := ""
		if target.Repo != "" {
			repo = " in " + target.Repo
		}
		if _, err := fmt.Fprintf(out, "  %s revision %d%s at %s\n", target.Change, target.Number, repo, shortSHA(target.HeadSHA)); err != nil {
			return err
		}
	}
	return nil
}

// pinReviewedHeads refuses, as stale, a target whose head is not one the
// reviewer pinned with --head (S4): a new attempt arrived after they looked.
// A pinned head may be abbreviated to at least 7 characters.
func pinReviewedHeads(targets []verdictTarget, heads []string) error {
	if len(heads) == 0 {
		return nil
	}
	for _, target := range targets {
		pinned := false
		for _, head := range heads {
			if head == target.HeadSHA || (len(head) >= 7 && strings.HasPrefix(target.HeadSHA, head)) {
				pinned = true
			}
		}
		if !pinned {
			return staleVerdictError(target, loomgit.NewError(loomgit.StaleSubject,
				"its head "+shortSHA(target.HeadSHA)+" is not the reviewed head "+strings.Join(heads, ", "), nil))
		}
	}
	return nil
}

// refuseOwnTask refuses a task agent deciding its own task (D42). Review also
// refuses an agent approving a revision it authored.
func refuseOwnTask(verb string, actor commandActor, task string) error {
	if actor.Kind == "agent" && actor.Task != "" && actor.Task == task {
		return loomgit.NewError(loomgit.ReviewRequired, fmt.Sprintf("a task agent cannot %s its own task %s; a human or the lead must", verb, task), nil)
	}
	return nil
}

// staleVerdictError explains a refusal because the code moved after it was shown.
func staleVerdictError(target verdictTarget, err error) error {
	if errors.Is(err, loomgit.NewError(loomgit.StaleSubject, "", nil)) || errors.Is(err, loomgit.NewError(loomgit.RevisionSuperseded, "", nil)) {
		return fmt.Errorf("stale: %s revision %d at %s is not the code that was reviewed (%w); nothing was recorded for it, run the command with --dry-run to see the new attempt",
			target.Change, target.Number, shortSHA(target.HeadSHA), err)
	}
	return fmt.Errorf("%s revision %d: %w", target.Change, target.Number, err)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
