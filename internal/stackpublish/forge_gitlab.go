package stackpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// GitLabForge reads merge requests and keeps loom/dependencies on GitLab: a
// commit status named loom/dependencies on every SHA, plus the response to a
// project external status check of the same name when one is configured. It
// only reads project settings; it never changes approval rules or branches.
type GitLabForge struct {
	token, baseURL string // baseURL is the API root, e.g. https://gitlab.com/api/v4
	client         *http.Client
}

func NewGitLabForge(token string, client *http.Client, baseURL string) *GitLabForge {
	if client == nil {
		client = http.DefaultClient
	}
	return &GitLabForge{token: token, baseURL: strings.TrimSuffix(baseURL, "/"), client: client}
}

// providerCall sends one JSON request and returns the status and body.
func providerCall(ctx context.Context, client *http.Client, token, method, target string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "loom-stack-publisher")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, req.URL.Path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	return res.StatusCode, data, err
}

func providerErr(provider, token, method, target string, code int, data []byte) error {
	return fmt.Errorf("%s %s %s: %d: %s", provider, method, target, code, strings.TrimSpace(scrubSecrets(string(data), token)))
}

// call decodes a response with the expected status into out; any other status
// is an error, so unknown provider answers fail closed.
func (g *GitLabForge) call(ctx context.Context, method, target string, body, out any, want int) error {
	code, data, err := providerCall(ctx, g.client, g.token, method, g.baseURL+target, body)
	if err != nil {
		return err
	}
	if code != want {
		return providerErr("gitlab", g.token, method, target, code, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("gitlab %s decode: %w", target, err)
		}
	}
	return nil
}

func gitlabProject(owner, repo string) string {
	return "/projects/" + url.PathEscape(owner+"/"+repo)
}

