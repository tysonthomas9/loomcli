package landing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type Store interface {
	loomgit.RevisionStore
	PublishedChanges(context.Context) ([]journal.Publication, error)
	LatestReadyRevision(context.Context, string, string) (int, string, error)
	ProviderObservation(context.Context, string, string) (journal.ProviderObservation, bool, error)
	RecordProviderObservation(context.Context, journal.ProviderObservation) error
	LandingStatus(context.Context, string, string) (journal.LandingStatus, error)
	MarkMerged(context.Context, string, string) error
	MarkLanded(context.Context, string, string, ...string) error
	ChangeForTask(context.Context, string, string, string) (string, error)
	SourceRevision(context.Context, string, string) (int, error)
	OfferRestack(context.Context, journal.RestackOffer) error
	OpenRestackOffers(context.Context) ([]journal.RestackOffer, error)
	CompleteRestackOffer(context.Context, journal.RestackOffer, int) error
	RecordLandingAttention(context.Context, string, string, string) (bool, error)
	ClearLandingAttention(context.Context, string, string) (bool, error)
}

// errPublicationMismatch marks a published change whose owned PR no longer
// matches it, and errTrunkUnavailable one whose repository trunk could not be
// fetched. Only those changes wait; reconcile continues for the rest.
var (
	errPublicationMismatch = errors.New("publication mismatch")
	errTrunkUnavailable    = errors.New("trunk unavailable")
)

type Dependent struct {
	Task, Repo string
}

func LocalDependents(ctx context.Context, workspace, change string) ([]Dependent, error) {
	lineages, err := taskcopy.DependentsOf(ctx, workspace, change)
	if err != nil {
		return nil, err
	}
	dependents := make([]Dependent, 0, len(lineages))
	for _, lineage := range lineages {
		dependents = append(dependents, Dependent{Task: lineage.Task, Repo: lineage.Repo})
	}
	return dependents, nil
}

type Options struct {
	Dependents func(context.Context, string, string) ([]Dependent, error)
	// Predecessors enables loom/dependencies for changes whose task waits for
	// changes in other repositories.
	Predecessors Predecessors
	Restack      func(context.Context, journal.RestackOffer, Forge) (int, error)
	Forge        Forge
}

type Forge interface {
	PullByNumber(context.Context, string, string, int) (stackpublish.PR, error)
	PullsForCommit(context.Context, string, string, string) ([]stackpublish.PR, error)
}

type fetchedPublication struct {
	publication journal.Publication
	trunkSHA    string
	trunkRef    string
	runner      *gitexec.Runner
	// err is set when the trunk could not be fetched; landing is not decided
	// without a fresh trunk.
	err error
}

func RunOnce(ctx context.Context) error {
	return RunOnceWithOptions(ctx, Options{})
}

func RunOnceWithOptions(ctx context.Context, options Options) error {
	return RunAtWithOptions(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), options.Forge, "", options)
}

func Status(ctx context.Context, workspace, change string) (journal.LandingStatus, error) {
	store, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return journal.LandingStatus{}, err
	}
	defer func() { _ = store.Close() }()
	return store.LandingStatus(ctx, workspace, change)
}

func RunAt(ctx context.Context, path string, forge Forge, token string) error {
	return RunAtWithOptions(ctx, path, forge, token, Options{})
}

func RunAtWithOptions(ctx context.Context, path string, forge Forge, token string, options Options) error {
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if forge == nil {
		if token == "" {
			token = githubtoken.GitHub(ctx)
		}
		forge = stackpublish.NewConfiguredGitHubForge(token)
	}
	return ReconcileWithOptions(ctx, store, forge, options)
}

func Reconcile(ctx context.Context, store Store, forge Forge) error {
	return ReconcileWithOptions(ctx, store, forge, Options{})
}

func ReconcileWithOptions(ctx context.Context, store Store, forge Forge, options Options) error {
	publications, err := store.PublishedChanges(ctx)
	if err != nil {
		return err
	}
	if len(publications) > 0 && (options.Dependents == nil || options.Restack == nil) {
		slog.Warn("landing dependent work skipped: adapters not configured",
			"dependents_configured", options.Dependents != nil, "restack_configured", options.Restack != nil)
	}
	fetched, err := fetchPublications(ctx, store, publications, options.Dependents != nil)
	if err != nil {
		return err
	}
	// One change's failure must not stall landing for every other workspace.
	var failures []error
	for _, item := range fetched {
		err := item.err
		if err == nil {
			err = reconcilePublication(ctx, store, forge, item, publications, options)
		}
		if errors.Is(err, errPublicationMismatch) || errors.Is(err, errTrunkUnavailable) {
			err = recordAttention(ctx, store, item.publication, err)
		} else if err == nil {
			err = clearAttention(ctx, store, item.publication)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("landing %s/%s: %w", item.publication.Workspace, item.publication.Change, err))
		}
	}
	if err := propagateClosure(ctx, store, publications); err != nil {
		return errors.Join(append(failures, err)...)
	}
	if options.Predecessors != nil {
		failures = append(failures, reconcileDependencies(ctx, store, forge, publications, options.Predecessors))
	}
	if options.Restack != nil {
		failures = append(failures, runRestacks(ctx, store, forge, options.Restack))
	}
	return errors.Join(failures...)
}

