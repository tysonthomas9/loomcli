package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func adoptNativeRestack(ctx context.Context, store *journal.SQLite, offer journal.RestackOffer,
	publication journal.Publication, forge landing.Forge, revision *int) error {
	area, err := store.WorkingAreaForAppliedChange(ctx, offer.Workspace, offer.Change, offer.Repo)
	if err != nil {
		return err
	}
	runner, err := gitexec.New(area.Path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	layers, err := apply.New(store, nil, runner).AppliedLog(ctx, offer.Workspace, area.Lead)
	if err != nil {
		return err
	}
	publications, heads, err := nativeRestackHeads(ctx, store, runner, offer, publication, forge, layers)
	if err != nil {
		return err
	}
	*revision, err = store.SourceRevision(ctx, offer.Workspace, offer.Change)
	if err != nil {
		return err
	}
	if err := recheckNativeHeads(ctx, forge, publication.Slug, publications, heads); err != nil {
		return err
	}
	if *revision <= offer.Revision {
		result, restackErr := pull.AdoptRestackLocal(ctx, area.Path, offer.TrunkSHA, nil, heads,
			"landing-restack:"+offer.Workspace+":"+offer.Change+":"+offer.Predecessor)
		if restackErr != nil {
			return recordRestackError(ctx, store, offer, publication.StackID, result.Paths, restackErr)
		}
		*revision, err = store.SourceRevision(ctx, offer.Workspace, offer.Change)
		if err != nil {
			return err
		}
	} else if err := verifyNativeAdoption(ctx, store, runner, offer, *revision, heads, publications); err != nil {
		return err
	}
	if err := updateNativePublicationRefs(ctx, runner, publications, heads); err != nil {
		return err
	}
	if err := store.AdoptStackPublications(ctx, publications, heads); err != nil {
		return err
	}
	if err := store.ClearStackAttention(ctx, offer.Workspace, publication.StackID); err != nil {
		return err
	}
	return nil
}

func verifyNativeAdoption(ctx context.Context, store *journal.SQLite, runner *gitexec.Runner,
	offer journal.RestackOffer, revision int, heads map[string]string, publications []journal.Publication) error {
	derived, err := store.GetRevision(ctx, offer.Workspace, offer.Change, revision)
	if err != nil {
		return err
	}
	leaf := heads[publications[len(publications)-1].Change]
	current, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if derived.Operation != "restack" || derived.HeadSHA != heads[offer.Change] || strings.TrimSpace(string(current)) != leaf {
		return loomgit.NewError(loomgit.Stale, "native restack retry differs from the installed leaf", nil)
	}
	return nil
}

func nativeRestackHeads(ctx context.Context, store *journal.SQLite, runner *gitexec.Runner,
	offer journal.RestackOffer, publication journal.Publication, forge landing.Forge,
	layers []loomgit.AppliedLayer) ([]journal.Publication, map[string]string, error) {
	parts := strings.Split(publication.Slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil, errors.New("native stack repository slug is invalid")
	}
	publications := make([]journal.Publication, 0, len(layers))
	heads := make(map[string]string, len(layers))
	baseBranch, err := nativeTrunkBranch(ctx, store, offer.Workspace, offer.Repo)
	if err != nil {
		return nil, nil, err
	}
	for _, layer := range layers {
		landed, err := store.IsLanded(ctx, offer.Workspace, layer.Change)
		if err != nil {
			return nil, nil, err
		}
		if landed {
			continue
		}
		item, found, err := store.Publication(ctx, offer.Workspace, layer.Change)
		if err != nil || !found || item.StackID != publication.StackID || item.Phase != "done" {
			return nil, nil, loomgit.NewError(loomgit.AttentionRequired, "native stack publication is incomplete", err)
		}
		pr, err := forge.PullByNumber(ctx, parts[0], parts[1], item.PRNumber)
		if err != nil {
			return nil, nil, err
		}
		if !nativePROpenOn(pr, item.Branch, baseBranch) {
			return nil, nil, loomgit.NewError(loomgit.Stale, "native PR lineage or head is not ready", nil)
		}
		if pr.HeadSHA == "" {
			return nil, nil, loomgit.NewError(loomgit.AttentionRequired, "native PR head SHA is unavailable", nil)
		}
		if err := fetchNativeHead(ctx, runner, pr); err != nil {
			return nil, nil, err
		}
		item.Trunk = baseBranch
		publications = append(publications, item)
		heads[layer.Change] = pr.HeadSHA
		baseBranch = item.Branch
	}
	if len(publications) == 0 {
		return nil, nil, errors.New("native stack has no remaining layers")
	}
	return publications, heads, nil
}

// recheckNativeHeads re-reads every PR just before adoption writes anything and
// fails closed when its head SHA, head branch, base or open state moved since it was read.
func recheckNativeHeads(ctx context.Context, forge landing.Forge, slug string,
	publications []journal.Publication, heads map[string]string) error {
	owner, repo, _ := strings.Cut(slug, "/")
	for _, item := range publications {
		pr, err := forge.PullByNumber(ctx, owner, repo, item.PRNumber)
		if err != nil {
			return err
		}
		if !nativePROpenOn(pr, item.Branch, item.Trunk) || pr.HeadSHA != heads[item.Change] {
			return loomgit.NewError(loomgit.Stale, "native PR changed before adoption", nil)
		}
	}
	return nil
}

func nativePROpenOn(pr stackpublish.PR, head, base string) bool {
	return !pr.Merged && pr.State == "open" && pr.Head == head && pr.Base == base
}

func nativeTrunkBranch(ctx context.Context, store *journal.SQLite, workspace, repo string) (string, error) {
	repos, err := store.WorkspaceRepos(ctx, workspace)
	if err != nil {
		return "", err
	}
	for _, item := range repos {
		if item.Repo == repo && item.Trunk != "" {
			return item.Trunk, nil
		}
	}
	return "", loomgit.NewError(loomgit.Stale, "native stack trunk is unavailable", nil)
}

func fetchNativeHead(ctx context.Context, runner *gitexec.Runner, pr stackpublish.PR) error {
	if _, err := runner.Run(ctx, "fetch", "origin", "refs/heads/"+pr.Head); err != nil {
		return fmt.Errorf("fetch native PR head: %w", err)
	}
	fetched, err := runner.Run(ctx, "rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(fetched)) != pr.HeadSHA {
		return loomgit.NewError(loomgit.Stale, "native PR head differs from fetched branch", nil)
	}
	return nil
}

func updateNativePublicationRefs(ctx context.Context, runner *gitexec.Runner,
	publications []journal.Publication, heads map[string]string) error {
	var input strings.Builder
	input.WriteString("start\n")
	for _, item := range publications {
		ref, err := refname.Publication(item.Workspace, item.Change)
		if err != nil {
			return err
		}
		current, err := runner.Run(ctx, "rev-parse", "--verify", ref)
		if err != nil {
			return err
		}
		old := strings.TrimSpace(string(current))
		if old == heads[item.Change] {
			continue
		}
		if old != item.Head {
			return loomgit.NewError(loomgit.Stale, "native publication ref changed unexpectedly", nil)
		}
		fmt.Fprintf(&input, "update %s %s %s\n", ref, heads[item.Change], old)
	}
	input.WriteString("prepare\ncommit\n")
	if _, err := runner.RunWithInput(ctx, []byte(input.String()), nil, "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("adopt native publication refs: %w", err)
	}
	return nil
}
