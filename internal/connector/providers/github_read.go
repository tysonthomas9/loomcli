package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ActionGitHubRead is the agents' read-only GitHub surface (github_read):
// args.op names one of GitHubReadOps, and only that op's GET is issued.
const ActionGitHubRead = "github.read"

// readOp is one allowlisted GET: a fixed path under the repo, the args it
// takes as query parameters, the array holding a list's items, and the
// response keys kept (at any depth); everything else is dropped.
type readOp struct {
	path  string            // under /repos/{owner}/{repo}, or absolute when it starts with /search or /users
	query map[string]string // arg -> GitHub query parameter
	list  string            // the key of the items array; "" when the body itself is the item(s)
	keys  string
	page  bool // takes page and perPage
}

const (
	userKeys   = "user login type html_url"
	prKeys     = "number state title draft merged_at html_url created_at updated_at closed_at labels name head base ref sha " + userKeys
	issueKeys  = "number state state_reason title body labels name assignees milestone comments pull_request html_url created_at updated_at closed_at " + userKeys
	commitKeys = "sha commit message author committer name date parents html_url"
	fileKeys   = "filename status additions deletions changes patch previous_filename"
	runKeys    = "id name display_title status conclusion head_branch head_sha event run_number run_attempt workflow_id html_url created_at updated_at"
)

// GitHubReadOps is the whole github_read surface (the R1–R11 inventory).
// Every op is a GET on the bound repo; there is no method, path or GraphQL
// argument. stack_health is served by the host forge, not this provider.
var GitHubReadOps = map[string]readOp{
	"pr_view": {path: "/pulls/{number}", keys: prKeys + " body mergeable mergeable_state merged requested_reviewers requested_teams slug assignees" +
		" additions deletions changed_files commits review_comments comments"},
	"pr_files":           {path: "/pulls/{number}/files", keys: fileKeys, page: true},
	"pr_list":            {path: "/pulls", query: map[string]string{"state": "state", "base": "base", "head": "head"}, keys: prKeys, page: true},
	"pr_search":          {path: "/search/issues", list: "items", keys: issueKeys, page: true},
	"pr_reviews":         {path: "/pulls/{number}/reviews", keys: "id state body submitted_at commit_id html_url " + userKeys, page: true},
	"pr_review_comments": {path: "/pulls/{number}/comments", keys: "id body path line original_line side diff_hunk in_reply_to_id commit_id pull_request_review_id created_at updated_at html_url " + userKeys, page: true},
	"issue_view":         {path: "/issues/{number}", keys: issueKeys},
	"issue_list":         {path: "/issues", query: map[string]string{"state": "state", "labels": "labels"}, keys: issueKeys, page: true},
	"issue_search":       {path: "/search/issues", list: "items", keys: issueKeys, page: true},
	"issue_comments":     {path: "/issues/{number}/comments", keys: "id body created_at updated_at html_url " + userKeys, page: true},
	"check_runs":         {path: "/commits/{ref}/check-runs", list: "check_runs", keys: "id name status conclusion started_at completed_at html_url details_url app slug output title summary", page: true},
	"commit_status":      {path: "/commits/{ref}/status", keys: "state sha total_count statuses context description target_url created_at updated_at"},
	"run_list":           {path: "/actions/runs", query: map[string]string{"branch": "branch", "status": "status", "head": "head_sha"}, list: "workflow_runs", keys: runKeys, page: true},
	"run_view":           {path: "/actions/runs/{run}", keys: runKeys},
	"run_jobs":           {path: "/actions/runs/{run}/jobs", list: "jobs", keys: "id run_id name status conclusion started_at completed_at html_url steps number", page: true},
	"job_log":            {path: "/actions/jobs/{job}/logs"},
	"repo_view":          {path: "", keys: "name full_name private visibility description default_branch html_url archived fork topics owner login pushed_at updated_at"},
	"release_list":       {path: "/releases", keys: "id tag_name name body draft prerelease created_at published_at html_url author login", page: true},
	"release_view":       {path: "/releases/tags/{tag}", keys: "id tag_name name body draft prerelease created_at published_at html_url author login assets size download_count"},
	"release_latest":     {path: "/releases/latest", keys: "id tag_name name body draft prerelease created_at published_at html_url author login"},
	"commit_list":        {path: "/commits", query: map[string]string{"ref": "sha", "path": "path"}, keys: commitKeys, page: true},
	"commit_view":        {path: "/commits/{ref}", keys: commitKeys + " stats total files " + fileKeys},
	"compare":            {path: "/compare/{base}...{head}", keys: "status ahead_by behind_by total_commits commits files " + commitKeys + " " + fileKeys, page: true},
	"branch_list":        {path: "/branches", keys: "name commit sha protected", page: true},
	"contents":           {path: "/contents/{path}", query: map[string]string{"ref": "ref"}, keys: "name path type size sha content encoding html_url"},
	"assignees":          {path: "/assignees", keys: userKeys, page: true},
	"user_view":          {path: "/users/{login}", keys: "login name type company blog location bio html_url public_repos created_at"},
}

