package abandon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/cred"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Request struct {
	Workspace, Task, Repo, Attempt, Worktree, SourceRepo, Reason string
	RequestedBy                                                  string
	ClosePR, DeleteRemote, ConfirmIgnored                        bool
}

type Result struct {
	Revision          loomgit.Revision
	Complete          bool
	RetentionEligible bool
	Ignored           []agentcapture.Entry
	Dependents        []taskcopy.DependentLineage
}

type ConfirmationRequired struct{ Ignored []agentcapture.Entry }

func (err *ConfirmationRequired) Error() string { return "ignored files require confirmation" }

type Forge interface {
	ClosePR(context.Context, string, string, int, string) error
}

type Pusher interface {
	RemoteSHA(context.Context, string, string) (string, error)
	Push(context.Context, string, string, string, string) error
}

type ClaimReleaser interface {
	backend.IssueBackend
	CurrentIssueLockHolder(context.Context, string) (string, error)
}

type Service struct {
	JournalPath string
	Forge       Forge
	Pusher      Pusher
	Claims      ClaimReleaser
	Sessions    store.AgentSessionStore
}

func New() *Service {
	return &Service{JournalPath: filepath.Join(config.GetConfigDir(), "loomgit", "store.db")}
}

func ReconcileLocal(ctx context.Context, sessions store.AgentSessionStore) error {
	abandonment := New()
	claims, ok := cli.GetDeps(nil).IssueBackend.(ClaimReleaser)
	if !ok {
		return errors.New("issue backend cannot read current claim holder")
	}
	abandonment.Claims = claims
	abandonment.Sessions = sessions
	return abandonment.Reconcile(ctx)
}

func (service *Service) Run(ctx context.Context, req Request) (Result, error) {
	if req.Workspace == "" || req.Task == "" || req.Repo == "" || req.Attempt == "" ||
		req.Worktree == "" || req.Reason == "" || service.JournalPath == "" {
		return Result{}, errors.New("workspace, task, repo, attempt, worktree and reason are required")
	}
	if service.Claims == nil || service.Sessions == nil || req.RequestedBy == "" {
		return Result{}, errors.New("claim backend, session store and requesting user are required")
	}
	if err := os.MkdirAll(filepath.Dir(service.JournalPath), 0o700); err != nil {
		return Result{}, err
	}
	store, err := journal.OpenSQLite(service.JournalPath)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = store.Close() }()
	change, err := driverfreeze.ChangeForTaskAt(ctx, service.JournalPath, req.Workspace, req.Task, req.Repo)
	if err != nil {
		return Result{}, err
	}
	previous, exists, err := store.Abandonment(ctx, req.Workspace, change)
	if err != nil {
		return Result{}, err
	}
	if exists {
		return service.resume(ctx, store, req, change, previous)
	}
	return service.start(ctx, store, req, change)
}

func (service *Service) resume(ctx context.Context, store *journal.SQLite, req Request, change string, previous journal.Abandonment) (Result, error) {
	if previous.Reason != req.Reason {
		return Result{}, loomgit.NewError(loomgit.StaleSubject, "change was abandoned for another reason", nil)
	}
	if previous.RequestedBy != req.RequestedBy {
		return Result{}, loomgit.NewError(loomgit.StaleSubject, "abandonment requester changed", nil)
	}
	if err := store.AbandonChange(ctx, req.Workspace, change); err != nil {
		return Result{}, err
	}
	if (req.ClosePR || req.DeleteRemote) && previous.Repo == "" {
		return Result{}, loomgit.NewError(loomgit.StaleSubject, "change has no recorded publication", nil)
	}
	if err := store.EnableAbandonmentActions(ctx, req.Workspace, change, req.ClosePR, req.DeleteRemote); err != nil {
		return Result{}, err
	}
	previous, _, err := store.Abandonment(ctx, req.Workspace, change)
	if err != nil {
		return Result{}, err
	}
	revision, err := store.RevisionByRequest(ctx, "abandon:"+req.Workspace+":"+req.Attempt)
	if err != nil {
		return Result{}, err
	}
	dependents, err := taskcopy.DependentsOfAt(ctx, service.JournalPath, req.Workspace, change)
	if err != nil {
		return Result{}, err
	}
	return Result{Revision: revision, Complete: !revision.Incomplete, RetentionEligible: previous.RetentionEligible,
		Dependents: dependents}, service.finish(ctx, store, previous)
}

