package agentsv1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// GitHubReader serves one github_read op on the GitHub repo registered for
// repoPath in workspace ws, with the host's own GitHub credential. Serve's
// host GitHub connector implements it; the credential never leaves it.
type GitHubReader = func(ctx context.Context, ws, agentID, repoPath, op string, args map[string]any) (map[string]any, error)

// GitHubReadBody is one github_read call. It has no repo, method, path or
// query string: the repo is the calling agent's, and op picks one of the
// allowlisted reads.
type GitHubReadBody struct {
	Op      string `json:"op" jsonschema:"the read: pr_view, pr_files (the diff), pr_list, pr_search, pr_reviews, pr_review_comments, issue_view, issue_list, issue_search, issue_comments, check_runs, commit_status, run_list, run_view, run_jobs, job_log, repo_view, release_list, release_view, release_latest, commit_list, commit_view, compare, branch_list, contents, assignees or stack_health"`
	Number  int    `json:"number,omitempty" jsonschema:"the PR or issue number"`
	Ref     string `json:"ref,omitempty" jsonschema:"a commit SHA, branch or tag"`
	Base    string `json:"base,omitempty" jsonschema:"compare base, or pr_list base branch"`
	Head    string `json:"head,omitempty" jsonschema:"compare head, pr_list head (owner:branch), run_list head SHA, or the stack_health head-branch prefix"`
	State   string `json:"state,omitempty" jsonschema:"open, closed or all"`
	Labels  string `json:"labels,omitempty" jsonschema:"issue_list labels, comma-separated"`
	Branch  string `json:"branch,omitempty" jsonschema:"run_list branch"`
	Status  string `json:"status,omitempty" jsonschema:"run_list status"`
	Path    string `json:"path,omitempty" jsonschema:"a file or directory path in the repo"`
	Tag     string `json:"tag,omitempty" jsonschema:"release_view tag"`
	Run     int    `json:"run,omitempty" jsonschema:"a workflow run id"`
	Job     int    `json:"job,omitempty" jsonschema:"a workflow job id"`
	Query   string `json:"query,omitempty" jsonschema:"search text; the repo is added for you"`
	Page    int    `json:"page,omitempty" jsonschema:"the page to read, from next; 1 is the first"`
	PerPage int    `json:"perPage,omitempty" jsonschema:"items per page, at most 100"`
}

