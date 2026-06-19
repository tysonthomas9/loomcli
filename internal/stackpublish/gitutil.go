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

// headSHA returns the commit SHA a local ref points at.
func headSHA(ctx context.Context, dir, ref string) (string, error) {
	out, err := runGit(ctx, dir, nil, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