type gitlabMR struct {
	IID             int    `json:"iid"`
	State           string `json:"state"`
	SourceBranch    string `json:"source_branch"`
	TargetBranch    string `json:"target_branch"`
	SHA             string `json:"sha"`
	MergeCommitSHA  string `json:"merge_commit_sha"`
	SquashCommitSHA string `json:"squash_commit_sha"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	WebURL          string `json:"web_url"`
}

// toPR maps an MR. Only state "merged" is merged; an MR waiting in a merge
// train is still "opened". Unknown states are errors.
func (mr gitlabMR) toPR() (PR, error) {
	pr := PR{Number: mr.IID, Head: mr.SourceBranch, HeadSHA: mr.SHA, Base: mr.TargetBranch,
		Title: mr.Title, Body: mr.Description, URL: mr.WebURL}
	switch mr.State {
	case "opened", "locked":
		pr.State = "open"
	case "closed":
		pr.State = "closed"
	case "merged":
		pr.State, pr.Merged = "closed", true
		pr.MergeCommitSHA = mr.MergeCommitSHA
		if pr.MergeCommitSHA == "" {
			pr.MergeCommitSHA = mr.SquashCommitSHA
		}
		if pr.MergeCommitSHA == "" {
			pr.MergeCommitSHA = mr.SHA // fast-forward merge: the head itself landed
		}
	default:
		return PR{}, fmt.Errorf("gitlab merge request !%d has unknown state %q", mr.IID, mr.State)
	}
	return pr, nil
}

func (g *GitLabForge) PullByNumber(ctx context.Context, owner, repo string, number int) (PR, error) {
	var mr gitlabMR
	if err := g.call(ctx, http.MethodGet, fmt.Sprintf("%s/merge_requests/%d", gitlabProject(owner, repo), number), nil, &mr, http.StatusOK); err != nil {
		return PR{}, err
	}
	return mr.toPR()
}

func (g *GitLabForge) PullsForCommit(ctx context.Context, owner, repo, sha string) ([]PR, error) {
	var mrs []gitlabMR
	if err := g.call(ctx, http.MethodGet, fmt.Sprintf("%s/repository/commits/%s/merge_requests", gitlabProject(owner, repo), sha), nil, &mrs, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]PR, 0, len(mrs))
	for _, mr := range mrs {
		pr, err := mr.toPR()
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, nil
}

// MergeQueueHead returns the merge-result commit a GitLab merge train is
// testing for the MR, or "" when the MR is not in a train.
func (g *GitLabForge) MergeQueueHead(ctx context.Context, owner, repo string, number int) (string, error) {
	target := fmt.Sprintf("%s/merge_trains/merge_requests/%d", gitlabProject(owner, repo), number)
	code, data, err := providerCall(ctx, g.client, g.token, http.MethodGet, g.baseURL+target, nil)
	if err != nil {
		return "", err
	}
	if code == http.StatusNotFound {
		return "", nil
	}
	if code != http.StatusOK {
		return "", providerErr("gitlab", g.token, "GET", target, code, data)
	}
	var car struct {
		Status   string `json:"status"`
		Pipeline *struct {
			SHA string `json:"sha"`
		} `json:"pipeline"`
	}
	if err := json.Unmarshal(data, &car); err != nil {
		return "", fmt.Errorf("gitlab merge train decode: %w", err)
	}
	switch car.Status {
	case "merged", "skip_merged":
		return "", nil
	case "idle", "stale", "fresh", "merging":
		if car.Pipeline == nil || car.Pipeline.SHA == "" {
			return "", nil // queued, but the train has not built its merge commit yet
		}
		return car.Pipeline.SHA, nil
	default:
		return "", fmt.Errorf("gitlab merge train for !%d has unknown status %q", number, car.Status)
	}
}

type gitlabStatusCheck struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	ProtectedBranches []struct {
		Name string `json:"name"`
	} `json:"protected_branches"`
}

// dependencyStatusChecks lists the project's external status checks named
// loom/dependencies. 404 means the project has none (or no such feature).
func (g *GitLabForge) dependencyStatusChecks(ctx context.Context, project string) ([]gitlabStatusCheck, error) {
	target := project + "/external_status_checks"
	code, data, err := providerCall(ctx, g.client, g.token, http.MethodGet, g.baseURL+target, nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, providerErr("gitlab", g.token, "GET", target, code, data)
	}
	var checks []gitlabStatusCheck
	if err := json.Unmarshal(data, &checks); err != nil {
		return nil, fmt.Errorf("gitlab external status checks decode: %w", err)
	}
	named := checks[:0]
	for _, check := range checks {
		if check.Name == DependencyCheckName {
			named = append(named, check)
		}
	}
	return named, nil
}

// PostDependencyStatus sets the loom/dependencies commit status on sha and
// answers a loom/dependencies external status check on every open MR whose
// head is sha. State must be pending or success. GitLab identifies Loom's
// status by its exact name, so there is no app ID (0).
func (g *GitLabForge) PostDependencyStatus(ctx context.Context, owner, repo, sha string, status DependencyStatus) (int64, error) {
	return 0, g.postDependencyStatus(ctx, owner, repo, sha, status)
}

func (g *GitLabForge) postDependencyStatus(ctx context.Context, owner, repo, sha string, status DependencyStatus) error {
	states, ok := map[string][2]string{"pending": {"pending", "pending"}, "success": {"success", "passed"}}[status.State]
	if !ok {
		return fmt.Errorf("gitlab: unknown loom/dependencies state %q", status.State)
	}
	commitState, checkState := states[0], states[1]
	project := gitlabProject(owner, repo)
	target := fmt.Sprintf("%s/statuses/%s", project, sha)
	code, data, err := providerCall(ctx, g.client, g.token, http.MethodPost, g.baseURL+target,
		map[string]string{"state": commitState, "name": DependencyCheckName, "description": status.Description})
	if err != nil {
		return err
	}
	// GitLab refuses to re-post a status already in that state; the commit
	// already shows what we want.
	sameState := code == http.StatusBadRequest && strings.Contains(string(data), "Cannot transition status") &&
		strings.Contains(string(data), "from :"+commitState+" ")
	if code != http.StatusCreated && !sameState {
		return providerErr("gitlab", g.token, "POST", target, code, data)
	}
	checks, err := g.dependencyStatusChecks(ctx, project)
	if err != nil || len(checks) == 0 {
		return err
	}
	mrs, err := g.PullsForCommit(ctx, owner, repo, sha)
	if err != nil {
		return err
	}
	for _, mr := range mrs {
		if mr.State != "open" || mr.HeadSHA != sha {
			continue
		}
		for _, check := range checks {
			if err := g.call(ctx, http.MethodPost, fmt.Sprintf("%s/merge_requests/%d/status_check_responses", project, mr.Number),
				map[string]any{"sha": sha, "external_status_check_id": check.ID, "status": checkState}, nil, http.StatusCreated); err != nil {
				return err
			}
		}
	}
	return nil
}

// DependencyEnforcement is not_pinned when a loom/dependencies external status
// check covers branch and the project requires status checks to pass (GitLab
// matches by name only, so any Developer could answer it), and not_enforced
// otherwise. GitLab has no app pinning, so loomApp is unused.
func (g *GitLabForge) DependencyEnforcement(ctx context.Context, owner, repo, branch string, _ int64) (string, error) {
	project := gitlabProject(owner, repo)
	var settings struct {
		AllStatusChecksPassed *bool `json:"only_allow_merge_if_all_status_checks_passed"`
	}
	if err := g.call(ctx, http.MethodGet, project, nil, &settings, http.StatusOK); err != nil {
		return "", err
	}
	if settings.AllStatusChecksPassed == nil || !*settings.AllStatusChecksPassed {
		return "not_enforced", nil
	}
	checks, err := g.dependencyStatusChecks(ctx, project)
	if err != nil {
		return "", err
	}
	for _, check := range checks {
		if len(check.ProtectedBranches) == 0 {
			return "not_pinned", nil
		}
		for _, protected := range check.ProtectedBranches {
			if matched, _ := path.Match(protected.Name, branch); matched {
				return "not_pinned", nil
			}
		}
	}
	return "not_enforced", nil
}