// GitHubReadResult is a github_read answer: GitHub's own field names, only
// the allowlisted ones. Next is the page to ask for next, "" on the last.
type GitHubReadResult struct {
	Op        string         `json:"op"`
	Item      map[string]any `json:"item,omitempty"`
	Items     []any          `json:"items,omitempty"`
	Text      string         `json:"text,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	Next      string         `json:"next,omitempty"`
}

// The typed github_read failures.
const (
	CodeGitHubInvalid     loomagent.Code = "github_invalid"      // an unknown op or bad argument
	CodeGitHubDenied      loomagent.Code = "github_denied"       // not this agent's tool or repo, or GitHub refused the host
	CodeGitHubNotFound    loomagent.Code = "github_not_found"    // GitHub has no such object
	CodeGitHubRateLimited loomagent.Code = "github_rate_limited" // retry later
	CodeGitHubUnavailable loomagent.Code = "github_unavailable"  // no host connector or credential, or GitHub failed
)

// WithGitHub sets the host reader github_read uses; nil refuses it.
func (h *Handler) WithGitHub(r GitHubReader) *Handler {
	h.github = r
	return h
}

// githubRead serves POST github/read for an agent with the github_read tool,
// on its own repo only.
func (h *Handler) githubRead(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	var in GitHubReadBody
	if _, err := envelope(w, r, &in); err != nil {
		return 0, nil, err
	}
	c, repo, err := h.githubAgent(r, s)
	if err != nil {
		return 0, nil, err
	}
	if h.github == nil {
		return 0, nil, &loomagent.Error{Code: CodeGitHubUnavailable, Message: "GitHub is not configured on this Loom host (no GitHub token in its settings), so github_read is unavailable"}
	}
	var args map[string]any // the set arguments, by their JSON names
	raw, _ := json.Marshal(in)
	_ = json.Unmarshal(raw, &args)
	delete(args, "op")
	body, err := h.github(r.Context(), middleware.WorkspaceFromContext(r.Context()), c, repo, in.Op, args)
	if err != nil {
		return 0, nil, githubError(err)
	}
	out := GitHubReadResult{Op: in.Op}
	out.Item, _ = body["item"].(map[string]any)
	out.Items, _ = body["items"].([]any)
	out.Text, _ = body["text"].(string)
	out.Truncated, _ = body["truncated"].(bool)
	out.Next, _ = body["next"].(string)
	return http.StatusOK, out, nil
}

// githubAgent is the calling agent and its repo, when it is an agent with
// the github_read tool.
func (h *Handler) githubAgent(r *http.Request, s *loomagent.Service) (agentID, repo string, err error) {
	c := actor(r)
	if c.Kind != "agent" {
		return "", "", &loomagent.Error{Code: CodeGitHubDenied, Message: "github_read is for agents' bridges"}
	}
	a, err := s.Get(r.Context(), c.ID)
	if err != nil {
		return "", "", err
	}
	p, err := h.presets.Get(r.Context(), a.Agent.Preset)
	if err != nil || !slices.Contains(p.Tools, "github_read") {
		return "", "", &loomagent.Error{Code: CodeGitHubDenied, Message: "this agent has no github_read tool"}
	}
	return c.ID, a.Agent.Repo, nil
}

// PRWatcher saves and removes an agent's watch on a PR of its own repo
// (OR10), reading GitHub through the host GitHub connector as the host's
// viewer; serve's prwatch.Service implements it.
type PRWatcher interface {
	Watch(ctx context.Context, ws, agentID, repoPath string, number int) (loomstore.PRWatch, bool, error)
	Unwatch(ctx context.Context, ws, agentID, repoPath string, number int) (bool, error)
}

// WithPRWatch sets the PR watcher github/watch uses; nil refuses it.
func (h *Handler) WithPRWatch(w PRWatcher) *Handler {
	h.prWatch = w
	return h
}

// GitHubWatchBody names a PR of the calling agent's repo.
type GitHubWatchBody struct {
	Number int `json:"number" jsonschema:"the PR number"`
}

// GitHubWatchResult is the watch github/watch saved, or whether
// github/unwatch removed one.
type GitHubWatchResult struct {
	Owner   string `json:"owner,omitempty"`
	Repo    string `json:"repo,omitempty"`
	Number  int    `json:"number"`
	Viewer  string `json:"viewer,omitempty"`
	Created bool   `json:"created,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

// githubWatch serves POST github/watch: the calling agent watches a PR of
// its own repo.
func (h *Handler) githubWatch(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	return h.githubWatchRoute(w, r, s, true)
}

// githubUnwatch serves POST github/unwatch.
func (h *Handler) githubUnwatch(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	return h.githubWatchRoute(w, r, s, false)
}

func (h *Handler) githubWatchRoute(w http.ResponseWriter, r *http.Request, s *loomagent.Service, watch bool) (int, any, error) {
	var in GitHubWatchBody
	if _, err := envelope(w, r, &in); err != nil {
		return 0, nil, err
	}
	agentID, repo, err := h.githubAgent(r, s)
	if err != nil {
		return 0, nil, err
	}
	if h.prWatch == nil {
		return 0, nil, &loomagent.Error{Code: CodeGitHubUnavailable, Message: "PR watches are unavailable on this Loom host (no host GitHub connector)"}
	}
	if in.Number <= 0 {
		return 0, nil, &loomagent.Error{Code: CodeGitHubInvalid, Message: "number must be a PR number"}
	}
	ws, out := middleware.WorkspaceFromContext(r.Context()), GitHubWatchResult{Number: in.Number}
	if !watch {
		if out.Removed, err = h.prWatch.Unwatch(r.Context(), ws, agentID, repo, in.Number); err != nil {
			return 0, nil, githubError(err)
		}
		return http.StatusOK, out, nil
	}
	pw, created, err := h.prWatch.Watch(r.Context(), ws, agentID, repo, in.Number)
	if err != nil {
		return 0, nil, githubError(err)
	}
	out.Owner, out.Repo, out.Viewer, out.Created = pw.Owner, pw.Repo, pw.Viewer, created
	return http.StatusOK, out, nil
}

// githubError types a host GitHub failure. Messages are the connector's,
// which never carry the credential.
func githubError(err error) error {
	var (
		rl *providers.RateLimited
		up *providers.UpstreamError
	)
	code := CodeGitHubUnavailable
	switch {
	case errors.Is(err, domain.ErrInvalid):
		code = CodeGitHubInvalid
	case errors.Is(err, domain.ErrNotOwner), errors.Is(err, domain.ErrGrantDenied):
		code = CodeGitHubDenied
	case errors.As(err, &rl):
		code = CodeGitHubRateLimited
	case errors.As(err, &up) && up.Status == http.StatusNotFound:
		code = CodeGitHubNotFound
	case errors.As(err, &up) && (up.Status == http.StatusUnauthorized || up.Status == http.StatusForbidden):
		code = CodeGitHubDenied
	}
	return &loomagent.Error{Code: code, Message: err.Error()}
}
