package leadapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

func epicRepoHint(epic *backend.IssueDetailData) string {
	if epic == nil {
		return ""
	}
	if hint := strings.TrimSpace(epic.SourceRepo); hint != "" {
		return hint
	}
	for _, label := range epic.Labels {
		if hint, ok := strings.CutPrefix(strings.TrimSpace(label), "repo:"); ok {
			return strings.TrimSpace(hint)
		}
	}
	return ""
}

func selectDispatchRepo(repos []*domain.Repo, hint string) *domain.Repo {
	hint = strings.TrimSpace(hint)
	if hint != "" {
		for _, repo := range repos {
			if repo != nil && (repo.Name == hint || repo.SourceRepoID == hint) {
				return repo
			}
		}
	}
	if len(repos) == 1 {
		return repos[0]
	}
	return nil
}

func (m *Module) resolveEpicRepo(ctx context.Context, ws string,
	epic *backend.IssueDetailData,
) (repoURL, baseBranch string, err error) {
	repos, err := m.store.Repos().List(ctx, ws)
	if err != nil {
		return "", "", fmt.Errorf("list workspace repos: %w", err)
	}
	repo := selectDispatchRepo(repos, epicRepoHint(epic))
	if repo == nil {
		return "", "", unresolvedRepoError("no workspace repo matches this epic")
	}
	remote := strings.TrimSpace(repo.RemoteURL)
	if remote == "" {
		remote = strings.TrimSpace(repo.Remote)
	}
	normalized, _, normalizeErr := placement.NormalizeRepoCloneRemote(remote)
	if normalizeErr != nil {
		return "", "", unresolvedRepoError("repo " + repo.Name + " has no usable https remote")
	}
	if err := requireGitHubRemote(normalized); err != nil {
		return "", "", unresolvedRepoError(
			"repo " + repo.Name + " is not a GitHub owner/repo remote; the daytona PR path requires https://github.com/<owner>/<repo>")
	}
	branch := strings.TrimSpace(repo.DefaultBranch)
	if branch == "" {
		branch = "main"
	}
	return normalized, branch, nil
}

// Every currently-allowlisted runner requires a clone URL; if a non-repo runner
// is ever allowlisted, resolveEpicRepo must branch BEFORE calling this helper.
func unresolvedRepoError(reason string) error {
	return newStatusError(http.StatusBadRequest, "repo_unresolved", reason, false)
}

func requireGitHubRemote(normalized string) error {
	remote := strings.TrimSpace(normalized)
	remote = strings.TrimSuffix(remote, ".git")
	rest, ok := strings.CutPrefix(remote, "https://github.com/")
	if !ok || strings.ContainsAny(rest, " \t\r\n") {
		return errors.New("not an exact GitHub HTTPS remote")
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("GitHub remote must contain one owner and one repo segment")
	}
	return nil
}