// maxLogBytes bounds job_log: the log's last maxLogBytes are returned.
const maxLogBytes = 256 << 10

// foreignQualifier finds search qualifiers that would widen a search past the
// bound repo (GitHub ORs repeated repo: qualifiers).
var foreignQualifier = regexp.MustCompile(`(?i)(^|[\s(-])(repo|org|user|owner):`)

// githubRead serves one GitHubReadOps op on args.owner/args.repo. The Body
// is {op, item | items, next, text, truncated}: next is the page after this
// one when GitHub has more.
func (g *GitHub) githubRead(ctx context.Context, spec CallSpec) (CallResult, error) {
	fail := func(err error) (CallResult, error) {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	owner, repo, err := repoArgs(spec.Args)
	if err != nil {
		return fail(err)
	}
	name, _ := stringArg(spec.Args, "op")
	op, ok := GitHubReadOps[name]
	if !ok {
		return fail(fmt.Errorf("github_read has no op %q: %w", name, domain.ErrInvalid))
	}
	path, query, err := readRequest(op, spec.Args, owner, repo)
	if err != nil {
		return fail(err)
	}
	if name == "job_log" {
		return g.jobLog(ctx, spec, path)
	}
	res, err := g.do(ctx, spec, http.MethodGet, path, query, nil)
	if err != nil {
		return fail(err)
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, g.upstreamError(spec, res)
	}
	var raw any
	if err := decodeResponseJSON(spec, res.status, res.body, &raw); err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	if m, ok := raw.(map[string]any); ok && op.list != "" {
		raw = m[op.list]
	}
	keys := map[string]bool{}
	for _, k := range strings.Fields(op.keys) {
		keys[k] = true
	}
	body := map[string]any{"op": name}
	if items, ok := project(raw, keys, spec.Credential).([]any); ok {
		body["items"] = items
	} else {
		body["item"] = project(raw, keys, spec.Credential)
	}
	if op.page && strings.Contains(res.header.Get("Link"), `rel="next"`) {
		page, _, _ := intArg(spec.Args, "page")
		body["next"] = strconv.Itoa(max(page, 1) + 1)
	}
	return CallResult{Status: res.status, Body: body, Decision: domain.ConnectorCallGranted}, nil
}

// readRequest fills op's path from args and builds its query. owner and repo
// are the bound repo; args never set them.
func readRequest(op readOp, args map[string]any, owner, repo string) (string, url.Values, error) {
	path := op.path
	if !strings.HasPrefix(path, "/search") && !strings.HasPrefix(path, "/users") {
		path = fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo)) + path
	}
	for _, name := range []string{"number", "run", "job", "ref", "base", "head", "tag", "login", "path"} {
		if strings.Contains(path, "{"+name+"}") {
			v, err := pathArg(args, name)
			if err != nil {
				return "", nil, err
			}
			path = strings.ReplaceAll(path, "{"+name+"}", v)
		}
	}
	q, err := readQuery(op, args, owner, repo)
	return path, q, err
}

