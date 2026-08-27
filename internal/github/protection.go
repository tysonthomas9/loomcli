// Package github contains small, dependency-injected GitHub command helpers.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// RepositorySlug extracts owner/name from common GitHub remote URLs.
func RepositorySlug(remote string) (string, bool) {
	s := strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	s = strings.TrimPrefix(s, "git@github.com:")
	if u, err := url.Parse(s); err == nil && u.Host == "github.com" {
		s = strings.TrimPrefix(u.Path, "/")
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// Runner is the narrow command seam used by merge commands and tests.
type Runner func(context.Context, string, ...string) (stdout, stderr string, err error)

// PRProtection verifies that a pull request's base is protected. GitHub is the
// authority; missing, malformed, or failed responses are all merge-disabled.
func PRProtection(ctx context.Context, run Runner, pr string) error {
	out, stderr, err := run(ctx, "gh", "pr", "view", pr, "--json", "baseRefName,repository")
	if err != nil {
		return commandError("reading pull request", stderr, err)
	}
	var view struct {
		BaseRefName string `json:"baseRefName"`
		Repository  struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil || view.BaseRefName == "" || view.Repository.NameWithOwner == "" {
		return errors.New("merge disabled: GitHub pull request protection could not be verified")
	}
	return BranchProtection(ctx, run, view.Repository.NameWithOwner, view.BaseRefName)
}

// RepositoryProtection verifies the configured repository's default branch.
func RepositoryProtection(ctx context.Context, run Runner) error {
	out, stderr, err := run(ctx, "gh", "repo", "view", "--json", "nameWithOwner,defaultBranchRef")
	if err != nil {
		return commandError("reading repository", stderr, err)
	}
	var view struct {
		NameWithOwner    string `json:"nameWithOwner"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil || view.NameWithOwner == "" || view.DefaultBranchRef.Name == "" {
		return errors.New("merge disabled: GitHub default-branch protection could not be verified")
	}
	return BranchProtection(ctx, run, view.NameWithOwner, view.DefaultBranchRef.Name)
}

// BranchProtection treats any inability to prove protection as unsafe.
func BranchProtection(ctx context.Context, run Runner, repo, branch string) error {
	if !strings.Contains(repo, "/") || strings.ContainsAny(repo+branch, " \t\n") {
		return errors.New("merge disabled: invalid GitHub repository or branch")
	}
	_, stderr, err := run(ctx, "gh", "api", "repos/"+repo+"/branches/"+url.PathEscape(branch)+"/protection")
	if err != nil {
		return commandError("verifying default-branch protection", stderr, err)
	}
	return nil
}

func commandError(action, stderr string, err error) error {
	if strings.TrimSpace(stderr) != "" {
		return fmt.Errorf("merge disabled: %s: %s", action, strings.TrimSpace(stderr))
	}
	return fmt.Errorf("merge disabled: %s: %w", action, err)
}
