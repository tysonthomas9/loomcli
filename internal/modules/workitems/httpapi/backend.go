// Package httpapi implements Work Items ports as a Loom HTTP REST adapter
// against a Loom server's workspace-scoped endpoints.
//
// Unlike the FleetDB adapter, this adapter talks to the regular Loom server and
// uses types generated from
// api/openapi.yaml for request/response wire format. Authentication is
// layered in via an injected *http.Client (typically wrapping
// internal/httpclient.Client for OIDC device-flow support).
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/httptransport"
	"github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

// maxResponseBody limits response body reads to 50MB to prevent OOM on
// pathological responses.
const maxResponseBody = 50 << 20

// Adapter is safe for concurrent use.
type Adapter struct {
	client      *http.Client
	baseURL     string // e.g., "http://localhost:8080" (no trailing slash)
	workspaceID string
}

// Compile-time interface check.
var _ workitems.API = (*Adapter)(nil)
var _ workitems.ReadyQueries = (*Adapter)(nil)
var _ workitems.BlockedQueries = (*Adapter)(nil)
var _ workitems.SearchQueries = (*Adapter)(nil)
var _ workitems.StatsQueries = (*Adapter)(nil)
var _ workitems.EventQueries = (*Adapter)(nil)
var _ workitems.CommentQueries = (*Adapter)(nil)
var _ workitems.CommentCommands = (*Adapter)(nil)
var _ workitems.DependencyCommands = (*Adapter)(nil)

// apiResponse is the JSON envelope returned by loom server endpoints that
// follow the { success, data, error } convention.
type apiResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// New creates an Adapter with the given configuration. It does NOT
// perform any network I/O — the first request triggers auth discovery and
// transport-level errors. This lazy behavior lets the backend be constructed
// before the server is known to be reachable.
func New(cfg Config) (*Adapter, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("api.New: BaseURL is required")
	}
	if cfg.WorkspaceID == "" {
		return nil, fmt.Errorf("api.New: WorkspaceID is required")
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("api.New: invalid server URL %q", cfg.BaseURL)
	}

	// Use caller-supplied HTTPClient as-is (e.g. NewAuthHTTPClient wraps an
	// httpclient.Client whose own transport is already otelhttp-wrapped, so
	// re-wrapping here would double-emit spans). Otherwise, build a default
	// client whose transport carries OpenTelemetry instrumentation.
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = httptransport.NewObservedClient(30 * time.Second)
	}

	return &Adapter{
		client:      httpClient,
		baseURL:     baseURL,
		workspaceID: cfg.WorkspaceID,
	}, nil
}

// BackendName returns the backend identifier string ("api").
func (b *Adapter) BackendName() string { return "api" }

// workspaceBasePath returns the workspace-scoped URL path prefix for this
// backend, with the workspace ID URL-encoded.
func (b *Adapter) workspaceBasePath() string {
	return "/api/workspaces/" + url.PathEscape(b.workspaceID)
}

// doRequest executes an HTTP request against the workspace-scoped API and
// parses the JSON envelope. Path must start with "/".
func (b *Adapter) doRequest(ctx context.Context, method, path string, body interface{}) (*apiResponse, int, error) {
	return b.doRequestHeaders(ctx, method, path, body, nil)
}

// doRequestHeaders is doRequest with extra request headers (e.g. the
// idempotency headers on create, which must travel out-of-band because
// fleet-db's strict JSON decode rejects unknown body fields).
func (b *Adapter) doRequestHeaders(ctx context.Context, method, path string, body interface{}, headers map[string]string) (*apiResponse, int, error) {
	fullURL := b.baseURL + b.workspaceBasePath() + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	var parsed apiResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("server returned non-JSON response (HTTP %d)", resp.StatusCode)
	}
	return &parsed, resp.StatusCode, nil
}

// exec wraps doRequest with error classification.
func (b *Adapter) exec(ctx context.Context, op, method, path string, body interface{}) (*apiResponse, error) {
	return b.execHeaders(ctx, op, method, path, body, nil)
}

// execHeaders is exec with extra request headers.
func (b *Adapter) execHeaders(ctx context.Context, op, method, path string, body interface{}, headers map[string]string) (*apiResponse, error) {
	resp, statusCode, err := b.doRequestHeaders(ctx, method, path, body, headers)
	if err != nil {
		return nil, classifyTransportError(op, err)
	}
	if cerr := classifyHTTPError(op, statusCode, *resp); cerr != nil {
		return nil, cerr
	}
	return resp, nil
}

