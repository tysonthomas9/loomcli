package stackpublish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DependencyCheckName is the status Loom keeps on PRs whose change waits for
// changes in other repositories.
const DependencyCheckName = "loom/dependencies"

// DependencyStatus is one loom/dependencies result: State is pending or success.
type DependencyStatus struct {
	State, Description string
}

// PostDependencyStatus posts loom/dependencies on a commit. It posts a check run
// when the credential is a GitHub App installation, and a commit status
// otherwise (GitHub refuses check runs from any other credential).
func (g *GitHubForge) PostDependencyStatus(ctx context.Context, owner, repo, sha string, status DependencyStatus) error {
	run := map[string]any{"name": DependencyCheckName, "head_sha": sha, "status": "in_progress",
		"output": map[string]string{"title": status.Description, "summary": status.Description}}
	if status.State == "success" {
		run["status"], run["conclusion"] = "completed", "success"
	}
	path := fmt.Sprintf("/repos/%s/%s/check-runs", owner, repo)
	code, data, header, err := g.do(ctx, http.MethodPost, path, run)
	if err != nil {
		return err
	}
	if code == http.StatusCreated {
		return nil
	}
	if code != http.StatusForbidden && code != http.StatusNotFound || rateLimited(header, data) {
		return g.apiErr("POST", path, code, data)
	}
	description := status.Description
	if len(description) > 140 {
		description = description[:137] + "..."
	}
	path = fmt.Sprintf("/repos/%s/%s/statuses/%s", owner, repo, sha)
	code, data, _, err = g.do(ctx, http.MethodPost, path, map[string]string{
		"state": status.State, "context": DependencyCheckName, "description": description})
	if err != nil {
		return err
	}
	if code != http.StatusCreated {
		return g.apiErr("POST", path, code, data)
	}
	return nil
}

func rateLimited(header http.Header, data []byte) bool {
	return header.Get("X-RateLimit-Remaining") == "0" || strings.Contains(strings.ToLower(string(data)), "rate limit")
}

const mergeQueueHeadQuery = `query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){pullRequest(number:$number){mergeQueueEntry{headCommit{oid}}}}}`

// MergeQueueHead returns the temporary merge-group commit GitHub's merge queue
// built for a PR, or "" when the PR is not queued.
func (g *GitHubForge) MergeQueueHead(ctx context.Context, owner, repo string, number int) (string, error) {
	vars := map[string]any{"owner": owner, "repo": repo, "number": number}
	code, data, _, err := g.do(ctx, http.MethodPost, "/graphql", map[string]any{"query": mergeQueueHeadQuery, "variables": vars})
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", g.apiErr("POST", "/graphql", code, data)
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					MergeQueueEntry *struct {
						HeadCommit *struct {
							OID string `json:"oid"`
						} `json:"headCommit"`
					} `json:"mergeQueueEntry"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("github graphql merge queue decode: %w", err)
	}
	if len(resp.Errors) > 0 {
		return "", g.apiErr("POST", "/graphql", code, []byte(resp.Errors[0].Message))
	}
	entry := resp.Data.Repository.PullRequest.MergeQueueEntry
	if entry == nil || entry.HeadCommit == nil {
		return "", nil
	}
	return entry.HeadCommit.OID, nil
}

// DependencyEnforcement reports whether branch protection or a ruleset on the
// branch requires loom/dependencies: enforced (required and pinned to an app),
// not_pinned (required from any source) or not_enforced. Loom only reads it.
func (g *GitHubForge) DependencyEnforcement(ctx context.Context, owner, repo, branch string) (string, error) {
	classicRequired, classicPinned, err := g.classicDependencyRequirement(ctx, owner, repo, branch)
	if err != nil {
		return "", err
	}
	rulesRequired, rulesPinned, err := g.rulesetDependencyRequirement(ctx, owner, repo, branch)
	if err != nil {
		return "", err
	}
	switch {
	case classicPinned || rulesPinned:
		return "enforced", nil
	case classicRequired || rulesRequired:
		return "not_pinned", nil
	default:
		return "not_enforced", nil
	}
}

func (g *GitHubForge) getJSON(ctx context.Context, path string, out any) error {
	code, data, _, err := g.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return g.apiErr("GET", path, code, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("github %s decode: %w", path, err)
	}
	return nil
}

func (g *GitHubForge) classicDependencyRequirement(ctx context.Context, owner, repo, branch string) (bool, bool, error) {
	var classic struct {
		Protection struct {
			RequiredStatusChecks struct {
				Contexts []string `json:"contexts"`
				Checks   []struct {
					Context string `json:"context"`
					AppID   *int64 `json:"app_id"`
				} `json:"checks"`
			} `json:"required_status_checks"`
		} `json:"protection"`
	}
	if err := g.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/branches/%s", owner, repo, branch), &classic); err != nil {
		return false, false, err
	}
	required, pinned := false, false
	for _, context := range classic.Protection.RequiredStatusChecks.Contexts {
		required = required || context == DependencyCheckName
	}
	for _, check := range classic.Protection.RequiredStatusChecks.Checks {
		if check.Context == DependencyCheckName {
			required, pinned = true, pinned || check.AppID != nil && *check.AppID > 0
		}
	}
	return required, pinned, nil
}

func (g *GitHubForge) rulesetDependencyRequirement(ctx context.Context, owner, repo, branch string) (bool, bool, error) {
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			Checks []struct {
				Context       string `json:"context"`
				IntegrationID *int64 `json:"integration_id"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := g.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/rules/branches/%s", owner, repo, branch), &rules); err != nil {
		return false, false, err
	}
	required, pinned := false, false
	for _, rule := range rules {
		for _, check := range rule.Parameters.Checks {
			if rule.Type == "required_status_checks" && check.Context == DependencyCheckName {
				required, pinned = true, pinned || check.IntegrationID != nil && *check.IntegrationID > 0
			}
		}
	}
	return required, pinned, nil
}
