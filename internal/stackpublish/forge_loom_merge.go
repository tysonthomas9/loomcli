package stackpublish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
)

func (g *GitHubForge) MergeLoomPull(ctx context.Context, owner, repo string, number int, head string) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge-async", owner, repo, number)
	status, data, _, err := g.do(ctx, http.MethodPut, path, map[string]string{
		"merge_method": "squash", "merge_action": "default", "sha": head,
	})
	if err != nil {
		return err
	}
	if status != http.StatusAccepted && status != http.StatusOK {
		return g.apiErr("PUT", path, status, data)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("github loom merge decode: %w", err)
	}
	if result.Status != "pending" && result.Status != "merged" {
		return fmt.Errorf("github loom merge returned %q", result.Status)
	}
	return nil
}

func (g *GitHubForge) DeleteLoomBranch(ctx context.Context, owner, repo, branch string) error {
	path := fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", owner, repo, url.PathEscape(branch))
	status, data, _, err := g.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent || status == http.StatusNotFound {
		return nil
	}
	return g.apiErr("DELETE", path, status, data)
}

func (g *GitHubForge) FailedLoomChecks(ctx context.Context, owner, repo, head string) ([]string, error) {
	runs, err := g.failedCheckRuns(ctx, owner, repo, head)
	if err != nil {
		return nil, err
	}
	statuses, err := g.failedCommitStatuses(ctx, owner, repo, head)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, name := range append(runs, statuses...) {
		if name != "" {
			set[name] = true
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 5 {
		names = names[:5]
	}
	return names, nil
}

func (g *GitHubForge) failedCheckRuns(ctx context.Context, owner, repo, head string) ([]string, error) {
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", owner, repo, url.PathEscape(head))
	status, data, _, err := g.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, g.apiErr("GET", path, status, data)
	}
	var result struct {
		Runs []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	var failed []string
	for _, run := range result.Runs {
		switch run.Conclusion {
		case "failure", "timed_out", "cancelled", "action_required", "stale":
			failed = append(failed, run.Name)
		}
	}
	return failed, nil
}

func (g *GitHubForge) failedCommitStatuses(ctx context.Context, owner, repo, head string) ([]string, error) {
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/status", owner, repo, url.PathEscape(head))
	status, data, _, err := g.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, g.apiErr("GET", path, status, data)
	}
	var result struct {
		Statuses []struct {
			Context string `json:"context"`
			State   string `json:"state"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	var failed []string
	for _, item := range result.Statuses {
		if item.State == "failure" || item.State == "error" {
			failed = append(failed, item.Context)
		}
	}
	return failed, nil
}