func (service *Service) start(ctx context.Context, store *journal.SQLite, req Request, change string) (Result, error) {
	if err := service.ensureNoLiveTask(ctx, req.Workspace, req.Task); err != nil {
		return Result{}, err
	}
	holder, err := service.Claims.CurrentIssueLockHolder(ctx, req.Task)
	if err != nil {
		return Result{}, err
	}
	ignored, err := agentcapture.ListIgnored(ctx, req.Worktree)
	if err != nil {
		return Result{}, err
	}
	if len(ignored) > 0 && !req.ConfirmIgnored {
		return Result{Ignored: ignored}, &ConfirmationRequired{Ignored: ignored}
	}
	if _, err := abandonmentRow(ctx, store, req, change, holder, false); err != nil {
		return Result{}, err
	}
	revision, complete, err := service.captureRevision(ctx, req)
	if err != nil {
		return Result{}, err
	}
	result := Result{Revision: revision, Complete: complete, RetentionEligible: complete, Ignored: ignored}
	row, err := abandonmentRow(ctx, store, req, change, holder, complete)
	if err != nil {
		return result, err
	}
	if err := store.BeginAbandonment(ctx, row); err != nil {
		return result, err
	}
	if err := store.AbandonChange(ctx, req.Workspace, change); err != nil {
		return result, err
	}
	result.Dependents, err = taskcopy.DependentsOfAt(ctx, service.JournalPath, req.Workspace, change)
	if err != nil {
		return result, err
	}
	return result, service.finish(ctx, store, row)
}

func (service *Service) captureRevision(ctx context.Context, req Request) (loomgit.Revision, bool, error) {
	runner, err := gitexec.New(req.Worktree, gitexec.Options{ReadOnly: true})
	if err != nil {
		return loomgit.Revision{}, false, err
	}
	baseRef, err := refname.AttemptBase(req.Workspace, req.Attempt)
	if err != nil {
		return loomgit.Revision{}, false, err
	}
	base, err := runner.Run(ctx, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		return loomgit.Revision{}, false, err
	}
	if req.SourceRepo != "" {
		source, err := gitexec.New(req.SourceRepo, gitexec.Options{ReadOnly: true})
		if err != nil {
			return loomgit.Revision{}, false, err
		}
		sourceBase, err := source.Run(ctx, "rev-parse", "--verify", baseRef+"^{commit}")
		if err != nil || !strings.EqualFold(strings.TrimSpace(string(sourceBase)), strings.TrimSpace(string(base))) {
			return loomgit.Revision{}, false, loomgit.NewError(loomgit.StaleSubject, "source repository does not own task-copy base", err)
		}
	}
	captureAttempt := "abandon-" + req.Attempt
	captureRef, err := refname.AttemptCapture(req.Workspace, captureAttempt)
	if err != nil {
		return loomgit.Revision{}, false, err
	}
	exists, err := gitexec.RefExists(req.Worktree, captureRef)
	if err != nil {
		return loomgit.Revision{}, false, err
	}
	if exists {
		return loomgit.Revision{}, false, loomgit.NewError(loomgit.AttentionRequired, "abandonment capture exists without a completed record", nil)
	}
	source := req.SourceRepo
	if source == "" {
		source = req.Worktree
	}
	captured, err := agentcapture.CaptureTaskCopyAt(ctx, service.JournalPath, source, req.Worktree, req.Workspace, captureAttempt, req.Task, req.Task)
	if err != nil {
		return loomgit.Revision{}, false, err
	}
	revision, err := driverfreeze.FreezeCaptureAt(ctx, service.JournalPath, driverfreeze.CaptureRequest{
		Workspace: req.Workspace, Task: req.Task, Repo: req.Repo, Attempt: captureAttempt,
		Worktree: req.Worktree, Base: strings.TrimSpace(string(base)), CaptureSHA: captured.SHA,
		SourceRepo: req.SourceRepo, RequestID: "abandon:" + req.Workspace + ":" + req.Attempt,
		Outcome: "abandoned", Complete: captured.Complete,
	})
	return revision, captured.Complete, err
}

