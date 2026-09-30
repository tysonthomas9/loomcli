// Package publish pushes an approved working-area layer from the host with an exact lease.
package publish

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type Store interface {
	apply.Store
	RevisionByHead(context.Context, string, string, string) (loomgit.Revision, error)
	WorkspaceRepos(context.Context, string) ([]loomgit.WorkspaceRepo, error)
	BeginPublication(context.Context, journal.Publication) error
	BeginStackPublications(context.Context, []journal.Publication) error
	RecordPublicationDrift(context.Context, journal.Publication, string) error
	AdvancePublication(context.Context, journal.Publication) error
	OpenPublications(context.Context) ([]journal.Publication, error)
	Publication(context.Context, string, string) (journal.Publication, bool, error)
}

type Forge interface {
	ListStackPRs(context.Context, string, string, string) ([]stackpublish.PR, error)
	CreatePR(context.Context, string, string, string, string, string, string) (stackpublish.PR, error)
	UpdatePRBase(context.Context, string, string, int, string) error
	UpdatePRBody(context.Context, string, string, int, string) error
}

type Request struct {
	Workspace, Lead, Change string
	Repo, WorkingArea       string
	BaseSHA                 string
	Branch                  string // If supplied, must be the branch owned by this change.
	RepoName                string // Name in the recorded workspace repository list.
	forge                   Forge
	token                   string
	credential              func(context.Context) string
	slug                    string
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
	if err := requireNotStacked(ctx, store, req); err != nil {
		return loomgit.Revision{}, err
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
	publication, forge, err := preflight(ctx, store, runner, req, branch, head)
	if err != nil {
		return loomgit.Revision{}, err
	}
	if err := requireRevisionRef(ctx, runner, req, revision, head); err != nil {
		return loomgit.Revision{}, err
	}
	if err := store.BeginPublication(ctx, publication); err != nil {
		return loomgit.Revision{}, err
	}
	if err := finishPublication(ctx, store, runner, mirror.NewPusher(runner), forge, publication); err != nil {
		return loomgit.Revision{}, err
	}
	return revision, nil
}

func requireNotStacked(ctx context.Context, store Store, req Request) error {
	prior, found, err := store.Publication(ctx, req.Workspace, req.Change)
	if err != nil {
		return err
	}
	if found && prior.StackID != "" {
		return loomgit.NewError(loomgit.ModeMismatch, "change belongs to a published stack", nil)
	}
	return nil
}

func requireRevisionRef(ctx context.Context, runner *gitexec.Runner, req Request, revision loomgit.Revision, head string) error {
	revisionRef, err := refname.RevisionHead(req.Workspace, req.Change, strconv.Itoa(revision.Number))
	if err != nil {
		return err
	}
	refSHA, err := runner.Run(ctx, "rev-parse", "--verify", revisionRef)
	if err != nil || strings.TrimSpace(string(refSHA)) != head {
		return loomgit.NewError(loomgit.StaleSubject, "revision ref does not match working-area layer", err)
	}
	return nil
}

func preflight(ctx context.Context, store Store, runner *gitexec.Runner, req Request, branch, head string) (journal.Publication, Forge, error) {
	remoteOut, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
	if err != nil {
		return journal.Publication{}, nil, err
	}
	remote := strings.TrimSpace(string(remoteOut))
	slug, err := githubSlug(remote)
	if req.forge != nil && req.slug != "" {
		slug = req.slug
		err = nil
	}
	if err != nil {
		return journal.Publication{}, nil, err
	}
	parts := strings.Split(slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return journal.Publication{}, nil, errors.New("GitHub repository slug is invalid")
	}
	token := strings.TrimSpace(req.token)
	if req.credential != nil {
		token = strings.TrimSpace(req.credential(ctx))
	} else if token == "" {
		token = githubtoken.GitHub(ctx)
	}
	if token == "" {
		return journal.Publication{}, nil, errors.New("GitHub host credential unavailable")
	}
	trunk, err := recordedTrunk(ctx, store, req)
	if err != nil {
		return journal.Publication{}, nil, err
	}
	forge := req.forge
	if forge == nil {
		forge = stackpublish.NewGitHubForge(token, nil, "")
	}
	return journal.Publication{Workspace: req.Workspace, Change: req.Change, Repo: req.Repo,
		Branch: branch, Trunk: trunk, Slug: slug, Head: head}, forge, nil
}

func recordedTrunk(ctx context.Context, store Store, req Request) (string, error) {
	repos, err := store.WorkspaceRepos(ctx, req.Workspace)
	if err != nil {
		return "", err
	}
	repoName := req.RepoName
	if repoName == "" {
		repoName = filepath.Base(req.Repo)
	}
	for _, repo := range repos {
		if repo.Repo == repoName && repo.Trunk != "" {
			return repo.Trunk, nil
		}
	}
	return "", errors.New("recorded repository trunk unavailable")
}

func githubSlug(remote string) (string, error) {
	var path string
	if strings.HasPrefix(remote, "git@github.com:") {
		path = strings.TrimPrefix(remote, "git@github.com:")
	} else {
		u, err := url.Parse(remote)
		if err != nil || !strings.EqualFold(u.Hostname(), "github.com") || (u.Scheme != "https" && u.Scheme != "ssh") {
			return "", errors.New("origin is not a GitHub repository")
		}
		path = strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("origin has no GitHub repository slug")
	}
	return path, nil
}

func finishPublication(ctx context.Context, store Store, runner *gitexec.Runner, pusher mirror.RefPusher, forge Forge, publication journal.Publication) error {
	if err := push(ctx, runner, pusher, publication.Workspace, publication.Change, publication.Branch, publication.Head); err != nil {
		return err
	}
	publication.Phase = "pushed"
	if err := store.AdvancePublication(ctx, publication); err != nil {
		return err
	}
	parts := strings.Split(publication.Slug, "/")
	prs, err := forge.ListStackPRs(ctx, parts[0], parts[1], publication.Branch)
	if err != nil {
		return err
	}
	body := "Loom-Change-Id: " + publication.Change + "\n"
	for _, pr := range prs {
		if pr.Head != publication.Branch || pr.State != "open" {
			continue
		}
		if publication.StackID != "" && pr.Base != publication.Trunk {
			if err := forge.UpdatePRBase(ctx, parts[0], parts[1], pr.Number, publication.Trunk); err != nil {
				return err
			}
		}
		if !strings.Contains(pr.Body, body) {
			if err := forge.UpdatePRBody(ctx, parts[0], parts[1], pr.Number, strings.TrimSpace(pr.Body)+"\n\n"+body); err != nil {
				return err
			}
		}
		publication.PRNumber, publication.PRURL = pr.Number, pr.URL
		break
	}
	if publication.PRNumber == 0 {
		pr, err := forge.CreatePR(ctx, parts[0], parts[1], publication.Branch, publication.Trunk,
			"Loom change "+publication.Change, body)
		if err != nil {
			return err
		}
		if !strings.Contains(pr.Body, body) {
			if err := forge.UpdatePRBody(ctx, parts[0], parts[1], pr.Number, strings.TrimSpace(pr.Body)+"\n\n"+body); err != nil {
				return err
			}
		}
		publication.PRNumber, publication.PRURL = pr.Number, pr.URL
	}
	publication.Phase = "done"
	return store.AdvancePublication(ctx, publication)
}

// Reconcile resumes durable publish intents after an interrupted push or PR create.
func Reconcile(ctx context.Context, store Store, forge Forge, token string) error {
	publications, err := store.OpenPublications(ctx)
	if err != nil {
		return err
	}
	if len(publications) == 0 {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		token = githubtoken.GitHub(ctx)
	}
	if token == "" {
		return errors.New("GitHub host credential unavailable")
	}
	if forge == nil {
		forge = stackpublish.NewGitHubForge(token, nil, "")
	}
	stackGroups := make(map[string][]journal.Publication)
	for _, publication := range publications {
		if publication.StackID != "" {
			key := publication.Workspace + "\x00" + publication.StackID + "\x00" + publication.Repo
			stackGroups[key] = append(stackGroups[key], publication)
		}
	}
	for _, group := range stackGroups {
		runner, err := gitexec.New(group[0].Repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
		if err != nil {
			return err
		}
		layers := make([]stackLayer, 0, len(group))
		for _, publication := range group {
			layers = append(layers, stackLayer{publication: publication, revision: loomgit.Revision{HeadSHA: publication.Head}, prior: publication.Prior})
		}
		if err := pushStackHeads(ctx, store, runner, mirror.NewPusher(runner), layers); err != nil {
			return err
		}
	}
	for _, publication := range publications {
		runner, err := gitexec.New(publication.Repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
		if err != nil {
			return err
		}
		if err := finishPublication(ctx, store, runner, mirror.NewPusher(runner), forge, publication); err != nil {
			return err
		}
	}
	return nil
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
