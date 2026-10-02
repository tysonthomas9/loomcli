package landing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func expectedBase(ctx context.Context, store Store, publication journal.Publication, all []journal.Publication) (string, error) {
	predecessor, found := stackPredecessor(publication, all)
	if publication.StackID == "" || !found {
		return publication.Trunk, nil
	}
	status, err := store.LandingStatus(ctx, predecessor.Workspace, predecessor.Change)
	if err != nil {
		return "", err
	}
	if status.State != "landed" {
		return predecessor.Branch, nil
	}
	return targetTrunk(publication, all), nil
}

func observeProvider(ctx context.Context, store Store, forge Forge, item fetchedPublication, pull stackpublish.PR, all []journal.Publication) error {
	publication := item.publication
	if pull.HeadSHA == "" || pull.Base == "" {
		// GitHub omits these while it is still restacking a native stack. Record
		// nothing yet; merge detection and the restack offer handle the PR.
		return nil
	}
	previous, found, err := store.ProviderObservation(ctx, publication.Workspace, publication.Change)
	if err != nil {
		return err
	}
	base, err := expectedBase(ctx, store, publication, all)
	if err != nil {
		return err
	}
	state := "open"
	if pull.State == "closed" && !pull.Merged {
		state = "closed"
	} else if pull.Merged {
		state = "merged"
	} else if pull.Base != base || pull.HeadSHA != publication.Head && (!found || previous.HeadSHA != pull.HeadSHA) {
		state, err = providerMove(ctx, store, forge, item, pull, base, all)
		if err != nil {
			return err
		}
	} else if found && previous.HeadSHA == pull.HeadSHA && previous.Base == pull.Base {
		state = previous.State
	}
	return store.RecordProviderObservation(ctx, journal.ProviderObservation{Workspace: publication.Workspace,
		Change: publication.Change, Base: pull.Base, HeadSHA: pull.HeadSHA, State: state})
}

// providerMove classifies a provider-side base or head change. GitHub restacks
// a native stack itself after a lower layer merges; Loom's restack offer adopts
// those heads, so the move is neither drift nor a provider restack revision.
func providerMove(ctx context.Context, store Store, forge Forge, item fetchedPublication,
	pull stackpublish.PR, base string, all []journal.Publication) (string, error) {
	native, err := nativeRestackPending(ctx, store, forge, item.publication, all)
	if err != nil {
		return "", err
	}
	if native {
		shaped, err := nativeShaped(ctx, store, forge, item, pull, base, all)
		if err != nil {
			return "", err
		}
		if shaped {
			return "native_restack", nil
		}
		return "diverged", nil
	}
	if pull.Base != base {
		return "diverged", nil
	}
	return reconcileHead(ctx, store, item, pull)
}

type stackBackends interface {
	StackBackend(context.Context, string, string) (string, error)
}

