package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	pathpkg "path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// GitHub connector actions (dotted snake_case per the CV1 action grammar —
// field names stay camelCase, action segments do not).
const (
	// ActionGitHubPullRequestRead reads one pull request (read-only).
	ActionGitHubPullRequestRead = connectorsmodule.ActionGitHubPullRequestRead
	// ActionGitHubReviewPost posts a PR review after a pre-egress
	// liveness read asserting the PR is open at the expected head sha
	// (vet A1): the write is never issued against a moved or closed PR.
	ActionGitHubReviewPost = connectorsmodule.ActionGitHubReviewPost
	// ActionGitHubMerge merges a PR using GitHub's native server-side sha
	// precondition (vet A2): expectedHeadSha is sent as the merge API's
	// sha field, so GitHub itself 409s when the head moved -> StaleSubject.
	ActionGitHubMerge = connectorsmodule.ActionGitHubMerge
	// ActionGitHubPullsList lists pull requests (merge-conflict agent,
	// read-only).
	ActionGitHubPullsList = connectorsmodule.ActionGitHubPullsList
	// ActionGitHubCompareRead compares two refs (read-only).
	ActionGitHubCompareRead = connectorsmodule.ActionGitHubCompareRead
	// ActionGitHubIssueCommentPost posts an issue/PR comment.
	ActionGitHubIssueCommentPost = connectorsmodule.ActionGitHubIssueCommentPost
)

// GitHubActions returns the actions the GitHub provider implements (a copy).
func GitHubActions() []string {
	return connectorsmodule.ActionsForSource(connectorsmodule.ConnectorSourceGitHub)
}

// DefaultGitHubBaseURL is the public GitHub REST API endpoint.
const DefaultGitHubBaseURL = "https://api.github.com"

// githubAPIVersion pins the REST API version header.
const githubAPIVersion = "2022-11-28"

// maxResponseBytes accommodates GitHub list pages of 100 full PR objects,
// which measure about 21 KiB per PR (roughly 2.1 MiB/page). The 4 MiB cap
// leaves headroom; successful bodies are immediately slimmed by pullSummary,
// so the additional peak memory is transient.
const maxResponseBytes = 4 << 20

const (
	// Review payload limits bound workflow-controlled egress before JSON
	// encoding. The built-in review agents cap findings below these ceilings;
	// the provider keeps the same trust boundary for custom workflows.
	maxGitHubReviewComments         = 50
	maxGitHubReviewPathBytes        = 4096
	maxGitHubReviewBodyBytes        = 64 << 10
	maxGitHubReviewCommentBodyBytes = 64 << 10
	maxGitHubReviewURLBytes         = 4096
	maxGitHubReviewLine             = 1<<31 - 1
)

// GitHub is the Provider adapter for the GitHub REST API. The base URL is
// injectable for tests; the zero values fall back to the public API and
// http.DefaultClient.
type GitHub struct {
	baseURL string
	client  *http.Client
}

var _ Provider = (*GitHub)(nil)

// NewGitHub builds a GitHub provider. client defaults to http.DefaultClient
// and baseURL to DefaultGitHubBaseURL when empty.
func NewGitHub(client *http.Client, baseURL string) *GitHub {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = DefaultGitHubBaseURL
	}
	return &GitHub{baseURL: strings.TrimSuffix(baseURL, "/"), client: client}
}

// Call implements Provider, dispatching on spec.Action.
func (g *GitHub) Call(ctx context.Context, spec CallSpec) (CallResult, error) {
	switch spec.Action {
	case ActionGitHubPullRequestRead:
		return g.pullRequestRead(ctx, spec)
	case ActionGitHubReviewPost:
		return g.reviewPost(ctx, spec)
	case ActionGitHubMerge:
		return g.merge(ctx, spec)
	case ActionGitHubPullsList:
		return g.pullsList(ctx, spec)
	case ActionGitHubCompareRead:
		return g.compareRead(ctx, spec)
	case ActionGitHubIssueCommentPost:
		return g.issueCommentPost(ctx, spec)
	default:
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("github provider does not implement %q: %w", spec.Action, ErrUnknownAction)
	}
}

