package landing

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type Store interface {
	PublishedChanges(context.Context) ([]journal.Publication, error)
	LandingStatus(context.Context, string, string) (journal.LandingStatus, error)
	MarkMerged(context.Context, string, string) error
	MarkLanded(context.Context, string, string, ...string) error
	ChangeForTask(context.Context, string, string, string) (string, error)
	SourceRevision(context.Context, string, string) (int, error)
	OfferRestack(context.Context, journal.RestackOffer) error
	OpenRestackOffers(context.Context) ([]journal.RestackOffer, error)
	CompleteRestackOffer(context.Context, journal.RestackOffer, int) error
}

type Dependent struct {
	Task, Repo string
}

type Options struct {
	Dependents func(context.Context, string, string) ([]Dependent, error)
	Restack    func(context.Context, journal.RestackOffer) (int, error)
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
}

func RunOnce(ctx context.Context) error {
	return RunOnceWithOptions(ctx, Options{})
}

func RunOnceWithOptions(ctx context.Context, options Options) error {
	return RunAtWithOptions(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), nil, "", options)
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
		forge = stackpublish.NewGitHubForge(token, nil, "")
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
	fetched, err := fetchPublications(ctx, store, publications, options.Dependents != nil)
	if err != nil {
		return err
	}
	for _, item := range fetched {
		status, err := store.LandingStatus(ctx, item.publication.Workspace, item.publication.Change)
		if err != nil {
			return err
		}
		if status.State != "landed" {
			if err := detect(ctx, store, forge, item); err != nil {
				return err
			}
		}
		status, err = store.LandingStatus(ctx, item.publication.Workspace, item.publication.Change)
		if err != nil {
			return err
		}
		if status.State == "landed" {
			if err := offerDependents(ctx, store, item, options); err != nil {
				return err
			}
		}
	}
	if options.Restack != nil {
		return runRestacks(ctx, store, options.Restack)
	}
	return nil
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
		key := publication.Repo + "\x00" + publication.Trunk
		if existing, ok := byTrunk[key]; ok {
			existing.publication = publication
			fetched = append(fetched, existing)
			continue
		}
		runner, err := gitexec.New(publication.Repo, gitexec.Options{
			FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"},
		})
		if err != nil {
			return nil, err
		}
		if _, err := runner.Run(ctx, "fetch", "origin", publication.Trunk); err != nil {
			return nil, fmt.Errorf("fetch trunk for %s: %w", publication.Change, err)
		}
		trunkRef := "origin/" + publication.Trunk
		out, err := runner.Run(ctx, "rev-parse", "--verify", trunkRef+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("resolve fetched trunk for %s: %w", publication.Change, err)
		}
		item := fetchedPublication{publication: publication,
			trunkSHA: strings.TrimSpace(string(out)), trunkRef: trunkRef, runner: runner}
		byTrunk[key] = item
		fetched = append(fetched, item)
	}
	return fetched, nil
}

func detect(ctx context.Context, store Store, forge Forge, item fetchedPublication) error {
	publication := item.publication
	parts := strings.Split(publication.Slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("published repository slug is invalid")
	}
	pull, err := forge.PullByNumber(ctx, parts[0], parts[1], publication.PRNumber)
	if err != nil {
		return err
	}
	if pull.Number != publication.PRNumber || pull.Head != publication.Branch {
		return fmt.Errorf("owned PR %d does not match published change %s", publication.PRNumber, publication.Change)
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
		associated, err := forge.PullsForCommit(ctx, parts[0], parts[1], commit)
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

func runRestacks(ctx context.Context, store Store, restack func(context.Context, journal.RestackOffer) (int, error)) error {
	offers, err := store.OpenRestackOffers(ctx)
	if err != nil {
		return err
	}
	for _, offer := range offers {
		derivedRevision, err := restack(ctx, offer)
		if err != nil {
			return err
		}
		if err := store.CompleteRestackOffer(ctx, offer, derivedRevision); err != nil {
			return err
		}
	}
	return nil
}