func reconcilePublication(ctx context.Context, store Store, forge Forge, item fetchedPublication, publications []journal.Publication, options Options) error {
	status, err := store.LandingStatus(ctx, item.publication.Workspace, item.publication.Change)
	if err != nil {
		return err
	}
	if status.State != "landed" && status.State != "dependency_abandoned" {
		if err := detect(ctx, store, forge, item, publications); err != nil {
			return err
		}
	}
	status, err = store.LandingStatus(ctx, item.publication.Workspace, item.publication.Change)
	if err != nil {
		return err
	}
	if status.State == "landed" {
		return offerDependents(ctx, store, item, options)
	}
	return nil
}

// recordAttention keeps a change that needs repair visible in status and
// doctor, logging only when its reason changes.
func recordAttention(ctx context.Context, store Store, publication journal.Publication, reason error) error {
	recorded, err := store.RecordLandingAttention(ctx, publication.Workspace, publication.Change, reason.Error())
	if err != nil || !recorded {
		return err
	}
	slog.Warn("landing needs attention", "workspace", publication.Workspace, "change", publication.Change,
		"pr", publication.PRNumber, "err", reason)
	return nil
}

func clearAttention(ctx context.Context, store Store, publication journal.Publication) error {
	cleared, err := store.ClearLandingAttention(ctx, publication.Workspace, publication.Change)
	if err == nil && cleared {
		slog.Info("landing attention cleared", "workspace", publication.Workspace, "change", publication.Change, "pr", publication.PRNumber)
	}
	return err
}

func fetchPublications(ctx context.Context, store Store, publications []journal.Publication, includeLanded bool) ([]fetchedPublication, error) {
	fetched := make([]fetchedPublication, 0, len(publications))
	byTrunk := make(map[string]fetchedPublication)
	for _, publication := range publications {
		status, err := store.LandingStatus(ctx, publication.Workspace, publication.Change)
		if err != nil {
			return nil, err
		}
		if status.State == "landed" && !includeLanded {
			continue
		}
		trunk := targetTrunk(publication, publications)
		key := publication.Repo + "\x00" + trunk
		if existing, ok := byTrunk[key]; ok {
			existing.publication = publication
			fetched = append(fetched, existing)
			continue
		}
		item := fetchTrunk(ctx, publication, trunk)
		byTrunk[key] = item
		fetched = append(fetched, item)
	}
	return fetched, nil
}

// fetchTrunk fetches one repository trunk. A failure only holds back the
// changes that land on it.
func fetchTrunk(ctx context.Context, publication journal.Publication, trunk string) fetchedPublication {
	item := fetchedPublication{publication: publication}
	runner, err := gitexec.New(publication.Repo, gitexec.Options{
		FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"},
	})
	if err != nil {
		item.err = fmt.Errorf("%w: open %s: %w", errTrunkUnavailable, publication.Repo, err)
		return item
	}
	if _, err := runner.Run(ctx, "fetch", "origin", trunk); err != nil {
		item.err = fmt.Errorf("%w: fetch %s in %s: %w", errTrunkUnavailable, trunk, publication.Repo, err)
		return item
	}
	item.trunkRef = "origin/" + trunk
	out, err := runner.Run(ctx, "rev-parse", "--verify", item.trunkRef+"^{commit}")
	if err != nil {
		item.err = fmt.Errorf("%w: resolve fetched %s in %s: %w", errTrunkUnavailable, trunk, publication.Repo, err)
		return item
	}
	item.trunkSHA, item.runner = strings.TrimSpace(string(out)), runner
	return item
}

// targetTrunk follows a stacked PR's predecessors to the bottom layer's trunk,
// where provider merges actually land.
func targetTrunk(publication journal.Publication, all []journal.Publication) string {
	for range all {
		predecessor, found := stackPredecessor(publication, all)
		if !found {
			break
		}
		publication = predecessor
	}
	return publication.Trunk
}

