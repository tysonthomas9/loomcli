package stackpublish

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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

type OriginKind string

const (
	OriginKindGitHub OriginKind = "github"
	OriginKindLocal  OriginKind = "local"
)

type Origin struct {
	Kind  OriginKind
	URL   string
	Owner string
	Repo  string
}

// RepoOriginURL returns the repo's configured origin URL.
func RepoOriginURL(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, nil, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func cannotParseOrigin(raw string) error {
	return fmt.Errorf("stackpublish: cannot parse owner/repo from origin %q", strings.TrimSpace(raw))
}

// ClassifyOriginURL identifies the only origins stackpublish knows how to use:
// GitHub remotes, or local filesystem origins expressed as absolute paths or
// local file:// URLs. Everything else fails closed.
func ClassifyOriginURL(raw string) (Origin, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Origin{}, cannotParseOrigin(raw)
	}
	if m := repoSlugRe.FindStringSubmatch(raw); m != nil {
		return Origin{Kind: OriginKindGitHub, URL: raw, Owner: m[1], Repo: m[2]}, nil
	}
	if filepath.IsAbs(raw) {
		return Origin{Kind: OriginKindLocal, URL: raw}, nil
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme == "file" && u.RawQuery == "" && u.Fragment == "" {
		if u.Host != "" && u.Host != "localhost" {
			return Origin{}, cannotParseOrigin(raw)
		}
		if filepath.IsAbs(u.Path) {
			return Origin{Kind: OriginKindLocal, URL: raw}, nil
		}
	}
	return Origin{}, cannotParseOrigin(raw)
}

// NewForgeForOrigin constructs the forge allowed for originURL.
func NewForgeForOrigin(originURL, token string) (Forge, Origin, error) {
	origin, err := ClassifyOriginURL(originURL)
	if err != nil {
		return nil, Origin{}, err
	}
	switch origin.Kind {
	case OriginKindGitHub:
		return NewGitHubForge(token, nil, ""), origin, nil
	case OriginKindLocal:
		return NewLocalForge(origin.URL), origin, nil
	default:
		return nil, Origin{}, cannotParseOrigin(originURL)
	}
}

// NewForgeForRepo reads repoPath's origin and constructs the matching forge.
func NewForgeForRepo(ctx context.Context, repoPath, token string) (Forge, Origin, error) {
	originURL, err := RepoOriginURL(ctx, repoPath)
	if err != nil {
		return nil, Origin{}, err
	}
	return NewForgeForOrigin(originURL, token)
}

// repoSlug parses owner/repo from the repo's GitHub origin remote URL (ssh or https).
func repoSlug(ctx context.Context, dir string) (owner, repo string, err error) {
	raw, err := RepoOriginURL(ctx, dir)
	if err != nil {
		return "", "", err
	}
	origin, err := ClassifyOriginURL(raw)
	if err != nil {
		return "", "", err
	}
	if origin.Kind != OriginKindGitHub {
		return "", "", cannotParseOrigin(raw)
	}
	m := repoSlugRe.FindStringSubmatch(raw)
	if m == nil {
		return "", "", cannotParseOrigin(raw)
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
