package gitread

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// TaskStore reads a task's revisions and the lead stack they are applied in.
type TaskStore interface {
	ListTaskRevisions(context.Context, string, string) ([]loomgit.Revision, error)
	RepoForChange(context.Context, string, string) (string, error)
	WorkingAreaForAppliedChange(context.Context, string, string, string) (journal.WorkingArea, error)
	AppliedLog(context.Context, string, string) ([]loomgit.AppliedLayer, error)
	WorkingAreas(context.Context, string, string) ([]journal.WorkingArea, error)
	DeliveryMode(context.Context, string) (string, error)
}

// TaskDiff is one task's reviewable diff: its newest revision against the
// layer below it in the lead's stack (what its PR contains), else its base.
// In trunk mode each task's PR is its own revision on trunk, so it is always
// the revision against its base.
type TaskDiff struct {
	Diff
	Change string `json:"change"`
	Repo   string `json:"repo"`
	// Compare is "layer" (the layer below), "trunk" (bottom layer, or trunk
	// mode) or "base" (not applied: the revision's own base).
	Compare string `json:"compare"`
}

// TaskDiff resolves the task's newest revision and diffs it the way its PR
// shows it. An empty lead uses the lead that most recently applied the change.
func (r *Reader) TaskDiff(ctx context.Context, workspace, task, lead string) (TaskDiff, error) {
	revisions, err := r.Tasks.ListTaskRevisions(ctx, workspace, task)
	if err != nil {
		return TaskDiff{}, err
	}
	if len(revisions) == 0 {
		return TaskDiff{}, journal.ErrNotFound
	}
	newest := revisions[0]
	repoName, err := r.Tasks.RepoForChange(ctx, workspace, newest.Change)
	if err != nil {
		return TaskDiff{}, err
	}
	repo, err := r.repo(ctx, workspace, repoName)
	if err != nil {
		return TaskDiff{}, err
	}
	out := TaskDiff{Change: newest.Change, Repo: repoName, Compare: "base"}
	from, to, err := r.pair(ctx, repo, newest)
	if err != nil {
		return TaskDiff{}, err
	}
	mode, err := r.Tasks.DeliveryMode(ctx, workspace)
	if err != nil {
		return TaskDiff{}, err
	}
	layer, below, found, err := r.appliedLayer(ctx, workspace, lead, repoName, newest)
	if err != nil {
		return TaskDiff{}, err
	}
	if mode == "trunk" {
		out.Compare = "trunk"
	} else if found {
		from, to, out.Compare = layer.OldTip, layer.NewTip, "trunk"
		if below {
			out.Compare = "layer"
		}
	}
	files, err := r.files(ctx, repo, from, to)
	if err != nil {
		return TaskDiff{}, err
	}
	out.Diff = Diff{Revision: newest.Number, Files: files}
	return out, nil
}

// appliedLayer finds the lead's current layer for this exact revision and
// whether anything (a task layer or the lead's own commits) sits below it,
// i.e. the layer does not start at the working area's trunk base.
func (r *Reader) appliedLayer(ctx context.Context, workspace, lead, repo string, rev loomgit.Revision) (loomgit.AppliedLayer, bool, bool, error) {
	none := func(err error) (loomgit.AppliedLayer, bool, bool, error) {
		return loomgit.AppliedLayer{}, false, false, err
	}
	var area journal.WorkingArea
	if lead == "" {
		var err error
		area, err = r.Tasks.WorkingAreaForAppliedChange(ctx, workspace, rev.Change, repo)
		if errors.Is(err, sql.ErrNoRows) {
			return none(nil)
		}
		if err != nil {
			return none(err)
		}
		lead = area.Lead
	} else {
		areas, err := r.Tasks.WorkingAreas(ctx, workspace, lead)
		if err != nil {
			return none(err)
		}
		for _, candidate := range areas {
			if candidate.Repo == repo {
				area = candidate
			}
		}
	}
	layers, err := r.Tasks.AppliedLog(ctx, workspace, lead)
	if err != nil {
		return none(err)
	}
	for i := len(layers) - 1; i >= 0; i-- {
		if layers[i].Change != rev.Change {
			continue
		}
		if layers[i].Revision != rev.Number {
			return none(nil)
		}
		return layers[i], layers[i].OldTip != area.BaseSHA, true, nil
	}
	return none(nil)
}

// CommitDate reports a recorded commit's committer date (ISO 8601).
func (r *Reader) CommitDate(ctx context.Context, workspace, repoName, sha string) (string, error) {
	repo, err := r.repo(ctx, workspace, repoName)
	if err != nil {
		return "", err
	}
	out, err := repo.Run(ctx, "show", "-s", "--format=%cI", sha)
	return strings.TrimSpace(string(out)), err
}
