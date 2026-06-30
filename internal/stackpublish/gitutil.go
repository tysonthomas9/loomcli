package stackpublish

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// envWith returns the current process environment, for git subprocesses that
// also need an extra credential variable appended.
func envWith() []string { return os.Environ() }

func runGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
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
// is returned so callers can fail closed.
func isAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", ancestor, descendant) //nolint:gosec // fixed executable; controlled args
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
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
