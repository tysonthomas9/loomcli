package stackpublish

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// callBoundDepthKey marks a context already under Session.Do / boundLocal so
// nested git does not re-enter (PushBranches → runGit). Outer Do still tracks
// forge/push uncertainty; boundLocal does not poison on settled local exits.
type callBoundDepthKey struct{}

// envWith returns the current process environment, for git subprocesses that
// also need an extra credential variable appended.
func envWith() []string { return os.Environ() }

// runGit runs one git subprocess. Every invocation has a ≤60s deadline. While a
// publish admission session is held (and not already inside Do/boundLocal):
//   - git push uses Session.Do (conservative uncertain on any post-start error)
//   - all other commands use boundLocal (renew + lease bound; settled local
//     nonzero exits do not poison admission)
func runGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	if ctx.Value(callBoundDepthKey{}) != nil {
		return execGit(ctx, dir, env, args...)
	}
	if sess := SessionFrom(ctx); sess != nil {
		var out string
		run := func(cctx context.Context) error {
			var e error
			out, e = execGit(cctx, dir, env, args...)
			return e
		}
		if gitInvocationIsPush(args) {
			// Product pushes normally arrive via leasingForge.PushBranches → Do
			// (depth key set). This path is the failsafe if push is invoked
			// under a Session without an outer Do.
			err := sess.Do(ctx, run)
			return out, err
		}
		err := sess.boundLocal(ctx, run)
		return out, err
	}
	cctx, cancel := context.WithTimeout(ctx, domain.MaxStackPublishCallBound)
	defer cancel()
	return execGit(cctx, dir, env, args...)
}

// gitInvocationIsPush reports whether args are a `git push` (after -c/-C and
// similar option pairs). Used so remote-mutating push never skips Session.Do.
func gitInvocationIsPush(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || a == "-C" || a == "--git-dir" || a == "--work-tree":
			i++ // skip option value
		case a == "push":
			return true
		case strings.HasPrefix(a, "-"):
			// other flags; keep scanning for the verb
		default:
			return a == "push"
		}
	}
	return false
}

func execGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed executable; args controlled by publisher
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(scrubSecrets(string(out))))
	}
	return string(out), nil
}

var repoSlugRe = regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/\s]+?)(?:\.git)?/?$`)

// repoSlug parses owner/repo from the repo's origin remote URL (ssh or https).
func repoSlug(ctx context.Context, dir string) (owner, repo string, err error) {
	out, err := runGit(ctx, dir, nil, "remote", "get-url", "origin")
	if err != nil {
		return "", "", err
	}
	m := repoSlugRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return "", "", fmt.Errorf("stackpublish: cannot parse owner/repo from origin %q", strings.TrimSpace(out))
	}
	return m[1], m[2], nil
}

// commitsBetween returns the number of commits in base..head (head commits not in
// base). Zero means head adds nothing on top of base (an empty unit).
func commitsBetween(ctx context.Context, dir, base, head string) (int, error) {
	out, err := runGit(ctx, dir, nil, "rev-list", "--count", base+".."+head)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("stackpublish: rev-list count %q: %w", strings.TrimSpace(out), err)
	}
	return n, nil
}

// fetchRef updates a single remote ref (e.g. the live RootBase) so subsequent
// ancestry checks reflect post-merge reality.
func fetchRef(ctx context.Context, dir, remote, ref string) error {
	_, err := runGit(ctx, dir, nil, "fetch", remote, ref)
	return err
}

// isAncestor reports whether `ancestor` is an ancestor of `descendant`. A clean
// exit-1 means "no" (not an error); any other failure (e.g. an unresolvable ref)
// is returned so callers can fail closed. Bound like other local git subprocesses.
func isAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	run := func(cctx context.Context) (bool, error) {
		cmd := exec.CommandContext(cctx, "git", "-C", dir, "merge-base", "--is-ancestor", ancestor, descendant) //nolint:gosec // fixed executable; controlled args
		err := cmd.Run()
		if err == nil {
			return true, nil
		}
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
	}
	if ctx.Value(callBoundDepthKey{}) != nil {
		return run(ctx)
	}
	if sess := SessionFrom(ctx); sess != nil {
		var ok bool
		err := sess.boundLocal(ctx, func(cctx context.Context) error {
			var e error
			ok, e = run(cctx)
			return e
		})
		return ok, err
	}
	cctx, cancel := context.WithTimeout(ctx, domain.MaxStackPublishCallBound)
	defer cancel()
	return run(cctx)
}

// headSHA returns the commit SHA a local ref points at.
func headSHA(ctx context.Context, dir, ref string) (string, error) {
	out, err := runGit(ctx, dir, nil, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ownedCommitText returns the owned commit at ref as a subject/body pair — the
// fallback source for a stacked PR's title/body when no issue metadata is
// available. The format is NUL-delimited so a multi-line body can't be confused
// with the subject.
func ownedCommitText(ctx context.Context, dir, ref string) (commitText, error) {
	out, err := runGit(ctx, dir, nil, "show", "-s", "--format=%s%x00%b", ref)
	if err != nil {
		return commitText{}, err
	}
	parts := strings.SplitN(out, "\x00", 2)
	ct := commitText{Subject: strings.TrimSpace(parts[0])}
	if len(parts) == 2 {
		ct.Body = strings.TrimSpace(parts[1])
	}
	return ct, nil
}
