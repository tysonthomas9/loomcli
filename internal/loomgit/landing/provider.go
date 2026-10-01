package landing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func expectedBase(ctx context.Context, store Store, publication journal.Publication, all []journal.Publication) (string, error) {
	if publication.StackID == "" {
		return publication.Trunk, nil
	}
	for _, predecessor := range all {
		if predecessor.Workspace != publication.Workspace || predecessor.StackID != publication.StackID ||
			predecessor.Change == publication.Change || predecessor.Head != publication.Prior || publication.Prior == "" {
			continue
		}
		status, err := store.LandingStatus(ctx, predecessor.Workspace, predecessor.Change)
		if err != nil {
			return "", err
		}
		if status.State != "landed" {
			return predecessor.Branch, nil
		}
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
		return "native_restack", nil
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
		status, err := store.LandingStatus(ctx, predecessor.Workspace, predecessor.Change)
		if err != nil {
			return false, err
		}
		if status.State == "landed" || status.State == "merged" {
			return true, nil
		}
		pull, err := ownedPull(ctx, forge, predecessor)
		if err != nil {
			return false, err
		}
		if pull.Merged {
			return true, nil
		}
		current = predecessor
	}
	return false, nil
}

func stackPredecessor(publication journal.Publication, all []journal.Publication) (journal.Publication, bool) {
	for _, candidate := range all {
		if candidate.Workspace != publication.Workspace || candidate.StackID != publication.StackID ||
			candidate.Change == publication.Change {
			continue
		}
		if candidate.Branch == publication.Trunk || publication.Prior != "" && candidate.Head == publication.Prior {
			return candidate, true
		}
	}
	return journal.Publication{}, false
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