// merge merges a pull request with GitHub's native sha precondition: the
// expected head sha rides in the request, so the provider itself rejects a
// moved head with 409 — no TOCTOU window (vet A2).
func (g *GitHub) merge(ctx context.Context, spec CallSpec) (CallResult, error) {
	if spec.Preconditions.ExpectedHeadSha == "" {
		return CallResult{Decision: domain.ConnectorCallPreconditionRequired},
			&PreconditionRequired{Action: spec.Action, Fields: []string{"expectedHeadSha"}}
	}
	if err := requireIdempotencyKey(spec); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	owner, repo, number, err := repoNumberArgs(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	payload := map[string]any{"sha": spec.Preconditions.ExpectedHeadSha}
	if v, ok := stringArg(spec.Args, "commitTitle"); ok {
		payload["commit_title"] = v
	}
	if v, ok := stringArg(spec.Args, "commitMessage"); ok {
		payload["commit_message"] = v
	}
	if v, ok := stringArg(spec.Args, "mergeMethod"); ok {
		payload["merge_method"] = v
	}
	res, err := g.do(ctx, spec, http.MethodPut,
		fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", owner, repo, number), nil, payload)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	switch {
	case res.status == http.StatusOK:
		obj, decodeErr := decodeResponseObject(spec, res.status, res.body)
		if decodeErr != nil {
			return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, decodeErr
		}
		return CallResult{
			Status:   res.status,
			Body:     map[string]any{"merged": obj["merged"], "sha": obj["sha"]},
			Decision: domain.ConnectorCallGranted,
		}, nil
	case res.status == http.StatusConflict:
		// GitHub rejected the sha precondition: the head moved after the
		// run pinned it.
		return CallResult{Status: res.status, Decision: domain.ConnectorCallStaleSubject},
			&StaleSubject{
				Action:   spec.Action,
				Resource: spec.Resource,
				Expected: spec.Preconditions.ExpectedHeadSha,
				Reason:   "head moved since expected sha (provider sha precondition rejected)",
			}
	default:
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
}

// reviewPost posts a PR review, gated by a pre-egress liveness read (vet
// A1): GET the PR first and refuse with StaleSubject — without issuing the
// write — unless it is still open at the expected head sha. The head can
// move between the read and the POST; that TOCTOU window is accepted because
// a review is reversible (dismissable), unlike merge.
func (g *GitHub) reviewPost(ctx context.Context, spec CallSpec) (CallResult, error) {
	if spec.Preconditions.ExpectedHeadSha == "" {
		return CallResult{Decision: domain.ConnectorCallPreconditionRequired},
			&PreconditionRequired{Action: spec.Action, Fields: []string{"expectedHeadSha"}}
	}
	if err := requireIdempotencyKey(spec); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	owner, repo, number, err := repoNumberArgs(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	payload, err := githubReviewPayload(spec)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}

	if result, err := g.reviewLivenessCheck(ctx, spec, owner, repo, number); err != nil {
		return result, err
	}

	res, err := g.do(ctx, spec, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, number), nil, payload)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK && res.status != http.StatusCreated {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
	obj, err := decodeResponseObject(spec, res.status, res.body)
	if err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	body, err := githubReviewResult(spec, res.status, obj)
	if err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	return CallResult{
		Status:   res.status,
		Body:     body,
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// githubReviewPayload validates and projects workflow-controlled review
// arguments onto GitHub's documented create-review shape. The projection is
// intentionally closed: custom workflows cannot smuggle arbitrary fields into
// the upstream request. Validation runs before the liveness GET, so malformed
// output causes no provider egress at all.
func githubReviewPayload(spec CallSpec) (map[string]any, error) {
	rawEvent, present := spec.Args["event"]
	event, ok := rawEvent.(string)
	if !present || !ok || event == "" {
		return nil, fmt.Errorf("%s requires string args.event: %w", spec.Action, domain.ErrInvalid)
	}
	switch event {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return nil, fmt.Errorf("%s args.event must be APPROVE, REQUEST_CHANGES, or COMMENT: %w",
			spec.Action, domain.ErrInvalid)
	}

	payload := map[string]any{
		"event":     event,
		"commit_id": spec.Preconditions.ExpectedHeadSha,
	}
	rawBody, bodyPresent := spec.Args["body"]
	body := ""
	if bodyPresent {
		body, ok = rawBody.(string)
		if !ok {
			return nil, fmt.Errorf("%s args.body must be a string: %w", spec.Action, domain.ErrInvalid)
		}
		if err := validateGitHubReviewText("args.body", body, maxGitHubReviewBodyBytes, true); err != nil {
			return nil, fmt.Errorf("%s %w", spec.Action, err)
		}
	}
	if (event == "COMMENT" || event == "REQUEST_CHANGES") &&
		(!bodyPresent || body == "") {
		return nil, fmt.Errorf("%s requires non-empty args.body for event %s: %w",
			spec.Action, event, domain.ErrInvalid)
	}

	unanchored, err := githubReviewFindings(spec.Args)
	if err != nil {
		return nil, fmt.Errorf("%s %w", spec.Action, err)
	}
	body, err = appendUnanchoredGitHubReviewFindings(body, unanchored)
	if err != nil {
		return nil, fmt.Errorf("%s %w", spec.Action, err)
	}
	if body != "" {
		payload["body"] = body
	}
	return payload, nil
}

// githubReviewFindings accepts the built-in review agents' public
// {path,line?,body} finding shape. A positive model-supplied line is only a
// locator hint: it does not prove that the path and line belong to a RIGHT-side
// hunk at the pinned commit. GitHub rejects the entire create-review request
// when even one inline anchor is invalid, so this provider deliberately keeps
// every uncertified finding in the top-level review body. Inline comments can
// be restored later only with provider-owned exact-diff certification.
func githubReviewFindings(args map[string]any) ([]githubReviewFinding, error) {
	raw, present := args["comments"]
	if !present {
		return nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("args.comments must be an array: %w", domain.ErrInvalid)
	}
	if len(entries) > maxGitHubReviewComments {
		return nil, fmt.Errorf("args.comments exceeds %d entries: %w",
			maxGitHubReviewComments, domain.ErrInvalid)
	}

	findings := make([]githubReviewFinding, 0, len(entries))
	for i, rawEntry := range entries {
		finding, err := githubReviewFindingFromValue(i, rawEntry)
		if err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

func githubReviewFindingFromValue(index int, raw any) (githubReviewFinding, error) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return githubReviewFinding{}, fmt.Errorf(
			"args.comments[%d] must be an object: %w", index, domain.ErrInvalid,
		)
	}
	for key := range entry {
		switch key {
		case "path", "line", "body":
		default:
			return githubReviewFinding{}, fmt.Errorf(
				"args.comments[%d] contains unsupported fields: %w", index, domain.ErrInvalid,
			)
		}
	}

	commentPath, ok := entry["path"].(string)
	if !ok || commentPath == "" {
		return githubReviewFinding{}, fmt.Errorf(
			"args.comments[%d].path must be a non-empty string: %w", index, domain.ErrInvalid,
		)
	}
	if err := validateGitHubReviewPath(commentPath); err != nil {
		return githubReviewFinding{}, fmt.Errorf("args.comments[%d].path %w", index, err)
	}

	commentBody, ok := entry["body"].(string)
	if !ok || commentBody == "" {
		return githubReviewFinding{}, fmt.Errorf(
			"args.comments[%d].body must be a non-empty string: %w", index, domain.ErrInvalid,
		)
	}
	if err := validateGitHubReviewText(
		"body", commentBody, maxGitHubReviewCommentBodyBytes, true,
	); err != nil {
		return githubReviewFinding{}, fmt.Errorf("args.comments[%d].%w", index, err)
	}

	finding := githubReviewFinding{path: commentPath, body: commentBody}
	if line, err := githubReviewLine(entry["line"]); err == nil {
		finding.line = line
	}
	return finding, nil
}

type githubReviewFinding struct {
	path string
	line int
	body string
}

func appendUnanchoredGitHubReviewFindings(
	body string,
	findings []githubReviewFinding,
) (string, error) {
	if len(findings) == 0 {
		return body, nil
	}

	var rendered strings.Builder
	rendered.WriteString(body)
	if body != "" {
		rendered.WriteString("\n\n")
	}
	rendered.WriteString("Unanchored review findings:")
	for _, finding := range findings {
		rendered.WriteString("\n- file ")
		rendered.WriteString(strconv.Quote(finding.path))
		if finding.line > 0 {
			rendered.WriteString(", unverified line ")
			rendered.WriteString(strconv.Itoa(finding.line))
		}
		rendered.WriteString(": ")
		rendered.WriteString(finding.body)
	}

	result := rendered.String()
	if err := validateGitHubReviewText(
		"args.body", result, maxGitHubReviewBodyBytes, true,
	); err != nil {
		return "", fmt.Errorf("cannot preserve unanchored args.comments in review body: %w", err)
	}
	return result, nil
}

func githubReviewLine(raw any) (int, error) {
	var line int64
	switch value := raw.(type) {
	case int:
		line = int64(value)
	case int64:
		line = value
	case float64:
		if value != float64(int64(value)) {
			return 0, fmt.Errorf("must be an integer: %w", domain.ErrInvalid)
		}
		line = int64(value)
	default:
		return 0, fmt.Errorf("must be an integer: %w", domain.ErrInvalid)
	}
	if line < 1 || line > maxGitHubReviewLine {
		return 0, fmt.Errorf("must be between 1 and %d: %w", maxGitHubReviewLine, domain.ErrInvalid)
	}
	return int(line), nil
}

func validateGitHubReviewPath(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("must be valid UTF-8: %w", domain.ErrInvalid)
	}
	if len(value) > maxGitHubReviewPathBytes {
		return fmt.Errorf("exceeds %d bytes: %w", maxGitHubReviewPathBytes, domain.ErrInvalid)
	}
	if pathpkg.IsAbs(value) || pathpkg.Clean(value) != value || value == "." ||
		value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("must be a normalized repository-relative path: %w", domain.ErrInvalid)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("contains a control character: %w", domain.ErrInvalid)
		}
	}
	return nil
}

func validateGitHubReviewText(field, value string, maxBytes int, allowWhitespaceControls bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8: %w", field, domain.ErrInvalid)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes: %w", field, maxBytes, domain.ErrInvalid)
	}
	for _, r := range value {
		if unicode.IsControl(r) &&
			!(allowWhitespaceControls && (r == '\n' || r == '\r' || r == '\t')) {
			return fmt.Errorf("%s contains an unsafe control character: %w", field, domain.ErrInvalid)
		}
	}
	return nil
}