// pathArg is args[name] escaped for the path: a positive id, a ref-like
// name, or a repo file path that stays in the repo.
func pathArg(args map[string]any, name string) (string, error) {
	bad := fmt.Errorf("github_read needs a valid args.%s: %w", name, domain.ErrInvalid)
	if name == "number" || name == "run" || name == "job" {
		n, ok, err := intArg(args, name)
		if err != nil || !ok || n <= 0 {
			return "", bad
		}
		return strconv.Itoa(n), nil
	}
	v, ok := stringArg(args, name)
	if (!ok && name != "path") || strings.ContainsAny(v, " \t\r\n") {
		return "", bad
	}
	if name != "path" {
		if strings.Contains(v, "..") {
			return "", bad
		}
		return url.PathEscape(v), nil
	}
	segs := strings.Split(strings.Trim(v, "/"), "/")
	for i, seg := range segs {
		if seg == "." || seg == ".." {
			return "", bad
		}
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/"), nil
}

// readQuery is op's query: its allowed args, a search bound to the repo,
// and a bounded page.
func readQuery(op readOp, args map[string]any, owner, repo string) (url.Values, error) {
	q := url.Values{}
	for arg, param := range op.query {
		if v, ok := stringArg(args, arg); ok {
			q.Set(param, v)
		}
	}
	if strings.HasPrefix(op.path, "/search") {
		text, ok := stringArg(args, "query")
		if !ok || foreignQualifier.MatchString(text) {
			return nil, fmt.Errorf("github_read search needs args.query without repo, org, user or owner qualifiers: %w", domain.ErrInvalid)
		}
		kind := "is:issue"
		if args["op"] == "pr_search" {
			kind = "is:pr"
		}
		q.Set("q", text+" repo:"+owner+"/"+repo+" "+kind)
	}
	if !op.page {
		return q, nil
	}
	perPage, _, err := intArg(args, "perPage")
	if err != nil {
		return nil, err
	}
	page, _, err := intArg(args, "page")
	if err != nil {
		return nil, err
	}
	if perPage <= 0 {
		perPage = 30
	}
	q.Set("per_page", strconv.Itoa(min(perPage, 100)))
	q.Set("page", strconv.Itoa(max(page, 1)))
	return q, nil
}

// jobLog reads a job's log: GitHub redirects to a short-lived download URL,
// and Go drops the Authorization header when the redirect leaves the API
// host. Only the log's last maxLogBytes are kept.
func (g *GitHub) jobLog(ctx context.Context, spec CallSpec, path string) (CallResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	req.Header.Set("Authorization", "Bearer "+spec.Credential)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	resp, err := g.client.Do(req)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			&UpstreamError{Action: spec.Action, Class: ClassNetwork, Summary: sanitizeUpstreamMessage(err.Error(), spec.Credential)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		res, _ := readHTTPResult(spec, resp, func(m string) string { return sanitizeUpstreamMessage(m, spec.Credential) })
		res.status = resp.StatusCode
		return CallResult{Status: resp.StatusCode, Decision: domain.ConnectorCallUpstreamError}, g.upstreamError(spec, res)
	}
	var tail []byte
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		tail = append(tail, buf[:n]...)
		if len(tail) > 2*maxLogBytes {
			tail = append([]byte(nil), tail[len(tail)-maxLogBytes:]...)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return CallResult{Decision: domain.ConnectorCallUpstreamError},
				&UpstreamError{Action: spec.Action, Class: ClassNetwork, Status: resp.StatusCode, Summary: sanitizeUpstreamMessage(rerr.Error(), spec.Credential)}
		}
	}
	truncated := len(tail) > maxLogBytes
	if truncated {
		tail = tail[len(tail)-maxLogBytes:]
	}
	return CallResult{Status: resp.StatusCode, Decision: domain.ConnectorCallGranted, Body: map[string]any{"op": "job_log",
		"text": redact(string(tail), spec.Credential), "truncated": truncated}}, nil
}

// project keeps only keys (at any depth) and redacts the credential from
// every string, so no raw upstream payload passes through.
func project(v any, keys map[string]bool, credential string) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			if keys[k] {
				out[k] = project(x, keys, credential)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = project(x, keys, credential)
		}
		return out
	case string:
		return redact(t, credential)
	}
	return v
}

func redact(s, credential string) string {
	if credential == "" {
		return s
	}
	return strings.ReplaceAll(s, credential, "[redacted]")
}