// hasData returns true if the response Data field is present and non-null.
func hasData(resp *apiResponse) bool {
	return resp != nil && resp.Data != nil && string(resp.Data) != "null"
}

func unmarshalIssueSummaries(resp *apiResponse, op string) ([]workitems.IssueSummary, error) {
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	var issues []gen.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, workitems.AdapterInternal(op, "unmarshal response", err)
	}
	result := make([]workitems.IssueSummary, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issueToSummary(issue))
	}
	return result, nil
}

// --- Query operations ---

func (b *Adapter) Get(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	id := query.IssueID
	resp, err := b.exec(ctx, "Get", http.MethodGet, "/issues/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterNotFound("Get", "issue not found")
	}
	var issue gen.IssueResponse
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, workitems.AdapterInternal("Get", "unmarshal response", err)
	}
	result := issueResponseToDetail(issue)
	return &result, nil
}

func (b *Adapter) List(ctx context.Context, query workitems.ListQuery) (*workitems.ListResult, error) {
	path := "/issues"
	if q := listQueryToQuery(query); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "List", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	summaries, err := unmarshalIssueSummaries(resp, "List")
	if err != nil {
		return nil, err
	}
	items := make([]workitems.ListItem, len(summaries))
	for index := range summaries {
		items[index] = workitems.ListItem{IssueSummary: summaries[index]}
	}
	return &workitems.ListResult{Issues: items}, nil
}

func (b *Adapter) Deferred(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return nil, workitems.AdapterNotImplemented("Deferred", "remote Loom API does not expose a standalone deferred projection")
}

func (b *Adapter) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	path := "/ready"
	if q := readyQueryToQuery(query); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Ready", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueSummaries(resp, "Ready")
}

func (b *Adapter) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	if err := checkBlockedQuerySupported(query); err != nil {
		return nil, err
	}
	path := "/blocked"
	if q := blockedQueryToQuery(query); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Blocked", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	var issues []gen.BlockedIssue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, workitems.AdapterInternal("Blocked", "unmarshal response", err)
	}
	result := make([]workitems.IssueSummary, 0, len(issues))
	for _, i := range issues {
		result = append(result, blockedIssueToSummary(i))
	}
	return result, nil
}

// Stats fetches per-workspace statistics. The server returns a raw
// Statistics object directly (not wrapped in the envelope) at
// GET /api/workspaces/{ws}/stats.
func (b *Adapter) Stats(ctx context.Context) (*workitems.Stats, error) {
	// The /stats endpoint does not use the envelope — call via raw doRequest
	// and decode directly into Statistics.
	fullURL := b.baseURL + b.workspaceBasePath() + "/stats"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, workitems.AdapterInternal("Stats", "create request", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, classifyTransportError("Stats", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, workitems.AdapterInternal("Stats", "read response body", err)
	}
	if resp.StatusCode >= 400 {
		return nil, classifyHTTPError("Stats", resp.StatusCode, apiResponse{Error: string(body)})
	}
	var stats gen.Statistics
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, workitems.AdapterInternal("Stats", "unmarshal response", err)
	}
	result := statisticsToStats(stats)
	return &result, nil
}

// Search performs a full-text search via the /issues endpoint using the
// q query parameter. Returns an empty slice if no results match.
// Note: the loom server uses "q" (not "query") for its search param — see
// internal/modules/workitems/httpapi/params.go addListSearchFilters.
func (b *Adapter) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	if query.Query == "" {
		return nil, workitems.AdapterInvalid("Search", "query must not be empty")
	}
	if query.Limit < 0 {
		return nil, workitems.AdapterInvalid("Search", "limit must not be negative")
	}
	path := "/issues?q=" + url.QueryEscape(query.Query)
	if query.Limit > 0 {
		path += "&limit=" + strconv.Itoa(query.Limit)
	}
	resp, err := b.exec(ctx, "Search", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	var issues []gen.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, workitems.AdapterInternal("Search", "unmarshal response", err)
	}
	result := make([]workitems.IssueSummary, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issueToSummary(issue))
	}
	return result, nil
}

// --- Mutation operations ---

