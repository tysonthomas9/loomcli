package stackpublish

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var repoSlugRe = regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/\s]+?)(?:\.git)?/?$`)

// repoSlug parses owner/repo from the repo's origin remote URL (ssh or https).
func repoSlug(ctx context.Context, dir string) (owner, repo string, err error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin") //nolint:gosec // fixed read-only argv
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git remote get-url origin: %w: %s", err, strings.TrimSpace(scrubSecrets(string(out))))
	}
	m := repoSlugRe.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", "", fmt.Errorf("stackpublish: cannot parse owner/repo from origin %q", strings.TrimSpace(string(out)))
	}
	return m[1], m[2], nil
}
