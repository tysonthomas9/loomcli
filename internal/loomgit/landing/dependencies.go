package landing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

// Predecessors lists the tasks a workspace task waits for.
type Predecessors func(ctx context.Context, workspace, task string) ([]string, error)

// DependencyStore is the journal surface loom/dependencies reads.
type DependencyStore interface {
	TaskForChange(context.Context, string, string) (string, error)
	RepoForChange(context.Context, string, string) (string, error)
	TaskChanges(context.Context, string, string) (map[string]string, error)
	Publication(context.Context, string, string) (journal.Publication, bool, error)
	LandingStatus(context.Context, string, string) (journal.LandingStatus, error)
	ChangeAbandoned(context.Context, string, string) (bool, error)
}

// CrossRepoDependencies evaluates loom/dependencies for one change: success
// only once every predecessor change in another repository has landed (P4.4
// rule). found is false when the change's task has no such predecessor.
func CrossRepoDependencies(ctx context.Context, store DependencyStore, workspace, change string, predecessors Predecessors) (stackpublish.DependencyStatus, bool, error) {
	task, err := store.TaskForChange(ctx, workspace, change)
	if err != nil || task == "" {
		return stackpublish.DependencyStatus{}, false, err
	}
	repo, err := store.RepoForChange(ctx, workspace, change)
	if err != nil {
		return stackpublish.DependencyStatus{}, false, err
	}
	tasks, err := predecessors(ctx, workspace, task)
	if err != nil {
		return stackpublish.DependencyStatus{}, false, err
	}
	sort.Strings(tasks)
	found := false
	var waiting []string
	for _, predecessor := range tasks {
		changes, err := store.TaskChanges(ctx, workspace, predecessor)
		if err != nil {
			return stackpublish.DependencyStatus{}, false, err
		}
		if len(changes) == 0 {
			// Its repository is unknown until it records a change, so it may be
			// another repo: fail closed.
			found = true
			waiting = append(waiting, "task "+predecessor+" (no change recorded yet)")
			continue
		}
		repos := make([]string, 0, len(changes))
		for name := range changes {
			if name != repo {
				repos = append(repos, name)
			}
		}
		sort.Strings(repos)
		for _, name := range repos {
			found = true
			reason, err := predecessorWait(ctx, store, workspace, name, changes[name])
			if err != nil {
				return stackpublish.DependencyStatus{}, false, err
			}
			if reason != "" {
				waiting = append(waiting, reason)
			}
		}
	}
	if !found {
		return stackpublish.DependencyStatus{}, false, nil
	}
	if len(waiting) == 0 {
		return stackpublish.DependencyStatus{State: "success", Description: "All cross-repo predecessors landed"}, true, nil
	}
	return stackpublish.DependencyStatus{State: "pending", Description: "Waiting for " + strings.Join(waiting, "; ")}, true, nil
}

// predecessorWait names why a predecessor change has not landed, or "" once it has.
func predecessorWait(ctx context.Context, store DependencyStore, workspace, repo, change string) (string, error) {
	status, err := store.LandingStatus(ctx, workspace, change)
	if err != nil || status.State == "landed" {
		return "", err
	}
	publication, published, err := store.Publication(ctx, workspace, change)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s change %s", repo, change)
	if published && publication.PRNumber > 0 {
		name = fmt.Sprintf("%s#%d", publication.Slug, publication.PRNumber)
	}
	abandoned, err := store.ChangeAbandoned(ctx, workspace, change)
	if err != nil {
		return "", err
	}
	switch {
	case abandoned || status.State == "closed" || status.State == "dependency_abandoned":
		return name + " (closed unmerged: dependency_abandoned)", nil
	case !published || publication.PRNumber == 0:
		return name + " (not published)", nil
	default:
		return name + " to land", nil
	}
}

// DependencyForge posts loom/dependencies and reads whether a repo requires it.
type DependencyForge interface {
	PostDependencyStatus(context.Context, string, string, string, stackpublish.DependencyStatus) (int64, error)
	MergeQueueHead(context.Context, string, string, int) (string, error)
	DependencyEnforcement(context.Context, string, string, string, int64) (string, error)
}

type dependencyJournal interface {
	DependencyStore
	DependencyChecks(context.Context) ([]journal.DependencyCheck, []journal.DependencyEnforcement, error)
	RecordDependencyCheck(context.Context, journal.DependencyCheck) error
	DependencyPosted(context.Context, string, string, string, string, string) (bool, error)
	RecordDependencyPost(context.Context, string, string, string, string, string) error
	RecordDependencyEnforcement(context.Context, journal.DependencyEnforcement) error
	RecordDependencyApp(context.Context, string, int64) error
	DependencyApp(context.Context, string) (int64, error)
}