func (b *Adapter) Create(ctx context.Context, params workitems.CreateCommand) (*workitems.IssueSummary, error) {
	req := createCommandToRequest(params)
	resp, err := b.execHeaders(ctx, "Create", http.MethodPost, "/issues", req, params.IdempotencyHeaders())
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("Create", "empty response from server", nil)
	}
	var issue gen.IssueResponse
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, workitems.AdapterInternal("Create", "unmarshal response", err)
	}
	result := issueResponseToSummary(issue)
	return &result, nil
}

func (b *Adapter) Patch(ctx context.Context, params workitems.PatchCommand) (*workitems.IssueDetail, error) {
	req := patchCommandToRequest(params)
	if _, err := b.exec(ctx, "Patch", http.MethodPatch, "/issues/"+url.PathEscape(params.IssueID), req); err != nil {
		return nil, err
	}
	return b.Get(ctx, workitems.GetQuery{IssueID: params.IssueID})
}

// ClaimIssue atomically claims an issue via POST /issues/{id}/claim. A zero
// TTL asks the server to use its default; positive TTLs are forwarded as
// second-granular lock_ttl values. Returns KindConflict when the issue is
// already claimed by a different agent and KindNotFound when it does not
// exist.
func (b *Adapter) Claim(ctx context.Context, command workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	id := command.IssueID
	if id == "" {
		return nil, workitems.AdapterInvalid("Claim", "id must not be empty")
	}
	body, err := claimIssueBody(0)
	if err != nil {
		return nil, err
	}
	path := "/issues/" + url.PathEscape(id) + "/claim"
	if _, err = b.exec(ctx, "Claim", http.MethodPost, path, body); err != nil {
		return nil, err
	}
	return b.Get(ctx, workitems.GetQuery{IssueID: id})
}

// ReleaseIssueLock releases only the operational lock on the issue. The
// generic API backend (a thin OpenAPI client) does not expose a lock-only
// release endpoint, so this returns KindNotImplemented and lets callers fall
// back to TTL expiry. Use the fleet backend for explicit lock release.
func (b *Adapter) ReleaseIssueLock(_ context.Context, _, _ string) error {
	return workitems.AdapterNotImplemented("ReleaseIssueLock", "remote Loom API does not support explicit lock release")
}

func claimIssueBody(lockTTL time.Duration) (any, error) {
	if lockTTL < 0 {
		return nil, workitems.AdapterInvalid("Claim", "lockTTL must not be negative")
	}
	if lockTTL == 0 {
		return nil, nil
	}
	seconds := int((lockTTL + time.Second - time.Nanosecond) / time.Second)
	return struct {
		LockTTL int `json:"lock_ttl"`
	}{LockTTL: seconds}, nil
}

func (b *Adapter) Close(ctx context.Context, params workitems.CloseCommand) (*workitems.CloseResult, error) {
	id := params.IssueID
	req := gen.CloseRequest{}
	if params.Reason != "" {
		req.Reason = &params.Reason
	}
	if params.Session != "" {
		req.Session = &params.Session
	}
	if params.SuggestNext {
		req.SuggestNext = &params.SuggestNext
	}
	if params.Force {
		req.Force = &params.Force
	}
	_, err := b.exec(ctx, "Close", http.MethodPost, "/issues/"+url.PathEscape(id)+"/close", req)
	if err != nil {
		return nil, err
	}
	// The close endpoint returns an opaque data object; the server does not
	// expose unblocked issues in the response. Return a minimal CloseResult
	// with just the closed ID populated so callers see a non-nil result.
	closed := workitems.IssueSummary{ID: id}
	return &workitems.CloseResult{
		Closed:    &closed,
		Unblocked: []workitems.IssueSummary{},
	}, nil
}

func (b *Adapter) Reopen(ctx context.Context, params workitems.ReopenCommand) error {
	id := params.IssueID
	if id == "" {
		return workitems.AdapterInvalid("Reopen", "id must not be empty")
	}
	status := gen.PatchIssueRequestStatus("open")
	req := gen.PatchIssueRequest{Status: &status}
	_, err := b.exec(ctx, "Reopen", http.MethodPatch, "/issues/"+url.PathEscape(id), req)
	if err != nil {
		return err
	}
	// Record reason as a comment after the lifecycle transition. Best-effort:
	// the status transition already succeeded.
	if params.Reason != "" {
		_, _ = b.exec(ctx, "Reopen", http.MethodPost, "/issues/"+url.PathEscape(id)+"/comments", gen.CommentRequest{Text: params.Reason})
	}
	return nil
}

