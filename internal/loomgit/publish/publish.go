// Package publish pushes an approved working-area layer from the host with an exact lease.
package publish

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type Store interface {
	apply.Store
	RevisionByHead(context.Context, string, string, string) (loomgit.Revision, error)
}

type Request struct {
	Workspace, Lead, Change string
	Repo, WorkingArea       string
	BaseSHA                 string
	Branch                  string // If supplied, must be the branch owned by this change.
}

// Publish selects the applied layer, checks its verdict, and pushes its immutable head.
// Repo is the host's source repository; WorkingArea is the checked-out lead area.
func Publish(ctx context.Context, store Store, req Request) (loomgit.Revision, error) {
	branch, err := refname.ChangeBranch(req.Workspace, req.Change)
	if err != nil {
		return loomgit.Revision{}, err
	}
	if req.Branch != "" && req.Branch != branch {
		return loomgit.Revision{}, loomgit.NewError(loomgit.Protected, "branch is not owned by this change", nil)
	}
	if req.BaseSHA == "" || req.Repo == "" || req.WorkingArea == "" || req.Lead == "" {
		return loomgit.Revision{}, errors.New("repo, working area, lead and base SHA are required")
	}
	runner, err := gitexec.New(req.Repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return loomgit.Revision{}, err
	}
	area, err := gitexec.New(req.WorkingArea, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return loomgit.Revision{}, err
	}
	head, err := layerHead(ctx, apply.New(store, nil, area), area, req.Workspace, req.Lead, req.BaseSHA, req.Change)
	if err != nil {
		return loomgit.Revision{}, err
	}
	revision, err := store.RevisionByHead(ctx, req.Workspace, req.Change, head)
	if errors.Is(err, journal.ErrNotFound) {
		return loomgit.Revision{}, loomgit.NewError(loomgit.StaleSubject, "working-area layer has no recorded revision", err)
	}
	if err != nil {
		return loomgit.Revision{}, err
	}
	if err := review.RequireVerdict(ctx, store, req.Workspace, req.Change, revision.Number, head, "publish", ""); err != nil {
		return loomgit.Revision{}, err
	}
	revisionRef, err := refname.RevisionHead(req.Workspace, req.Change, strconv.Itoa(revision.Number))
	if err != nil {
		return loomgit.Revision{}, err
	}
	refSHA, err := runner.Run(ctx, "rev-parse", "--verify", revisionRef)
	if err != nil || strings.TrimSpace(string(refSHA)) != head {
		return loomgit.Revision{}, loomgit.NewError(loomgit.StaleSubject, "revision ref does not match working-area layer", err)
	}
	if err := push(ctx, runner, mirror.NewPusher(runner), req.Workspace, req.Change, branch, head); err != nil {
		return loomgit.Revision{}, err
	}
	return revision, nil
}

func layerHead(ctx context.Context, applied *apply.Service, area *gitexec.Runner, workspace, lead, base, change string) (string, error) {
	if len(base) != 40 && len(base) != 64 {
		return "", errors.New("base must be a full Git SHA")
	}
	layers, err := applied.AppliedLog(ctx, workspace, lead)
	if err != nil {
		return "", err
	}
	for index := len(layers) - 1; index >= 0; index-- {
		layer := layers[index]
		if layer.Change == change {
			if _, err := area.Run(ctx, "merge-base", "--is-ancestor", base, layer.NewTip); err != nil {
				return "", loomgit.NewError(loomgit.StaleSubject, "applied layer is outside the requested base", err)
			}
			if _, err := area.Run(ctx, "merge-base", "--is-ancestor", layer.NewTip, "HEAD"); err != nil {
				return "", loomgit.NewError(loomgit.StaleSubject, "applied layer is no longer in the working area", err)
			}
			return layer.NewTip, nil
		}
	}
	return "", loomgit.NewError(loomgit.StaleSubject, "change is not a working-area layer", nil)
}

func push(ctx context.Context, runner *gitexec.Runner, pusher mirror.RefPusher, workspace, change, branch, head string) error {
	publication, err := refname.Publication(workspace, change)
	if err != nil {
		return err
	}
	prior, err := localSHA(ctx, runner, publication)
	if err != nil {
		return err
	}
	remoteOut, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
	if err != nil {
		return err
	}
	remote := strings.TrimSpace(string(remoteOut))
	if remote == "" {
		return errors.New("origin push remote is empty")
	}
	remoteRef := "refs/heads/" + branch
	actual, err := pusher.RemoteSHA(ctx, remote, remoteRef)
	if err != nil {
		return err
	}
	if actual != prior && actual != head {
		return loomgit.NewError(loomgit.Diverged, "PR branch moved outside Loom", nil)
	}
	if actual != head {
		if err := pusher.Push(ctx, remote, remoteRef, head, prior); err != nil {
			current, readErr := pusher.RemoteSHA(ctx, remote, remoteRef)
			if readErr == nil && current != prior {
				return loomgit.NewError(loomgit.Diverged, "PR branch changed during publish", err)
			}
			return fmt.Errorf("publish PR branch: %w", err)
		}
	}
	if prior == head {
		return nil
	}
	expected := prior
	if expected == "" {
		expected = strings.Repeat("0", len(head))
	}
	if err := runner.UpdateRef(ctx, publication, head, expected); err != nil {
		return fmt.Errorf("record published head: %w", err)
	}
	return nil
}

func localSHA(ctx context.Context, runner *gitexec.Runner, ref string) (string, error) {
	exists, err := gitexec.RefExists(runner.Path(), ref)
	if err != nil || !exists {
		return "", err
	}
	out, err := runner.Run(ctx, "rev-parse", "--verify", ref)
	return strings.TrimSpace(string(out)), err
}