func detect(ctx context.Context, store Store, forge Forge, item fetchedPublication, publications []journal.Publication) error {
	publication := item.publication
	pull, err := ownedPull(ctx, forge, publication)
	if err != nil {
		return err
	}
	if err := observeProvider(ctx, store, forge, item, pull, publications); err != nil {
		return err
	}
	if !pull.Merged {
		return nil
	}
	if pull.MergeCommitSHA != "" {
		if _, err := item.runner.Run(ctx, "merge-base", "--is-ancestor", pull.MergeCommitSHA, item.trunkSHA); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 128) {
				return store.MarkMerged(ctx, publication.Workspace, publication.Change)
			}
			return err
		}
		return store.MarkLanded(ctx, publication.Workspace, publication.Change, "merge_commit")
	}
	return detectAssociatedCommit(ctx, store, forge, item)
}

// splitSlug splits a slug at its first slash. The repo part may hold more
// segments: GitLab projects live in nested groups (group/subgroup/project).
func splitSlug(slug string) (string, string, error) {
	for _, part := range strings.Split(slug, "/") {
		if part == "" {
			return "", "", errors.New("published repository slug is invalid")
		}
	}
	owner, repo, ok := strings.Cut(slug, "/")
	if !ok {
		return "", "", errors.New("published repository slug is invalid")
	}
	return owner, repo, nil
}

func ownedPull(ctx context.Context, forge Forge, publication journal.Publication) (stackpublish.PR, error) {
	owner, repo, err := splitSlug(publication.Slug)
	if err != nil {
		return stackpublish.PR{}, err
	}
	pull, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil {
		return stackpublish.PR{}, err
	}
	if pull.Number != publication.PRNumber || pull.Head != publication.Branch {
		return stackpublish.PR{}, fmt.Errorf("%w: owned PR %d does not match published change %s", errPublicationMismatch, publication.PRNumber, publication.Change)
	}
	return pull, nil
}

func detectAssociatedCommit(ctx context.Context, store Store, forge Forge, item fetchedPublication) error {
	publication := item.publication
	owner, repo, err := splitSlug(publication.Slug)
	if err != nil {
		return err
	}
	commits, err := item.runner.Run(ctx, "log", "--format=%H", "--fixed-strings", "--grep=Loom-Change-Id: "+publication.Change, item.trunkRef)
	if err != nil {
		return err
	}
	for _, commit := range strings.Fields(string(commits)) {
		message, err := item.runner.Run(ctx, "show", "-s", "--format=%B", commit)
		if err != nil {
			return err
		}
		if !hasChangeID(string(message), publication.Change) {
			continue
		}
		associated, err := forge.PullsForCommit(ctx, owner, repo, commit)
		if err != nil {
			return err
		}
		for _, candidate := range associated {
			if candidate.Number == publication.PRNumber {
				return store.MarkLanded(ctx, publication.Workspace, publication.Change, "associated_commit")
			}
		}
	}
	return store.MarkMerged(ctx, publication.Workspace, publication.Change)
}

func hasChangeID(message, change string) bool {
	for _, line := range strings.Split(message, "\n") {
		if strings.TrimSpace(line) == "Loom-Change-Id: "+change {
			return true
		}
	}
	return false
}

func offerDependents(ctx context.Context, store Store, item fetchedPublication, options Options) error {
	if options.Dependents == nil {
		return nil
	}
	publication := item.publication
	dependents, err := options.Dependents(ctx, publication.Workspace, publication.Change)
	if err != nil {
		return err
	}
	for _, dependent := range dependents {
		change, err := store.ChangeForTask(ctx, publication.Workspace, dependent.Task, dependent.Repo)
		if errors.Is(err, journal.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		revision, err := store.SourceRevision(ctx, publication.Workspace, change)
		if errors.Is(err, journal.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := store.OfferRestack(ctx, journal.RestackOffer{Workspace: publication.Workspace,
			Change: change, Predecessor: publication.Change, Task: dependent.Task, Repo: dependent.Repo,
			Revision: revision, TrunkSHA: item.trunkSHA}); err != nil {
			return err
		}
	}
	return nil
}

func runRestacks(ctx context.Context, store Store, forge Forge, restack func(context.Context, journal.RestackOffer, Forge) (int, error)) error {
	offers, err := store.OpenRestackOffers(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, offer := range offers {
		derivedRevision, err := restack(ctx, offer, forge)
		if err == nil {
			err = store.CompleteRestackOffer(ctx, offer, derivedRevision)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("restack %s/%s: %w", offer.Workspace, offer.Change, err))
		}
	}
	return errors.Join(failures...)
}