func (b *Adapter) Delete(ctx context.Context, params workitems.DeleteCommand) (workitems.DeleteResult, error) {
	id := params.IssueID
	if id == "" {
		return workitems.DeleteResult{}, workitems.AdapterInvalid("Delete", "id must not be empty")
	}
	if _, err := b.exec(ctx, "Delete", http.MethodDelete, "/issues/"+url.PathEscape(id), nil); err != nil && !workitems.IsKind(err, workitems.KindNotFound) {
		return workitems.DeleteResult{}, err
	}
	return workitems.DeleteResult{DeletedCount: 1, DeletedIDs: []string{id}}, nil
}

func (b *Adapter) BlockRepositoryRequired(context.Context, workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error) {
	return nil, workitems.AdapterNotImplemented("BlockRepositoryRequired", "remote Loom API does not expose repository-admission repair")
}

func (b *Adapter) AssignRepository(ctx context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	body := struct {
		Repo string `json:"repo"`
	}{Repo: command.Repository}
	resp, err := b.exec(ctx, "AssignRepository", http.MethodPut, "/issues/"+url.PathEscape(command.IssueID)+"/repository", body)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("AssignRepository", "empty response from server", nil)
	}
	var issue gen.IssueResponse
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, workitems.AdapterInternal("AssignRepository", "unmarshal response", err)
	}
	result := issueResponseToSummary(issue)
	return &result, nil
}

// --- Dependency operations ---

func (b *Adapter) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	req := gen.AddDependencyRequest{
		DependsOnId: command.DependsOnID,
	}
	if command.Type != "" {
		req.DepType = &command.Type
	}
	_, err := b.exec(ctx, "AddDependency", http.MethodPost, "/issues/"+url.PathEscape(command.IssueID)+"/dependencies", req)
	return err
}

func (b *Adapter) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	path := "/issues/" + url.PathEscape(command.IssueID) + "/dependencies/" + url.PathEscape(command.DependsOnID)
	_, err := b.exec(ctx, "RemoveDependency", http.MethodDelete, path, nil)
	return err
}

func (b *Adapter) ListDependencies(ctx context.Context, query workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	detail, err := b.Get(ctx, workitems.GetQuery(query))
	if err != nil {
		return nil, err
	}
	return append([]workitems.Dependency(nil), detail.Dependencies...), nil
}

// --- Comment operations ---

func (b *Adapter) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	detail, err := b.Get(ctx, workitems.GetQuery(query))
	if err != nil {
		return nil, err
	}
	if detail.Comments == nil {
		return []*workitems.Comment{}, nil
	}
	result := make([]*workitems.Comment, 0, len(detail.Comments))
	for _, comment := range detail.Comments {
		if comment != nil {
			copy := *comment
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (b *Adapter) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	req := gen.CommentRequest{Text: command.Text}
	resp, err := b.exec(ctx, "AddComment", http.MethodPost, "/issues/"+url.PathEscape(command.IssueID)+"/comments", req)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("AddComment", "empty response from server", nil)
	}
	var comment gen.Comment
	if err := json.Unmarshal(resp.Data, &comment); err != nil {
		return nil, workitems.AdapterInternal("AddComment", "unmarshal response", err)
	}
	result := commentToWorkItem(comment)
	// Server may not populate IssueID in the response; ensure we pass it
	// through from the params.
	if result.IssueID == "" {
		result.IssueID = command.IssueID
	}
	return result, nil
}

// --- Event operations ---

func (b *Adapter) ListEvents(ctx context.Context, query workitems.ListEventsQuery) ([]*workitems.Event, error) {
	path := "/issues/" + url.PathEscape(query.IssueID) + "/events"
	if query.Limit > 0 {
		path += "?limit=" + strconv.Itoa(query.Limit)
	}
	resp, err := b.exec(ctx, "ListEvents", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []*workitems.Event{}, nil
	}
	var events []gen.IssueEvent
	if err := json.Unmarshal(resp.Data, &events); err != nil {
		return nil, workitems.AdapterInternal("ListEvents", "unmarshal response", err)
	}
	result := make([]*workitems.Event, 0, len(events))
	for _, e := range events {
		result = append(result, eventToWorkItem(e))
	}
	return result, nil
}