func abandonmentRow(ctx context.Context, store *journal.SQLite, req Request, change, holder string, complete bool) (journal.Abandonment, error) {
	publication, published, err := store.Publication(ctx, req.Workspace, change)
	if err != nil {
		return journal.Abandonment{}, err
	}
	if !published && (req.ClosePR || req.DeleteRemote) {
		return journal.Abandonment{}, loomgit.NewError(loomgit.StaleSubject, "change has no recorded publication", nil)
	}
	row := journal.Abandonment{Workspace: req.Workspace, Change: change, Task: req.Task, Reason: req.Reason,
		Worktree: req.Worktree, SourceRepo: req.SourceRepo, ClaimActor: holder, RequestedBy: req.RequestedBy,
		RetentionEligible: complete}
	if published {
		if req.SourceRepo == "" || filepath.Clean(req.SourceRepo) != filepath.Clean(publication.Repo) {
			return row, loomgit.NewError(loomgit.StaleSubject, "published change source repository differs from task copy", nil)
		}
		branch, err := refname.ChangeBranch(req.Workspace, change)
		if err != nil {
			return row, err
		}
		if publication.Branch != branch {
			return row, loomgit.NewError(loomgit.Protected, "publication branch is not owned by change", nil)
		}
		row.Repo, row.Branch, row.Slug, row.Head, row.PRNumber = publication.Repo, branch, publication.Slug, publication.Head, publication.PRNumber
		row.ClosePR = (complete || req.ClosePR) && row.PRNumber != 0
		row.DeleteRemote = complete || req.DeleteRemote
		if req.ClosePR && row.PRNumber == 0 {
			return row, errors.New("published change has no recorded PR number")
		}
	}
	return row, nil
}