// githubReviewResult keeps the provider response closed and camelCase. A
// successful GitHub create-review response always carries a browser-facing
// html_url; malformed success bodies are upstream failures, not granted calls
// with an unusable review link.
func githubReviewResult(spec CallSpec, status int, obj map[string]any) (map[string]any, error) {
	id, ok := obj["id"].(float64)
	if !ok || id < 1 || math.Trunc(id) != id {
		return nil, malformedGitHubReviewResult(spec, status, "missing positive integer id")
	}
	state, ok := obj["state"].(string)
	if !ok || state == "" {
		return nil, malformedGitHubReviewResult(spec, status, "missing string state")
	}
	if err := validateGitHubReviewText("state", state, 64, false); err != nil {
		return nil, malformedGitHubReviewResult(spec, status, "invalid state")
	}
	htmlURL, ok := obj["html_url"].(string)
	if !ok || htmlURL == "" {
		return nil, malformedGitHubReviewResult(spec, status, "missing string html_url")
	}
	if err := validateGitHubReviewURL(htmlURL); err != nil {
		return nil, malformedGitHubReviewResult(spec, status, "invalid html_url")
	}
	return map[string]any{
		"id":      id,
		"state":   state,
		"htmlUrl": htmlURL,
	}, nil
}

func validateGitHubReviewURL(value string) error {
	if err := validateGitHubReviewText("html_url", value, maxGitHubReviewURLBytes, false); err != nil {
		return err
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("html_url must be an absolute HTTP(S) URL: %w", domain.ErrInvalid)
	}
	return nil
}

