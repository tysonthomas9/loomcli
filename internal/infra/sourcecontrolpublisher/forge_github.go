package stackpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultGitHubBaseURL is the GitHub REST API root.
const DefaultGitHubBaseURL = "https://api.github.com"

// GitHubForge is a repo-scoped GitHub implementation of Forge over the REST API.
// The token is used for API calls and is supplied to git push via an env-backed
// credential helper (never in argv), mirroring local-task-runner.ts.
type GitHubForge struct {
	token   string
	baseURL string
	client  *http.Client
}

var _ Forge = (*GitHubForge)(nil)

func (*GitHubForge) SupportsPullRequests() bool { return true }

// NewGitHubForge builds a forge. A nil client uses a 30s-timeout default; an
// empty baseURL uses the public API.
func NewGitHubForge(token string, client *http.Client, baseURL string) *GitHubForge {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if baseURL == "" {
		baseURL = DefaultGitHubBaseURL
	}
	return &GitHubForge{token: token, baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

type ghPull struct {
	Number   int     `json:"number"`
	State    string  `json:"state"`
	Title    string  `json:"title"`
	Body     string  `json:"body"`
	HTMLURL  string  `json:"html_url"`
	MergedAt *string `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (p ghPull) toPR() PR {
	return PR{
		Number: p.Number, Head: p.Head.Ref, Base: p.Base.Ref,
		State: p.State, Merged: p.MergedAt != nil && *p.MergedAt != "",
		Title: p.Title, Body: p.Body, URL: p.HTMLURL,
	}
}

var linkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func (g *GitHubForge) do(ctx context.Context, method, path string, body any) (int, []byte, http.Header, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	url := path
	if strings.HasPrefix(path, "/") {
		url = g.baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "loom-stack-publisher")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("github %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data, res.Header, nil
}

func (g *GitHubForge) apiErr(method, path string, status int, data []byte) error {
	return fmt.Errorf("github %s %s: %d: %s", method, path, status, strings.TrimSpace(scrubSecrets(string(data))))
}

func (g *GitHubForge) ListStackPRs(ctx context.Context, owner, repo, headPrefix string) ([]PR, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=all&per_page=100", owner, repo)
	var out []PR
	for path != "" {
		status, data, hdr, err := g.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, g.apiErr("GET", "pulls", status, data)
		}
		var pulls []ghPull
		if err := json.Unmarshal(data, &pulls); err != nil {
			return nil, fmt.Errorf("github list pulls decode: %w", err)
		}
		for _, p := range pulls {
			if strings.HasPrefix(p.Head.Ref, headPrefix) {
				out = append(out, p.toPR())
			}
		}
		path = ""
		if m := linkNextRe.FindStringSubmatch(hdr.Get("Link")); m != nil {
			path = m[1] // absolute next URL
		}
	}
	return out, nil
}

func (g *GitHubForge) CreatePR(ctx context.Context, owner, repo, head, base, title, body string) (PR, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls", owner, repo)
	status, data, _, err := g.do(ctx, http.MethodPost, path, map[string]any{
		"title": title, "head": head, "base": base, "body": body, "draft": false,
	})
	if err != nil {
		return PR{}, err
	}
	if status == http.StatusCreated {
		var p ghPull
		if err := json.Unmarshal(data, &p); err != nil {
			return PR{}, fmt.Errorf("github create pr decode: %w", err)
		}
		return p.toPR(), nil
	}
	// 422 commonly means a PR for this head already exists — adopt it.
	if status == http.StatusUnprocessableEntity {
		existing, lerr := g.ListStackPRs(ctx, owner, repo, head)
		if lerr == nil {
			for _, p := range existing {
				if p.Head == head && p.State == "open" {
					return p, nil
				}
			}
		}
	}
	return PR{}, g.apiErr("POST", "pulls", status, data)
}

func (g *GitHubForge) UpdatePRBase(ctx context.Context, owner, repo string, number int, base string) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	status, data, _, err := g.do(ctx, http.MethodPatch, path, map[string]any{"base": base})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return g.apiErr("PATCH", path, status, data)
	}
	return nil
}

func (g *GitHubForge) ClosePR(ctx context.Context, owner, repo string, number int, comment string) error {
	if strings.TrimSpace(comment) != "" {
		cpath := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
		_, _, _, _ = g.do(ctx, http.MethodPost, cpath, map[string]any{"body": comment}) // best-effort
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	status, data, _, err := g.do(ctx, http.MethodPatch, path, map[string]any{"state": "closed"})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return g.apiErr("PATCH", path, status, data)
	}
	return nil
}

const prGraphQuery = `query($owner:String!,$repo:String!,$cursor:String){repository(owner:$owner,name:$repo){pullRequests(states:OPEN,first:100,after:$cursor){nodes{number headRefName mergeable reviewDecision mergeQueueEntry{id} commits(last:1){nodes{commit{statusCheckRollup{state}}}}} pageInfo{hasNextPage endCursor}}}}`

type prGraphNode struct {
	Number          int     `json:"number"`
	HeadRefName     string  `json:"headRefName"`
	Mergeable       string  `json:"mergeable"`
	ReviewDecision  *string `json:"reviewDecision"`
	MergeQueueEntry *struct {
		ID string `json:"id"`
	} `json:"mergeQueueEntry"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// fetchPRNodes pages through all open PRs, returning the fields both merge-queue
// detection and status display need (one query serves both).
func (g *GitHubForge) fetchPRNodes(ctx context.Context, owner, repo string) ([]prGraphNode, error) {
	var out []prGraphNode
	var cursor *string
	for {
		vars := map[string]any{"owner": owner, "repo": repo, "cursor": cursor}
		status, data, _, err := g.do(ctx, http.MethodPost, "/graphql", map[string]any{"query": prGraphQuery, "variables": vars})
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, g.apiErr("POST", "/graphql", status, data)
		}
		var resp struct {
			Data struct {
				Repository struct {
					PullRequests struct {
						Nodes    []prGraphNode `json:"nodes"`
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
					} `json:"pullRequests"`
				} `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("github graphql pulls decode: %w", err)
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("github graphql pulls: %s", scrubSecrets(resp.Errors[0].Message))
		}
		pr := resp.Data.Repository.PullRequests
		out = append(out, pr.Nodes...)
		if !pr.PageInfo.HasNextPage || pr.PageInfo.EndCursor == "" {
			return out, nil
		}
		c := pr.PageInfo.EndCursor
		cursor = &c
	}
}

func (g *GitHubForge) QueuedPRNumbers(ctx context.Context, owner, repo string) (map[int]bool, error) {
	nodes, err := g.fetchPRNodes(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	out := map[int]bool{}
	for _, n := range nodes {
		if n.MergeQueueEntry != nil {
			out[n.Number] = true
		}
	}
	return out, nil
}

func (g *GitHubForge) PRStatuses(ctx context.Context, owner, repo, headPrefix string) (map[string]PRStatus, error) {
	nodes, err := g.fetchPRNodes(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	out := map[string]PRStatus{}
	for _, n := range nodes {
		if !strings.HasPrefix(n.HeadRefName, headPrefix) {
			continue
		}
		out[n.HeadRefName] = PRStatus{
			Number:    n.Number,
			Checks:    rollupToChecks(n),
			Review:    reviewToStatus(n.ReviewDecision),
			Mergeable: mergeableToStatus(n.Mergeable),
		}
	}
	return out, nil
}

func (g *GitHubForge) UpdatePRBody(ctx context.Context, owner, repo string, number int, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	status, data, _, err := g.do(ctx, http.MethodPatch, path, map[string]any{"body": body})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return g.apiErr("PATCH", path, status, data)
	}
	return nil
}

func rollupToChecks(n prGraphNode) string {
	if len(n.Commits.Nodes) == 0 || n.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return "none"
	}
	switch n.Commits.Nodes[0].Commit.StatusCheckRollup.State {
	case "SUCCESS":
		return "passing"
	case "FAILURE", "ERROR":
		return "failing"
	case "PENDING", "EXPECTED":
		return "pending"
	default:
		return "none"
	}
}

func reviewToStatus(d *string) string {
	if d == nil {
		return "none"
	}
	switch *d {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "REVIEW_REQUIRED":
		return "review_required"
	default:
		return "none"
	}
}

func mergeableToStatus(m string) string {
	switch m {
	case "MERGEABLE":
		return "mergeable"
	case "CONFLICTING":
		return "conflicting"
	default:
		return "unknown"
	}
}

// credHelper supplies the token to git over stdin-free env, never argv.
//
//nolint:gosec // G101: a git credential-helper script template that reads an env var, not a hardcoded credential.
const credHelper = `!f() { echo username=x-access-token; echo "password=$LOOM_PR_GIT_PASSWORD"; }; f`

func (g *GitHubForge) PushBranches(ctx context.Context, repoPath string, pushes []BranchPush) error {
	if len(pushes) == 0 {
		return nil
	}
	args := []string{
		"-c", "credential.helper=",
		"-c", "credential.helper=" + credHelper,
		"push", "--atomic", "origin",
	}
	for _, p := range pushes {
		args = append(args, "refs/heads/"+p.Branch+":refs/heads/"+p.Branch)
	}
	for _, p := range pushes {
		if p.ExpectedSHA != "" {
			// Explicit lease: assert the remote ref is exactly where we last left
			// it, robust to stale remote-tracking state (unlike a bare lease).
			args = append(args, "--force-with-lease=refs/heads/"+p.Branch+":"+p.ExpectedSHA)
		}
	}
	env := append(envWith(), "LOOM_PR_GIT_PASSWORD="+g.token, "GIT_TERMINAL_PROMPT=0")
	_, err := runGit(ctx, repoPath, env, args...)
	return err
}
