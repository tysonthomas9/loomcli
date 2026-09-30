package landing

import (
	"context"
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
	return publication.Trunk, nil
}

func observeProvider(ctx context.Context, store Store, item fetchedPublication, pull stackpublish.PR, all []journal.Publication) error {
	publication := item.publication
	if pull.HeadSHA == "" || pull.Base == "" {
		return errors.New("provider PR omitted head SHA or base")
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
	} else if pull.Base != base {
		state = "diverged"
	} else if pull.HeadSHA != publication.Head && (!found || previous.HeadSHA != pull.HeadSHA) {
		state, err = reconcileHead(ctx, store, item, pull)
		if err != nil {
			return err
		}
	} else if found && previous.HeadSHA == pull.HeadSHA && previous.Base == pull.Base {
		state = previous.State
	}
	return store.RecordProviderObservation(ctx, journal.ProviderObservation{Workspace: publication.Workspace,
		Change: publication.Change, Base: pull.Base, HeadSHA: pull.HeadSHA, State: state})
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
