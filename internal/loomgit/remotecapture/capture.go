// Package remotecapture binds remote task-run captures to host repositories.
package remotecapture

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pushproxy"
)

const leaseTTL = 10 * time.Minute

type FinalizeInput struct {
	Workspace, Attempt, Task, RepoURL string
	BaseSHA, CaptureSHA, TreeHash     string
	Outcome                           string
	Complete                          bool
	Owner                             string
}

func journalPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
}

func openJournal(path string) (*journal.SQLite, error) {
	path = journalPath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if err := store.EnsureMirrorSchema(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.EnsureRemoteCaptureSchema(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func git(repo string) (*gitexec.Runner, error) {
	return gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
}

func remoteURL(ctx context.Context, repo string) (string, error) {
	runner, err := git(repo)
	if err != nil {
		return "", err
	}
	out, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
	if err != nil {
		return "", err
	}
	remote := strings.TrimSpace(string(out))
	if remote == "" {
		return "", errors.New("provider remote is empty")
	}
	return remote, nil
}

func repository(ctx context.Context, store *journal.SQLite, workspace, requestedURL string) (string, string, string, error) {
	if requestedURL == "" {
		return "", "", "", errors.New("provider URL is required")
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		return "", "", "", err
	}
	repos, err := store.WorkspaceRepos(ctx, workspace)
	if err != nil {
		return "", "", "", err
	}
	for _, record := range repos {
		path := localworkspace.RepoPath(cache.Workspaces[workspace], record.Repo)
		if path == "" {
			continue
		}
		remote, err := remoteURL(ctx, path)
		if err != nil {
			return "", "", "", err
		}
		if requestedURL == remote {
			return path, record.Repo, remote, nil
		}
	}
	return "", "", "", fmt.Errorf("provider remote is not a repository in workspace %q", workspace)
}

func fullSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func recordBase(ctx context.Context, repo, workspace, attempt, baseSHA string) error {
	if !fullSHA(baseSHA) {
		return errors.New("full base SHA is required")
	}
	runner, err := git(repo)
	if err != nil {
		return err
	}
	base, err := runner.Run(ctx, "rev-parse", "--verify", baseSHA+"^{commit}")
	if err != nil || strings.TrimSpace(string(base)) != baseSHA {
		return fmt.Errorf("full base SHA is not present in host repository: %w", err)
	}
	ref, err := refname.AttemptBase(workspace, attempt)
	if err != nil {
		return err
	}
	if existing, lookupErr := runner.Run(ctx, "show-ref", "--verify", "--hash", ref); lookupErr == nil {
		if strings.TrimSpace(string(existing)) != baseSHA {
			return errors.New("attempt base changed")
		}
		return nil
	}
	return runner.UpdateRef(ctx, ref, baseSHA, strings.Repeat("0", len(baseSHA)))
}

func claim(ctx context.Context, store *journal.SQLite, workspace, attempt, owner string) (loomgit.Lease, error) {
	scope := pushproxy.LeaseScope(workspace, attempt)
	prior, err := store.CurrentLease(ctx, scope)
	if err == nil && prior.Owner == owner && prior.ExpiresAt.After(time.Now()) {
		return store.RenewLease(ctx, prior, leaseTTL)
	}
	return store.ClaimLease(ctx, scope, owner, leaseTTL)
}

// Prepare pins the base and mints a short-lived token for this attempt only.
func Prepare(ctx context.Context, path, workspace, attempt, repoURL, baseSHA, owner string) (string, string, error) {
	if workspace == "" || attempt == "" || owner == "" {
		return "", "", errors.New("capture attempt identity is required")
	}
	store, err := openJournal(path)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = store.Close() }()
	repo, _, _, err := repository(ctx, store, workspace, repoURL)
	if err != nil {
		return "", "", err
	}
	if err := recordBase(ctx, repo, workspace, attempt, baseSHA); err != nil {
		return "", "", err
	}
	lease, err := claim(ctx, store, workspace, attempt, owner)
	if err != nil {
		return "", "", err
	}
	token, err := pushproxy.Mint(ctx, store, repo, workspace, attempt, lease.Fence)
	if err != nil {
		return "", "", err
	}
	ref, err := refname.AttemptCapture(workspace, attempt)
	return token, ref, err
}

// Finalize checks the provider ref and tree before recording a revision.
func Finalize(ctx context.Context, path string, in FinalizeInput) (loomgit.Revision, error) {
	if in.Outcome != "completed" && in.Outcome != "failed" && in.Outcome != "cancelled" {
		return loomgit.Revision{}, errors.New("invalid capture outcome")
	}
	store, err := openJournal(path)
	if err != nil {
		return loomgit.Revision{}, err
	}
	defer func() { _ = store.Close() }()
	repo, repoName, remote, err := repository(ctx, store, in.Workspace, in.RepoURL)
	if err != nil {
		return loomgit.Revision{}, err
	}
	lease, err := store.CurrentLease(ctx, pushproxy.LeaseScope(in.Workspace, in.Attempt))
	if err != nil || lease.Owner != in.Owner || !lease.ExpiresAt.After(time.Now()) {
		return loomgit.Revision{}, errors.New("capture attempt lease is stale")
	}
	runner, err := git(repo)
	if err != nil {
		return loomgit.Revision{}, err
	}
	ref, err := refname.AttemptCapture(in.Workspace, in.Attempt)
	if err != nil {
		return loomgit.Revision{}, err
	}
	fetched, err := mirror.FetchRef(ctx, runner, remote, ref)
	if err != nil {
		return loomgit.Revision{}, fmt.Errorf("fetch provider capture: %w", err)
	}
	revision, err := driverfreeze.FreezeRemoteAt(ctx, journalPath(path), driverfreeze.CaptureRequest{
		Workspace: in.Workspace, Task: in.Task, Repo: repoName, Attempt: in.Attempt, Worktree: repo,
		Base: in.BaseSHA, CaptureSHA: in.CaptureSHA, Outcome: in.Outcome, Complete: in.Complete,
	}, in.TreeHash, fetched)
	if err != nil {
		return loomgit.Revision{}, err
	}
	if err := store.MarkRemoteCaptureFrozen(ctx, in.Workspace, in.Attempt, in.CaptureSHA); err != nil {
		return loomgit.Revision{}, err
	}
	if err := store.ReleaseLease(ctx, lease); err != nil {
		return loomgit.Revision{}, err
	}
	return revision, nil
}

// Remember retains a verified host capture for the mirror and Snapshot retry.
func Remember(ctx context.Context, path string, in FinalizeInput, reason string) error {
	store, err := openJournal(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	repo, _, _, err := repository(ctx, store, in.Workspace, in.RepoURL)
	if err != nil {
		return err
	}
	lease, err := store.CurrentLease(ctx, pushproxy.LeaseScope(in.Workspace, in.Attempt))
	if err != nil || lease.Owner != in.Owner || !lease.ExpiresAt.After(time.Now()) {
		return errors.New("capture attempt lease is stale")
	}
	runner, err := git(repo)
	if err != nil {
		return err
	}
	ref, err := refname.AttemptCapture(in.Workspace, in.Attempt)
	if err != nil {
		return err
	}
	head, err := runner.Run(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil || strings.TrimSpace(string(head)) != in.CaptureSHA {
		return errors.New("pending capture was not retained by host proxy")
	}
	if err := store.PutRemoteCapture(ctx, journal.RemoteCaptureRecord{
		Workspace: in.Workspace, Attempt: in.Attempt, Task: in.Task, RepoURL: in.RepoURL,
		BaseSHA: in.BaseSHA, CaptureSHA: in.CaptureSHA, TreeHash: in.TreeHash,
		Outcome: in.Outcome, Complete: in.Complete, State: "pending", Reason: reason,
	}); err != nil {
		return err
	}
	return store.ReleaseLease(ctx, lease)
}

// Recover freezes captures after the regular mirror pass accepts their refs.
func Recover(ctx context.Context, path string) error {
	store, err := openJournal(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	rows, err := store.PendingRemoteCaptures(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if err := recoverOne(ctx, store, journalPath(path), row); err != nil {
			failures = append(failures, fmt.Errorf("capture %s/%s: %w", row.Workspace, row.Attempt, err))
		}
	}
	return errors.Join(failures...)
}

func recoverOne(ctx context.Context, store *journal.SQLite, path string, row journal.RemoteCaptureRecord) error {
	repo, repoName, remote, err := repository(ctx, store, row.Workspace, row.RepoURL)
	if err != nil {
		return err
	}
	ref, err := refname.AttemptCapture(row.Workspace, row.Attempt)
	if err != nil {
		return err
	}
	mirrored, found, err := store.MirrorState(ctx, repo, ref)
	if err != nil || !found || mirrored.State != "mirrored" || mirrored.SHA != row.CaptureSHA || mirrored.Remote != remote {
		return err
	}
	runner, err := git(repo)
	if err != nil {
		return err
	}
	fetched, err := mirror.FetchRef(ctx, runner, remote, ref)
	if err != nil {
		return err
	}
	_, err = driverfreeze.FreezeRemoteAt(ctx, path, driverfreeze.CaptureRequest{
		Workspace: row.Workspace, Task: row.Task, Repo: repoName, Attempt: row.Attempt,
		Worktree: repo, Base: row.BaseSHA, CaptureSHA: row.CaptureSHA,
		Outcome: row.Outcome, Complete: row.Complete,
	}, row.TreeHash, fetched)
	if err != nil {
		return err
	}
	return store.MarkRemoteCaptureFrozen(ctx, row.Workspace, row.Attempt, row.CaptureSHA)
}

// ServeHTTP passes a verified attempt token to the provider push proxy.
func ServeHTTP(w http.ResponseWriter, r *http.Request, path, workspace string) {
	store, err := openJournal(path)
	if err != nil {
		http.Error(w, "capture proxy unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = store.Close() }()
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := pushproxy.Verify(r.Context(), store, auth)
	if err != nil || claims.Workspace != workspace {
		http.Error(w, "capture token invalid", http.StatusUnauthorized)
		return
	}
	remote, err := remoteURL(r.Context(), claims.Repo)
	if err != nil {
		http.Error(w, "capture proxy unavailable", http.StatusServiceUnavailable)
		return
	}
	(&pushproxy.Proxy{Store: store, Remote: remote}).ServeHTTP(w, r)
}