func malformedGitHubReviewResult(spec CallSpec, status int, reason string) error {
	return &UpstreamError{
		Action:  spec.Action,
		Class:   ClassServerError,
		Status:  status,
		Summary: "invalid create-review response: " + reason,
	}
}

// reviewLivenessCheck is reviewPost's pre-egress liveness read: it GETs the
// PR and refuses with StaleSubject unless it is still open at the expected
// head sha. A nil error means the write may proceed.
func (g *GitHub) reviewLivenessCheck(ctx context.Context, spec CallSpec, owner, repo string, number int) (CallResult, error) {
	res, err := g.do(ctx, spec, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number), nil, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
	pr, err := decodeResponseObject(spec, res.status, res.body)
	if err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	if state, _ := pr["state"].(string); state != "open" {
		return CallResult{Decision: domain.ConnectorCallStaleSubject},
			&StaleSubject{
				Action:   spec.Action,
				Resource: spec.Resource,
				Expected: spec.Preconditions.ExpectedHeadSha,
				Reason:   fmt.Sprintf("pull request not open (state %q)", state),
			}
	}
	if sha := nestedString(pr, "head", "sha"); sha != spec.Preconditions.ExpectedHeadSha {
		return CallResult{Decision: domain.ConnectorCallStaleSubject},
			&StaleSubject{
				Action:   spec.Action,
				Resource: spec.Resource,
				Expected: spec.Preconditions.ExpectedHeadSha,
				Reason:   "head sha moved since review was prepared",
			}
	}
	return CallResult{}, nil
}