func (service *Service) finish(ctx context.Context, store *journal.SQLite, row journal.Abandonment) error {
	if err := service.ensureNoLiveTask(ctx, row.Workspace, row.Task); err != nil {
		return err
	}
	var failures []error
	if !row.ClaimReleased {
		if err := service.releaseClaim(ctx, store, row); err != nil {
			failures = append(failures, err)
		}
	}
	if row.ClosePR && !row.PRClosed {
		if err := service.closePR(ctx, store, row); err != nil {
			failures = append(failures, err)
		}
	}
	if row.DeleteRemote && !row.RemoteDeleted {
		if err := service.deleteRemote(ctx, store, row); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (service *Service) releaseClaim(ctx context.Context, store *journal.SQLite, row journal.Abandonment) error {
	if err := service.ensureNoLiveTask(ctx, row.Workspace, row.Task); err != nil {
		return err
	}
	holder, err := service.Claims.CurrentIssueLockHolder(ctx, row.Task)
	if err != nil {
		return err
	}
	if holder != "" && holder != row.ClaimActor {
		return loomgit.NewError(loomgit.AttentionRequired, "task claim holder changed after abandonment capture", nil)
	}
	if row.ClaimActor != "" {
		if _, err := driver.ReleaseTask(ctx, service.Claims, driver.TaskReleaseOptions{TaskID: row.Task, Actor: row.ClaimActor}); err != nil {
			return fmt.Errorf("release task claim: %w", err)
		}
	}
	return store.MarkAbandonmentClaimReleased(ctx, row.Workspace, row.Change)
}

func (service *Service) ensureNoLiveTask(ctx context.Context, workspace, task string) error {
	sessions, err := service.Sessions.List(ctx, workspace, store.AgentSessionFilter{TaskID: task})
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.Kind == domain.AgentSessionKindTask && session.FinishedAt == nil &&
			(session.Status == domain.AgentSessionQueued || session.Status == domain.AgentSessionLeased ||
				session.Status == domain.AgentSessionStarting || session.Status == domain.AgentSessionRunning ||
				session.Status == domain.AgentSessionIdle || session.Status == domain.AgentSessionYielded) {
			return loomgit.NewError(loomgit.AttentionRequired, "task has a live agent; stop it before abandoning", nil)
		}
	}
	return nil
}

func (service *Service) closePR(ctx context.Context, store *journal.SQLite, row journal.Abandonment) error {
	parts := strings.Split(row.Slug, "/")
	if len(parts) != 2 {
		return errors.New("recorded PR repository is invalid")
	}
	forge := service.Forge
	if forge == nil {
		forge = stackpublish.NewConfiguredGitHubForge(githubtoken.GitHub(ctx))
	}
	if err := forge.ClosePR(ctx, parts[0], parts[1], row.PRNumber, "Abandoned by "+row.RequestedBy+": "+row.Reason); err != nil {
		return fmt.Errorf("close PR: %w", err)
	}
	return store.AdvanceAbandonment(ctx, row.Workspace, row.Change, true, false)
}

func (service *Service) deleteRemote(ctx context.Context, store *journal.SQLite, row journal.Abandonment) error {
	runner, err := gitexec.New(row.Repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	remoteBytes, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
	if err != nil {
		return err
	}
	remote := strings.TrimSpace(string(remoteBytes))
	pusher := service.Pusher
	if pusher == nil {
		pusher = hostPusher{runner: runner}
	}
	ref := "refs/heads/" + row.Branch
	actual, err := pusher.RemoteSHA(ctx, remote, ref)
	if err != nil {
		return err
	}
	if actual != "" && actual != row.Head {
		return loomgit.NewError(loomgit.Diverged, "published branch changed outside Loom", nil)
	}
	if actual != "" {
		if err := pusher.Push(ctx, remote, ref, "", row.Head); err != nil {
			return err
		}
	}
	return store.AdvanceAbandonment(ctx, row.Workspace, row.Change, false, true)
}

type hostPusher struct{ runner *gitexec.Runner }

func (pusher hostPusher) run(ctx context.Context, remote string, args ...string) ([]byte, error) {
	parsed, err := url.Parse(remote)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "https" {
		if !strings.EqualFold(parsed.Hostname(), "github.com") {
			return nil, fmt.Errorf("no credential resolver for %s", parsed.Hostname())
		}
		token := githubtoken.GitHub(ctx)
		if token == "" {
			return nil, errors.New("GitHub host credential unavailable")
		}
		return pusher.runner.RunWithCredential(ctx, cred.New(remote, token), remote, args...)
	}
	if parsed.Scheme != "" && parsed.Scheme != "file" && parsed.Scheme != "ssh" {
		return nil, fmt.Errorf("unsupported Git remote scheme %q", parsed.Scheme)
	}
	return pusher.runner.Run(ctx, args...)
}

func (pusher hostPusher) RemoteSHA(ctx context.Context, remote, ref string) (string, error) {
	out, err := pusher.run(ctx, remote, "ls-remote", remote, ref)
	if err != nil || len(out) == 0 {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[1] != ref {
		return "", errors.New("unexpected remote ref response")
	}
	return fields[0], nil
}

func (pusher hostPusher) Push(ctx context.Context, remote, ref, localSHA, expected string) error {
	_, err := pusher.run(ctx, remote, "push", "--force-with-lease="+ref+":"+expected,
		remote, localSHA+":"+ref)
	return err
}

func (service *Service) Reconcile(ctx context.Context) error {
	store, err := journal.OpenSQLite(service.JournalPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	rows, err := store.OpenAbandonments(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if err := store.AbandonChange(ctx, row.Workspace, row.Change); err != nil {
			failures = append(failures, fmt.Errorf("mark %s abandoned: %w", row.Change, err))
			continue
		}
		if err := service.finish(ctx, store, row); err != nil {
			failures = append(failures, fmt.Errorf("abandon %s: %w", row.Change, err))
		}
	}
	return errors.Join(failures...)
}
