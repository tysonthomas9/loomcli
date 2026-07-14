package prreview

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

var ownerRepoSegmentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var pullNumberRE = regexp.MustCompile(`^[0-9]+$`)

type pullRequestPath struct {
	owner  string
	repo   string
	number int
}

func parsePullRequestPath(owner, repo, number string) (pullRequestPath, bool) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	number = strings.TrimSpace(number)
	if !ownerRepoSegmentRE.MatchString(owner) || !ownerRepoSegmentRE.MatchString(repo) || !pullNumberRE.MatchString(number) {
		return pullRequestPath{}, false
	}
	n, err := strconv.Atoi(number)
	if err != nil || n <= 0 {
		return pullRequestPath{}, false
	}
	return pullRequestPath{owner: owner, repo: repo, number: n}, true
}

func parseGitHubOwnerRepo(remoteURL string) (owner, repo string, ok bool) {
	raw := strings.TrimSpace(remoteURL)
	if raw == "" {
		return "", "", false
	}
	if strings.HasPrefix(raw, "git@github.com:") {
		path := strings.TrimPrefix(raw, "git@github.com:")
		return splitGitHubPath(path)
	}
	parsed, err := url.Parse(raw)
	if err != nil || !supportedGitHubURLScheme(parsed.Scheme) || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", "", false
	}
	return splitGitHubPath(parsed.Path)
}

func supportedGitHubURLScheme(scheme string) bool {
	return strings.EqualFold(scheme, "https") || strings.EqualFold(scheme, "ssh")
}

func splitGitHubPath(path string) (owner, repo string, ok bool) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	owner = strings.TrimSpace(parts[0])
	repo = strings.TrimSpace(parts[1])
	if !ownerRepoSegmentRE.MatchString(owner) || !ownerRepoSegmentRE.MatchString(repo) {
		return "", "", false
	}
	return owner, repo, true
}

// workspaceHasRepo checks the requested owner/repo against the workspace's
// registered repos case-insensitively and, on a match, returns the CANONICAL
// owner/repo exactly as registered. Callers must use the canonical pair for
// the grant resource/pattern and the dispatch resource so a non-canonical-cased
// request can never seed a grant whose (case-sensitive) pattern fails to match
// the later canonical dispatch.
func (m *Module) workspaceHasRepo(ctx context.Context, ws, owner, repo string) (canonOwner, canonRepo string, ok bool, err error) {
	data, buildErr := storeadapter.BuildWorkspaceDataForKey(ctx, m.store, ws)
	if buildErr != nil {
		return "", "", false, buildErr
	}
	for _, workspaceRepo := range data.Repos {
		gotOwner, gotRepo, parsed := parseGitHubOwnerRepo(workspaceRepo.RemoteURL)
		if !parsed {
			continue
		}
		if strings.EqualFold(gotOwner, owner) && strings.EqualFold(gotRepo, repo) {
			return gotOwner, gotRepo, true, nil
		}
	}
	return "", "", false, nil
}