// pullRequestRead reads one pull request.
func (g *GitHub) pullRequestRead(ctx context.Context, spec CallSpec) (CallResult, error) {
	owner, repo, number, err := repoNumberArgs(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	res, err := g.do(ctx, spec, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number), nil, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
	obj, err := decodeResponseObject(spec, res.status, res.body)
	if err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	return CallResult{
		Status:   res.status,
		Body:     pullSummary(obj),
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// pullsList lists pull requests; optional camelCase args state, base, head,
// perPage, and page map to GitHub's query parameters.
func (g *GitHub) pullsList(ctx context.Context, spec CallSpec) (CallResult, error) {
	owner, repo, err := repoArgs(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	query := url.Values{}
	if v, ok := stringArg(spec.Args, "state"); ok {
		query.Set("state", v)
	}
	if v, ok := stringArg(spec.Args, "base"); ok {
		query.Set("base", v)
	}
	if v, ok := stringArg(spec.Args, "head"); ok {
		query.Set("head", v)
	}
	if v, ok, err := intArg(spec.Args, "perPage"); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	} else if ok {
		query.Set("per_page", strconv.Itoa(v))
	}
	if v, ok, err := intArg(spec.Args, "page"); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	} else if ok {
		query.Set("page", strconv.Itoa(v))
	}
	res, err := g.do(ctx, spec, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/pulls", owner, repo), query, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
	var raw []map[string]any
	if err := decodeResponseJSON(spec, res.status, res.body, &raw); err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	pulls := make([]map[string]any, 0, len(raw))
	for _, pr := range raw {
		pulls = append(pulls, pullSummary(pr))
	}
	return CallResult{
		Status:   res.status,
		Body:     map[string]any{"pullRequests": pulls},
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// compareRead compares two refs (read-only; feeds the merge-conflict agent).
func (g *GitHub) compareRead(ctx context.Context, spec CallSpec) (CallResult, error) {
	owner, repo, err := repoArgs(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	base, okBase := stringArg(spec.Args, "base")
	head, okHead := stringArg(spec.Args, "head")
	if !okBase || !okHead {
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("%s requires args.base and args.head: %w", spec.Action, domain.ErrInvalid)
	}
	res, err := g.do(ctx, spec, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/compare/%s...%s",
			owner, repo, url.PathEscape(base), url.PathEscape(head)), nil, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
	obj, err := decodeResponseObject(spec, res.status, res.body)
	if err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	files, diff := compareFiles(obj["files"])
	return CallResult{
		Status: res.status,
		Body: map[string]any{
			"status":       obj["status"],
			"aheadBy":      obj["ahead_by"],
			"behindBy":     obj["behind_by"],
			"totalCommits": obj["total_commits"],
			// files carries the per-file changed-file summary (filename,
			// status, patch); diff is those patches stitched into a single
			// unified-diff string for a reviewer/LLM that reasons over the
			// raw diff. Both are derived from the same /compare JSON response
			// (the GitHub compare endpoint embeds files[].patch), so no extra
			// request and no media-type change is needed.
			"files": files,
			"diff":  diff,
		},
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// compareFiles projects the /compare response's files[] array into a sanitized
// camelCase list (filename, status, additions, deletions, patch) and stitches
// the per-file patches into one unified-diff string. A nil or non-array input
// yields an empty list and an empty diff.
func compareFiles(raw any) ([]map[string]any, string) {
	arr, ok := raw.([]any)
	if !ok {
		return []map[string]any{}, ""
	}
	files := make([]map[string]any, 0, len(arr))
	var diff strings.Builder
	for _, entry := range arr {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		filename, _ := obj["filename"].(string)
		status, _ := obj["status"].(string)
		patch, _ := obj["patch"].(string)
		files = append(files, map[string]any{
			"filename":  filename,
			"status":    status,
			"additions": obj["additions"],
			"deletions": obj["deletions"],
			"patch":     patch,
		})
		if patch == "" {
			continue
		}
		// A minimal but valid unified-diff file header so an LLM (or a human)
		// sees which file each hunk belongs to. GitHub omits the diff --git
		// preamble from files[].patch, so we synthesize the path lines.
		fmt.Fprintf(&diff, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n%s\n",
			filename, filename, filename, filename, patch)
	}
	return files, diff.String()
}

// issueCommentPost posts a comment on an issue or pull request.
func (g *GitHub) issueCommentPost(ctx context.Context, spec CallSpec) (CallResult, error) {
	if err := requireIdempotencyKey(spec); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	owner, repo, number, err := repoNumberArgs(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	body, ok := stringArg(spec.Args, "body")
	if !ok {
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("%s requires args.body: %w", spec.Action, domain.ErrInvalid)
	}
	res, err := g.do(ctx, spec, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number), nil,
		map[string]any{"body": body})
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusCreated {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
	obj, err := decodeResponseObject(spec, res.status, res.body)
	if err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	return CallResult{
		Status:   res.status,
		Body:     map[string]any{"id": obj["id"]},
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// httpResult is one upstream HTTP exchange whose body passed the shared size
// limit without truncation.
type httpResult struct {
	status int
	header http.Header
	body   []byte
}

func readHTTPResult(spec CallSpec, resp *http.Response, sanitize func(string) string) (httpResult, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxResponseBytes)+1))
	if err != nil {
		return httpResult{}, &UpstreamError{
			Action:  spec.Action,
			Class:   ClassNetwork,
			Status:  resp.StatusCode,
			Summary: sanitize(err.Error()),
		}
	}
	if len(raw) > maxResponseBytes {
		return httpResult{}, &UpstreamError{
			Action:  spec.Action,
			Class:   ClassServerError,
			Status:  resp.StatusCode,
			Summary: fmt.Sprintf("response exceeded %d bytes", maxResponseBytes),
		}
	}
	return httpResult{status: resp.StatusCode, header: resp.Header, body: raw}, nil
}

func decodeResponseJSON(spec CallSpec, status int, body []byte, dst any) error {
	if err := json.Unmarshal(body, dst); err != nil {
		return &UpstreamError{
			Action:  spec.Action,
			Class:   ClassServerError,
			Status:  status,
			Summary: fmt.Sprintf("invalid JSON response: %v", err),
		}
	}
	return nil
}

// do issues one HTTP request. The credential rides in the Authorization
// header only; the runID-derived idempotency key rides on every request as
// the Idempotency-Key header. Transport failures map to UpstreamError with
// ClassNetwork (sanitized).
func (g *GitHub) do(ctx context.Context, spec CallSpec, method, path string, query url.Values, payload any) (httpResult, error) {
	u := g.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return httpResult{}, fmt.Errorf("providers: encode %s request: %w", spec.Action, err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return httpResult{}, fmt.Errorf("providers: build %s request: %w", spec.Action, err)
	}
	req.Header.Set("Authorization", "Bearer "+spec.Credential)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if spec.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", spec.IdempotencyKey)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return httpResult{}, &UpstreamError{
			Action:  spec.Action,
			Class:   ClassNetwork,
			Summary: sanitizeUpstreamMessage(err.Error(), spec.Credential),
		}
	}
	defer resp.Body.Close()
	return readHTTPResult(spec, resp, func(message string) string {
		return sanitizeUpstreamMessage(message, spec.Credential)
	})
}

// upstreamError maps a non-success upstream response to the structured
// provider errors: 429 and rate-limit-shaped 403s become RateLimited
// (retryable); everything else becomes UpstreamError with a sanitized
// summary so credentials echoed by the provider never propagate.
func (g *GitHub) upstreamError(spec CallSpec, res httpResult) error {
	msg := extractMessage(res.body)
	if res.status == http.StatusTooManyRequests ||
		(res.status == http.StatusForbidden && isRateLimitResponse(res.header, msg)) {
		return &RateLimited{
			Action:     spec.Action,
			Status:     res.status,
			RetryAfter: parseRetryAfter(res.header),
		}
	}
	class := ClassClientError
	if res.status >= 500 {
		class = ClassServerError
	}
	return &UpstreamError{
		Action:  spec.Action,
		Class:   class,
		Status:  res.status,
		Summary: sanitizeUpstreamMessage(msg, spec.Credential),
	}
}

// isRateLimitResponse detects GitHub's 403-shaped rate limits via the
// standard headers or the documented message text.
func isRateLimitResponse(header http.Header, msg string) bool {
	if header.Get("Retry-After") != "" || header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	return strings.Contains(strings.ToLower(msg), "rate limit")
}

// parseRetryAfter reads a seconds-valued Retry-After header, zero otherwise.
func parseRetryAfter(header http.Header) time.Duration {
	secs, err := strconv.Atoi(header.Get("Retry-After"))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// extractMessage plucks GitHub's top-level "message" field from an error
// body; only this field — never the raw body — feeds error summaries.
func extractMessage(body []byte) string {
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		return ""
	}
	return obj.Message
}

func decodeResponseObject(spec CallSpec, status int, body []byte) (map[string]any, error) {
	var obj map[string]any
	if err := decodeResponseJSON(spec, status, body, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// nestedString plucks a nested string field (e.g. head.sha).
func nestedString(obj map[string]any, keys ...string) string {
	cur := any(obj)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

// pullSummary whitelists the camelCase PR fields exposed in CallResult.Body.
func pullSummary(pr map[string]any) map[string]any {
	return map[string]any{
		"number":      pr["number"],
		"state":       pr["state"],
		"title":       pr["title"],
		"draft":       pr["draft"],
		"merged":      pullMerged(pr),
		"authorLogin": nestedString(pr, "user", "login"),
		"updatedAt":   pr["updated_at"],
		"headSha":     nestedString(pr, "head", "sha"),
		"headRef":     nestedString(pr, "head", "ref"),
		"baseSha":     nestedString(pr, "base", "sha"),
		"baseRef":     nestedString(pr, "base", "ref"),
	}
}

func pullMerged(pr map[string]any) bool {
	if merged, ok := pr["merged"].(bool); ok {
		return merged
	}
	return pr["merged_at"] != nil
}

// requireIdempotencyKey enforces the §9.3 fencing contract on write actions:
// no runID-derived idempotency key, no egress. Violations wrap
// domain.ErrInvalid.
func requireIdempotencyKey(spec CallSpec) error {
	if spec.IdempotencyKey == "" {
		return fmt.Errorf("%s requires a runID-derived idempotency key: %w", spec.Action, domain.ErrInvalid)
	}
	return nil
}

// repoArgs extracts the required owner and repo args.
func repoArgs(args map[string]any) (owner, repo string, err error) {
	owner, okOwner := stringArg(args, "owner")
	repo, okRepo := stringArg(args, "repo")
	if !okOwner || !okRepo {
		return "", "", fmt.Errorf("args.owner and args.repo required: %w", domain.ErrInvalid)
	}
	return owner, repo, nil
}

// repoNumberArgs extracts the required owner, repo, and number args.
func repoNumberArgs(args map[string]any) (owner, repo string, number int, err error) {
	owner, repo, err = repoArgs(args)
	if err != nil {
		return "", "", 0, err
	}
	number, ok, err := intArg(args, "number")
	if err != nil {
		return "", "", 0, err
	}
	if !ok {
		return "", "", 0, fmt.Errorf("args.number required: %w", domain.ErrInvalid)
	}
	return owner, repo, number, nil
}

// stringArg reads a non-empty string arg, reporting presence.
func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key].(string)
	return v, ok && v != ""
}

// intArg reads an integer arg, accepting Go ints and JSON float64s.
// Non-integral values wrap domain.ErrInvalid.
func intArg(args map[string]any, key string) (int, bool, error) {
	raw, present := args[key]
	if !present {
		return 0, false, nil
	}
	switch v := raw.(type) {
	case int:
		return v, true, nil
	case int64:
		return int(v), true, nil
	case float64:
		if v != float64(int(v)) {
			return 0, false, fmt.Errorf("args.%s must be an integer: %w", key, domain.ErrInvalid)
		}
		return int(v), true, nil
	default:
		return 0, false, fmt.Errorf("args.%s must be an integer, got %T: %w", key, raw, domain.ErrInvalid)
	}
}
