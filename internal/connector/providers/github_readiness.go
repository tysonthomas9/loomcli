package providers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ActionGitHubPullRequestReadinessRead reads merge-readiness evidence for up
// to MaxReadinessNumbers PRs of one repository in a single GraphQL query
// (read-only; no mutation is ever issued).
const ActionGitHubPullRequestReadinessRead = "github.pull_request.readiness.read"

// MaxReadinessNumbers bounds one readiness query so its node count and the
// 10 s GraphQL timeout stay well inside GitHub's limits.
const MaxReadinessNumbers = 20

// maxReadinessContexts is the per-PR check-context page; more contexts than
// this mark the checks truncated (never known).
const maxReadinessContexts = 100

const readinessPRFields = `number id state isDraft merged
headRefName headRefOid baseRefName baseRefOid
mergeable mergeStateStatus reviewDecision
isInMergeQueue mergeQueueEntry { state }
commits(last: 1) { nodes { commit { oid statusCheckRollup { state
contexts(first: %d) { totalCount pageInfo { hasNextPage } nodes {
__typename
... on CheckRun { name status conclusion isRequired(pullRequestNumber: %d) }
... on StatusContext { context state isRequired(pullRequestNumber: %d) }
} } } } } }`

// readinessQuery builds one aliased GraphQL query. Numbers are validated
// positive ints, so inlining them is safe; owner/repo ride as variables.
func readinessQuery(numbers []int) string {
	var b strings.Builder
	b.WriteString("query($owner: String!, $repo: String!) { repository(owner: $owner, name: $repo) {\n")
	for _, n := range numbers {
		fmt.Fprintf(&b, "pr_%d: pullRequest(number: %d) { ", n, n)
		fmt.Fprintf(&b, readinessPRFields, maxReadinessContexts, n, n)
		b.WriteString(" }\n")
	}
	b.WriteString("} rateLimit { cost remaining resetAt } }")
	return b.String()
}

type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

type readinessContext struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	IsRequired bool   `json:"isRequired"`
}