func nativeRestackPending(ctx context.Context, store Store, forge Forge, publication journal.Publication, all []journal.Publication) (bool, error) {
	backends, ok := store.(stackBackends)
	if !ok || publication.StackID == "" {
		return false, nil
	}
	backend, err := backends.StackBackend(ctx, publication.Workspace, publication.StackID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && backend != "native" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current := publication
	for range all {
		predecessor, found := stackPredecessor(current, all)
		if !found {
			return false, nil
		}
		merged, err := layerMerged(ctx, store, forge, predecessor)
		if err != nil || merged {
			return merged, err
		}
		current = predecessor
	}
	return false, nil
}

// stackPredecessor finds the layer below by its recorded branch first: native
// adoption rewrites heads but not prior_sha, so Prior can name a stale head.
func stackPredecessor(publication journal.Publication, all []journal.Publication) (journal.Publication, bool) {
	for _, byBranch := range []bool{true, false} {
		for _, candidate := range all {
			if candidate.Workspace != publication.Workspace || candidate.StackID != publication.StackID ||
				candidate.Change == publication.Change {
				continue
			}
			if byBranch && candidate.Branch == publication.Trunk ||
				!byBranch && publication.Prior != "" && candidate.Head == publication.Prior {
				return candidate, true
			}
		}
	}
	return journal.Publication{}, false
}

func layerMerged(ctx context.Context, store Store, forge Forge, publication journal.Publication) (bool, error) {
	status, err := store.LandingStatus(ctx, publication.Workspace, publication.Change)
	if err != nil {
		return false, err
	}
	if status.State == "landed" || status.State == "merged" {
		return true, nil
	}
	pull, err := ownedPull(ctx, forge, publication)
	if err != nil {
		return false, fmt.Errorf("blocked by the layer below: %w", err)
	}
	return pull.Merged, nil
}

// nativeShaped reports whether a provider move is GitHub restacking a native
// stack rather than a foreign retarget or push. The PR keeps its expected base,
// or moves to trunk once the layer directly below merged. A moved head must be
// rebuilt on a base tip the recorded head did not already contain, and carry
// exactly the recorded layer's patches.
func nativeShaped(ctx context.Context, store Store, forge Forge, item fetchedPublication,
	pull stackpublish.PR, base string, all []journal.Publication) (bool, error) {
	publication := item.publication
	predecessor, found := stackPredecessor(publication, all)
	if !found {
		return false, nil
	}
	if pull.Base != base {
		if pull.Base != targetTrunk(publication, all) {
			return false, nil
		}
		if merged, err := layerMerged(ctx, store, forge, predecessor); err != nil || !merged {
			return false, err
		}
	}
	if pull.HeadSHA == publication.Head {
		return true, nil
	}
	tip, err := fetchTip(ctx, item.runner, pull.Base)
	if err != nil {
		return false, err
	}
	if head, err := fetchTip(ctx, item.runner, pull.Head); err != nil || head != pull.HeadSHA {
		return false, fmt.Errorf("provider head changed during fetch: %w", err)
	}
	if onTip, err := isAncestor(ctx, item.runner, tip, pull.HeadSHA); err != nil || !onTip {
		return false, err
	}
	if stale, err := isAncestor(ctx, item.runner, tip, publication.Head); err != nil || stale {
		return false, err
	}
	return review.PatchesMatch(ctx, item.runner, loomgit.Revision{BaseSHA: predecessor.Head, HeadSHA: publication.Head},
		loomgit.Revision{BaseSHA: tip, HeadSHA: pull.HeadSHA})
}

func fetchTip(ctx context.Context, runner *gitexec.Runner, branch string) (string, error) {
	if _, err := runner.Run(ctx, "fetch", "origin", branch); err != nil {
		return "", err
	}
	tip, err := runner.Run(ctx, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	return strings.TrimSpace(string(tip)), err
}

func isAncestor(ctx context.Context, runner *gitexec.Runner, ancestor, head string) (bool, error) {
	_, err := runner.Run(ctx, "merge-base", "--is-ancestor", ancestor, head)
	var exit *exec.ExitError
	if errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 128) {
		return false, nil
	}
	return err == nil, err
}

func reconcileHead(ctx context.Context, store Store, item fetchedPublication, pull stackpublish.PR) (string, error) {
	publication := item.publication
	if _, err := item.runner.Run(ctx, "fetch", "origin", pull.Head); err != nil {
		return "", err
	}
	got, err := item.runner.Run(ctx, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil || strings.TrimSpace(string(got)) != pull.HeadSHA {
		return "", fmt.Errorf("provider head changed during fetch: %w", err)
	}
	base, err := providerBase(ctx, item.runner, pull.Base, pull.HeadSHA)
	if err != nil {
		return "", err
	}
	number, _, err := store.LatestReadyRevision(ctx, publication.Workspace, publication.Change)
	if err != nil {
		return "", err
	}
	source, err := store.GetRevision(ctx, publication.Workspace, publication.Change, number)
	if err != nil {
		return "", err
	}
	if base == source.BaseSHA {
		return "diverged", nil
	}
	derived, err := changeset.RecordDerived(ctx, store, item.runner, changeset.DerivedInput{
		Workspace: publication.Workspace, Change: publication.Change, RequestID: "provider-restack:" + publication.Workspace + ":" + publication.Change + ":" + pull.HeadSHA,
		FromNumber: source.Number, Operation: "provider_restack", Outcome: "completed", BaseSHA: base, HeadSHA: pull.HeadSHA})
	if err != nil {
		return "", err
	}
	verdicts, ok := store.(review.Store)
	if !ok {
		return "", errors.New("provider reconciliation requires verdict store")
	}
	if _, _, err := review.CarryForward(ctx, verdicts, item.runner, source, derived, replay.Result{}); err != nil {
		return "", err
	}
	return "provider_restack", nil
}

func providerBase(ctx context.Context, runner *gitexec.Runner, branch, head string) (string, error) {
	if _, err := runner.Run(ctx, "fetch", "origin", branch); err != nil {
		return "", err
	}
	base, err := runner.Run(ctx, "merge-base", "FETCH_HEAD", head)
	return strings.TrimSpace(string(base)), err
}

func propagateClosure(ctx context.Context, store Store, publications []journal.Publication) error {
	closed := map[string]bool{}
	for _, publication := range publications {
		observation, found, err := store.ProviderObservation(ctx, publication.Workspace, publication.Change)
		if err != nil {
			return err
		}
		if found && (observation.State == "closed" || observation.State == "dependency_abandoned") {
			closed[publication.Workspace+"\x00"+publication.StackID+"\x00"+publication.Head] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, publication := range publications {
			key := publication.Workspace + "\x00" + publication.StackID + "\x00" + publication.Prior
			if !closed[key] || publication.Prior == "" {
				continue
			}
			observation, found, err := store.ProviderObservation(ctx, publication.Workspace, publication.Change)
			if err != nil {
				return err
			}
			if !found || observation.State != "dependency_abandoned" {
				if !found {
					observation = journal.ProviderObservation{Workspace: publication.Workspace, Change: publication.Change,
						Base: publication.Trunk, HeadSHA: publication.Head}
				}
				observation.State = "dependency_abandoned"
				if err := store.RecordProviderObservation(ctx, observation); err != nil {
					return err
				}
				changed = true
			}
			closed[publication.Workspace+"\x00"+publication.StackID+"\x00"+publication.Head] = true
		}
	}
	return nil
}
