package stackpublish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// PublishFromOrigin provisions an ephemeral checkout of repoURL, fetches the
// stack's branches from origin into local refs, and runs Publish against it.
//
// This is the unattended path used by the epic post-drain reconcile: it must NOT
// touch (or even require) a developer's local checkout, and it must not depend on
// the process working directory. The checkout is torn down before returning.
// token authenticates clone/fetch/push for a private GitHub https origin and is
// ignored for ssh/file origins.
//
// Non-dry-run: clone/fetch runs under the same outer publish admission as Publish
// (nested Publish reuses it). Remote push never runs outside the lease.
//
// Dry-run: provision is an explicit read-only preflight — clone/fetch only, no
// push, and no lease yet because DryRun Publish skips admission. Each git
// subprocess is still capped at MaxStackPublishCallBound (60s).
func (r *Reconciler) PublishFromOrigin(ctx context.Context, ws string, id sl.StackID, repoURL, token string, opts Options) (*Report, error) {
	if opts.DryRun {
		repoPath, cleanup, err := provisionOriginCheckout(ctx, repoURL, token, id)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		return r.Publish(ctx, ws, id, repoPath, opts)
	}
	var report *Report
	err := r.withAdmission(ctx, ws, id, func(ctx context.Context, _ *Session) error {
		repoPath, cleanup, err := provisionOriginCheckout(ctx, repoURL, token, id)
		if err != nil {
			return err
		}
		defer cleanup()
		var perr error
		report, perr = r.Publish(ctx, ws, id, repoPath, opts)
		return perr
	})
	return report, err
}

// provisionOriginCheckout clones repoURL into a fresh temp dir and fetches the
// stack's branches (loom/stack/<stack>/*) from origin into local refs/heads so
// the reconciler can run emptiness checks and re-push them. Returns the checkout
// path and a cleanup func that removes the temp dir.
//
// When called under a Session, runGit renews and bounds each subprocess by the
// verified lease window (boundLocal for non-push; Session.Do for push). Outside
// a session (DryRun preflight), each subprocess still has a ≤60s deadline.
func provisionOriginCheckout(ctx context.Context, repoURL, token string, id sl.StackID) (string, func(), error) {
	noop := func() {}
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return "", noop, fmt.Errorf("repo url required for origin reconcile")
	}
	tmp, err := os.MkdirTemp("", "loom-stack-reconcile-")
	if err != nil {
		return "", noop, fmt.Errorf("ephemeral checkout dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	env := append(envWith(), "LOOM_PR_GIT_PASSWORD="+token, "GIT_TERMINAL_PROMPT=0")
	cred := []string{"-c", "credential.helper=", "-c", "credential.helper=" + credHelper}

	cloneArgs := append(append([]string{}, cred...), "clone", "--no-tags", repoURL, "repo")
	if out, err := runGit(ctx, tmp, env, cloneArgs...); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("clone origin: %w (%s)", err, strings.TrimSpace(out))
	}
	repoPath := filepath.Join(tmp, "repo")

	// Fetch the stack's branches into local refs/heads so the reconciler sees them
	// as local branches (PushBranches re-pushes refs/heads/<branch>).
	prefix := sl.StackBranchPrefix(id)
	refspec := "refs/heads/" + prefix + "*:refs/heads/" + prefix + "*"
	fetchArgs := append(append([]string{}, cred...), "fetch", "--no-tags", "origin", refspec)
	if out, err := runGit(ctx, repoPath, env, fetchArgs...); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("fetch stack branches %s: %w (%s)", refspec, err, strings.TrimSpace(out))
	}
	return repoPath, cleanup, nil
}