type readinessNode struct {
	Number           int     `json:"number"`
	ID               string  `json:"id"`
	State            string  `json:"state"`
	IsDraft          bool    `json:"isDraft"`
	Merged           bool    `json:"merged"`
	HeadRefName      string  `json:"headRefName"`
	HeadRefOid       string  `json:"headRefOid"`
	BaseRefName      string  `json:"baseRefName"`
	BaseRefOid       string  `json:"baseRefOid"`
	Mergeable        string  `json:"mergeable"`
	MergeStateStatus string  `json:"mergeStateStatus"`
	ReviewDecision   *string `json:"reviewDecision"`
	IsInMergeQueue   bool    `json:"isInMergeQueue"`
	MergeQueueEntry  *struct {
		State string `json:"state"`
	} `json:"mergeQueueEntry"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				Oid               string `json:"oid"`
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						TotalCount int `json:"totalCount"`
						PageInfo   struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
						Nodes []readinessContext `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

type readinessResponse struct {
	Data *struct {
		Repository map[string]*readinessNode `json:"repository"`
		RateLimit  *struct {
			Cost      int    `json:"cost"`
			Remaining int    `json:"remaining"`
			ResetAt   string `json:"resetAt"`
		} `json:"rateLimit"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// pullRequestReadinessRead issues one GraphQL query for args.numbers.
//
// Body: pullRequests (one camelCase readiness map per PR GitHub returned),
// missing ([{number, code, message}] for aliases GitHub returned null —
// code not_found or upstream_error; partial data is kept), and rateLimit.
// A whole-query rate limit maps to RateLimited with RetryAfter; a missing
// repository maps to a 404 UpstreamError.
func (g *GitHub) pullRequestReadinessRead(ctx context.Context, spec CallSpec) (CallResult, error) {
	owner, repo, err := repoArgs(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	numbers, err := readinessNumbers(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	payload := map[string]any{
		"query":     readinessQuery(numbers),
		"variables": map[string]any{"owner": owner, "repo": repo},
	}
	res, err := g.do(ctx, spec, http.MethodPost, "/graphql", nil, payload)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			g.upstreamError(spec, res)
	}
	var parsed readinessResponse
	if err := decodeResponseJSON(spec, res.status, res.body, &parsed); err != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, err
	}
	if rlErr := graphQLRateLimited(spec, res, parsed.Errors); rlErr != nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError}, rlErr
	}
	if parsed.Data == nil || parsed.Data.Repository == nil {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			graphQLRepoError(spec, parsed.Errors)
	}
	body := readinessBody(spec, numbers, parsed)
	return CallResult{Status: res.status, Body: body, Decision: domain.ConnectorCallGranted}, nil
}

// readinessBody splits the parsed response into found PRs and per-alias
// misses; partial data is kept rather than failing the repository.
func readinessBody(spec CallSpec, numbers []int, parsed readinessResponse) map[string]any {
	errByAlias := graphQLErrorsByAlias(parsed.Errors)
	pulls := make([]map[string]any, 0, len(numbers))
	missing := make([]map[string]any, 0)
	for _, n := range numbers {
		alias := "pr_" + strconv.Itoa(n)
		node := parsed.Data.Repository[alias]
		if node == nil {
			code, message := "upstream_error", "pull request missing from GraphQL response"
			if e, ok := errByAlias[alias]; ok {
				message = sanitizeUpstreamMessage(e.Message, spec.Credential)
				if strings.EqualFold(e.Type, "NOT_FOUND") {
					code = "not_found"
				}
			}
			missing = append(missing, map[string]any{"number": n, "code": code, "message": message})
			continue
		}
		pulls = append(pulls, readinessSummary(node))
	}
	body := map[string]any{"pullRequests": pulls, "missing": missing}
	if rl := parsed.Data.RateLimit; rl != nil {
		body["rateLimit"] = map[string]any{"cost": rl.Cost, "remaining": rl.Remaining, "resetAt": rl.ResetAt}
	}
	return body
}

// readinessNumbers reads args.numbers: 1..MaxReadinessNumbers distinct
// positive integers.
func readinessNumbers(args map[string]any) ([]int, error) {
	var raw []any
	switch v := args["numbers"].(type) {
	case []any:
		raw = v
	case []int:
		for _, n := range v {
			raw = append(raw, n)
		}
	default:
		return nil, fmt.Errorf("args.numbers must be a list of PR numbers: %w", domain.ErrInvalid)
	}
	if len(raw) == 0 || len(raw) > MaxReadinessNumbers {
		return nil, fmt.Errorf("args.numbers needs 1..%d entries: %w", MaxReadinessNumbers, domain.ErrInvalid)
	}
	seen := make(map[int]bool, len(raw))
	numbers := make([]int, 0, len(raw))
	for i := range raw {
		n, _, err := intArg(map[string]any{"n": raw[i]}, "n")
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("args.numbers[%d] must be a positive integer: %w", i, domain.ErrInvalid)
		}
		if !seen[n] {
			seen[n] = true
			numbers = append(numbers, n)
		}
	}
	return numbers, nil
}

// readinessSummary projects one GraphQL node into the camelCase body shape
// prreadiness.GitHubPR decodes.
func readinessSummary(n *readinessNode) map[string]any {
	review := ""
	if n.ReviewDecision != nil {
		review = *n.ReviewDecision
	}
	queueState := ""
	if n.MergeQueueEntry != nil {
		queueState = n.MergeQueueEntry.State
	}
	checks := make([]map[string]any, 0)
	truncated := false
	if len(n.Commits.Nodes) > 0 {
		if rollup := n.Commits.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
			truncated = rollup.Contexts.PageInfo.HasNextPage || rollup.Contexts.TotalCount > len(rollup.Contexts.Nodes)
			for _, c := range rollup.Contexts.Nodes {
				checks = append(checks, readinessCheck(c))
			}
		}
	}
	return map[string]any{
		"number":           n.Number,
		"nodeId":           n.ID,
		"state":            n.State,
		"isDraft":          n.IsDraft,
		"merged":           n.Merged,
		"headRefName":      n.HeadRefName,
		"headRefOid":       n.HeadRefOid,
		"baseRefName":      n.BaseRefName,
		"baseRefOid":       n.BaseRefOid,
		"mergeable":        n.Mergeable,
		"mergeStateStatus": n.MergeStateStatus,
		"reviewDecision":   review,
		"isInMergeQueue":   n.IsInMergeQueue,
		"mergeQueueState":  queueState,
		"checksTruncated":  truncated,
		"checks":           checks,
	}
}

func readinessCheck(c readinessContext) map[string]any {
	if c.Typename == "StatusContext" {
		return map[string]any{
			"name": c.Context, "kind": "status_context",
			"status": "", "conclusion": c.State, "isRequired": c.IsRequired,
		}
	}
	return map[string]any{
		"name": c.Name, "kind": "check_run",
		"status": c.Status, "conclusion": c.Conclusion, "isRequired": c.IsRequired,
	}
}

// graphQLRateLimited detects GitHub's 200-with-errors rate limit.
func graphQLRateLimited(spec CallSpec, res httpResult, errs []graphQLError) error {
	for _, e := range errs {
		if strings.EqualFold(e.Type, "RATE_LIMITED") {
			return &RateLimited{Action: spec.Action, Status: res.status, RetryAfter: retryAfterFromHeaders(res.header, time.Now())}
		}
	}
	return nil
}

// graphQLRepoError maps a null repository to NOT_FOUND (404) or FORBIDDEN
// (403); anything else is a server-side upstream error.
func graphQLRepoError(spec CallSpec, errs []graphQLError) error {
	status, class, summary := http.StatusBadGateway, ClassServerError, "GraphQL response has no repository data"
	for _, e := range errs {
		typ := strings.ToUpper(e.Type)
		if typ != "NOT_FOUND" && typ != "FORBIDDEN" {
			continue
		}
		status, class, summary = http.StatusNotFound, ClassClientError, e.Message
		if typ == "FORBIDDEN" {
			status = http.StatusForbidden
		}
		break
	}
	return &UpstreamError{
		Action:  spec.Action,
		Class:   class,
		Status:  status,
		Summary: sanitizeUpstreamMessage(summary, spec.Credential),
	}
}

// graphQLErrorsByAlias indexes errors by the PR alias in their path
// (["repository", "pr_<n>", ...]).
func graphQLErrorsByAlias(errs []graphQLError) map[string]graphQLError {
	out := map[string]graphQLError{}
	for _, e := range errs {
		if len(e.Path) < 2 {
			continue
		}
		if alias, ok := e.Path[1].(string); ok && strings.HasPrefix(alias, "pr_") {
			if _, dup := out[alias]; !dup {
				out[alias] = e
			}
		}
	}
	return out
}