// reconcileDependencies keeps loom/dependencies current on every open PR whose
// change waits for other repositories, including GitHub merge-queue commits.
// A failed post is not recorded, so the next pass retries it; success is only
// posted from a fresh evaluation.
func reconcileDependencies(ctx context.Context, store Store, forge Forge, publications []journal.Publication, predecessors Predecessors) error {
	deps, ok := store.(dependencyJournal)
	if !ok {
		return errors.New("store cannot record dependency checks")
	}
	poster, ok := forge.(DependencyForge)
	if !ok {
		return errors.New("forge cannot post loom/dependencies")
	}
	checked, err := checkedChanges(ctx, deps)
	if err != nil {
		return err
	}
	var failures []error
	enforcement := map[string]bool{}
	for _, publication := range publications {
		landing, err := store.LandingStatus(ctx, publication.Workspace, publication.Change)
		if err != nil {
			return err
		}
		switch landing.State {
		case "landed", "merged", "closed", "dependency_abandoned":
			continue
		}
		status, found, err := CrossRepoDependencies(ctx, deps, publication.Workspace, publication.Change, predecessors)
		if err != nil {
			failures = append(failures, fmt.Errorf("evaluate dependencies for %s: %w", publication.Change, err))
			continue
		}
		if !found {
			if !checked[publication.Workspace+"\x00"+publication.Change] {
				continue
			}
			status = stackpublish.DependencyStatus{State: "success", Description: "No cross-repo predecessors"}
		}
		if err := deps.RecordDependencyCheck(ctx, journal.DependencyCheck{Workspace: publication.Workspace,
			Change: publication.Change, Repo: publication.Slug, State: status.State, Reason: status.Description}); err != nil {
			return err
		}
		failures = append(failures, postDependencyStatus(ctx, store, deps, poster, publication, status)...)
		trunk := targetTrunk(publication, publications)
		if key := publication.Slug + "\x00" + trunk; !enforcement[key] {
			enforcement[key] = true
			if err := recordEnforcement(ctx, deps, poster, publication.Slug, trunk); err != nil {
				return err
			}
		}
	}
	return errors.Join(failures...)
}

// checkedChanges lists changes that already carry loom/dependencies, so one
// whose cross-repo predecessors were removed is released rather than left pending.
func checkedChanges(ctx context.Context, deps dependencyJournal) (map[string]bool, error) {
	checks, _, err := deps.DependencyChecks(ctx)
	checked := map[string]bool{}
	for _, check := range checks {
		checked[check.Workspace+"\x00"+check.Change] = true
	}
	return checked, err
}

func postDependencyStatus(ctx context.Context, store Store, deps dependencyJournal, forge DependencyForge, publication journal.Publication, status stackpublish.DependencyStatus) []error {
	owner, repo, _ := strings.Cut(publication.Slug, "/")
	head := publication.Head
	if observation, found, err := store.ProviderObservation(ctx, publication.Workspace, publication.Change); err != nil {
		return []error{err}
	} else if found && observation.HeadSHA != "" {
		head = observation.HeadSHA
	}
	shas := []string{head}
	queued, err := forge.MergeQueueHead(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return []error{fmt.Errorf("read merge queue for %s#%d: %w", publication.Slug, publication.PRNumber, err)}
	}
	if queued != "" && queued != head {
		shas = append(shas, queued)
	}
	var failures []error
	for _, sha := range shas {
		posted, err := deps.DependencyPosted(ctx, publication.Workspace, publication.Change, sha, status.State, status.Description)
		if err != nil || posted {
			failures = append(failures, err)
			continue
		}
		app, err := forge.PostDependencyStatus(ctx, owner, repo, sha, status)
		if err != nil {
			failures = append(failures, fmt.Errorf("post loom/dependencies on %s@%s: %w", publication.Slug, sha, err))
			continue
		}
		if app > 0 {
			failures = append(failures, deps.RecordDependencyApp(ctx, publication.Slug, app))
		}
		failures = append(failures, deps.RecordDependencyPost(ctx, publication.Workspace, publication.Change, sha, status.State, status.Description))
	}
	return failures
}

func recordEnforcement(ctx context.Context, deps dependencyJournal, forge DependencyForge, slug, branch string) error {
	owner, repo, _ := strings.Cut(slug, "/")
	app, err := deps.DependencyApp(ctx, slug)
	if err != nil {
		return err
	}
	state, err := forge.DependencyEnforcement(ctx, owner, repo, branch, app)
	reason := map[string]string{
		"enforced":     "branch protection requires loom/dependencies from the app Loom posts as",
		"wrong_app":    "loom/dependencies is pinned to an app Loom does not post as: Loom's result cannot satisfy it",
		"not_pinned":   "loom/dependencies is required but not pinned to the Loom app: any collaborator could post it",
		"not_enforced": "not enforced: branch protection does not require loom/dependencies, so only Loom's own merges wait",
	}[state]
	if err != nil {
		state, reason = "unknown", "could not read branch protection: "+err.Error()
	}
	return deps.RecordDependencyEnforcement(ctx, journal.DependencyEnforcement{Repo: slug, Branch: branch, State: state, Reason: reason})
}
