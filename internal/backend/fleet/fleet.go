// Package fleet implements Work Items durable ports as an HTTP REST adapter
// against FleetDB's workspace-scoped endpoints.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// maxResponseBody limits response body reads to 50MB to prevent OOM.
const maxResponseBody = 50 << 20

// FleetBackend is safe for concurrent use.
type FleetBackend struct {
	client           *http.Client
	baseWorkspaceURL string // e.g., "http://host/api/v1/ws1"
	baseWorkspaceV2  string // e.g., "http://host/api/v2/ws1"

	mu        sync.RWMutex
	authToken string
	apiKey    string
	actor     string
}

// Compile-time interface check.
var _ workitems.Store = (*FleetBackend)(nil)
var _ workitems.ReadyQueries = (*FleetBackend)(nil)
var _ workitems.DeferredQueries = (*FleetBackend)(nil)
var _ workitems.BlockedQueries = (*FleetBackend)(nil)
var _ workitems.SearchQueries = (*FleetBackend)(nil)
var _ workitems.StatsQueries = (*FleetBackend)(nil)
var _ workitems.EventQueries = (*FleetBackend)(nil)
var _ workitems.CommentQueries = (*FleetBackend)(nil)
var _ workitems.CommentCommands = (*FleetBackend)(nil)
var _ workitems.DependencyCommands = (*FleetBackend)(nil)
var _ workitems.MutationStream = (*FleetBackend)(nil)
var _ workitems.ClaimLeaseCommands = (*FleetBackend)(nil)

// apiResponse is the generic JSON envelope returned by fleet server endpoints.
type apiResponse struct {
	Success bool              `json:"success"`
	Data    json.RawMessage   `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
	Code    string            `json:"code,omitempty"`
	Meta    map[string]string `json:"-"` // populated from native dialect error.meta
}

// New creates a FleetBackend with the given configuration.
func New(cfg Config) (*FleetBackend, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("fleet.New: BaseURL is required")
	}
	if cfg.WorkspaceID == "" {
		return nil, fmt.Errorf("fleet.New: WorkspaceID is required")
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		// Use the shared transport-backed client so every FleetBackend in
		// the process pools idle connections together (default
		// http.DefaultTransport caps MaxIdleConnsPerHost=2, which starves
		// fleet-db's Redis pool under N×M concurrent callers). See
		// SharedHTTPClient docstring + docs/design/fleet-http-connection-reuse.md.
		httpClient = SharedHTTPClient()
	}

	return &FleetBackend{
		client:           httpClient,
		baseWorkspaceURL: baseURL + "/api/v1/" + url.PathEscape(cfg.WorkspaceID),
		baseWorkspaceV2:  baseURL + "/api/v2/" + url.PathEscape(cfg.WorkspaceID),
		authToken:        cfg.AuthToken,
		apiKey:           cfg.APIKey,
		actor:            cfg.Actor,
	}, nil
}

func (b *FleetBackend) doRequestURL(ctx context.Context, method, rawURL string, body interface{}) (*apiResponse, int, error) {
	b.mu.RLock()
	auth := fleethttp.Auth{BearerToken: b.authToken, APIKey: b.apiKey, Actor: b.actor}
	b.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, rawURL, auth, body)
	if err != nil {
		return nil, 0, err
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

	apiResp, err := parseFleetResponse(respBody, resp.StatusCode)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return apiResp, resp.StatusCode, nil
}

// SetAuthToken updates the bearer token for subsequent requests.
// Safe to call concurrently with Work Items calls.
func (b *FleetBackend) SetAuthToken(token string) {
	b.mu.Lock()
	b.authToken = token
	b.mu.Unlock()
}

// SetAPIKey updates the API key for subsequent requests.
// Safe to call concurrently with Work Items calls.
func (b *FleetBackend) SetAPIKey(key string) {
	b.mu.Lock()
	b.apiKey = key
	b.mu.Unlock()
}

func (b *FleetBackend) BackendName() string { return "fleet" }

// doRequest executes an HTTP request and parses the JSON response envelope.
func (b *FleetBackend) doRequest(ctx context.Context, method, path string, body interface{}) (*apiResponse, int, error) {
	b.mu.RLock()
	auth := fleethttp.Auth{BearerToken: b.authToken, APIKey: b.apiKey, Actor: b.actor}
	b.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, b.baseWorkspaceURL+path, auth, body)
	if err != nil {
		return nil, 0, err
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

	apiResp, err := parseFleetResponse(respBody, resp.StatusCode)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return apiResp, resp.StatusCode, nil
}

// doRequestAsActor performs a POST request for a logical worker actor. An
// API-key-authenticated FleetDB treats X-Actor as untrusted/ignored, so service
// hops carry the worker in X-Fleet-Delegated-Actor while retaining the
// configured service actor for authentication and authorization. Dev-mode
// header auth continues to override X-Actor directly.
// Only POST is needed in practice (claim / release endpoints); see execAsActor.
func (b *FleetBackend) doRequestAsActor(ctx context.Context, path string, body interface{}, actor string) (*apiResponse, int, error) {
	b.mu.RLock()
	auth := fleethttp.Auth{BearerToken: b.authToken, APIKey: b.apiKey, Actor: b.actor}
	b.mu.RUnlock()
	if actor != "" {
		if auth.APIKey != "" {
			auth.DelegatedActor = actor
		} else {
			auth.Actor = actor
		}
	}

	req, err := fleethttp.BuildJSONRequest(ctx, "POST", b.baseWorkspaceURL+path, auth, body)
	if err != nil {
		return nil, 0, err
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

	apiResp, err := parseFleetResponse(respBody, resp.StatusCode)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return apiResp, resp.StatusCode, nil
}

// parseFleetResponse turns a fleet-db response body into the apiResponse
// envelope downstream code expects.
func parseFleetResponse(body []byte, statusCode int) (*apiResponse, error) {
	// Try envelope first.
	var env apiResponse
	envErr := json.Unmarshal(body, &env)
	hasEnvelopeFields := envErr == nil && (env.Success || env.Error != "" || env.Code != "" || env.Data != nil)
	if hasEnvelopeFields {
		return &env, nil
	}

	// Native dialect. On 2xx the whole body IS the data; on non-2xx try
	// to extract a structured error message before falling back to the raw
	// body string.
	if statusCode >= 200 && statusCode < 300 {
		// Empty body (e.g. 204 No Content) → success with no data.
		if len(bytes.TrimSpace(body)) == 0 {
			return &apiResponse{Success: true}, nil
		}
		return &apiResponse{Success: true, Data: body}, nil
	}

	// Native error shape: {"error":{"code":"...","message":"...","meta":{...}}}.
	var errEnv struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Meta    map[string]string `json:"meta"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errEnv) == nil && errEnv.Error.Message != "" {
		return &apiResponse{
			Success: false,
			Error:   errEnv.Error.Message,
			Code:    errEnv.Error.Code,
			Meta:    errEnv.Error.Meta,
		}, nil
	}

	// Last resort: surface the raw body as the error string. If even THAT
	// failed to parse as JSON, return the parse error so callers can
	// distinguish "non-JSON response" from "unknown error".
	if envErr != nil {
		return nil, fmt.Errorf("fleet server returned non-JSON response (HTTP %d)", statusCode)
	}
	return &apiResponse{Success: false, Error: string(body)}, nil
}

// exec is a convenience wrapper around doRequest that classifies errors.
func (b *FleetBackend) exec(ctx context.Context, op, method, path string, body interface{}) (*apiResponse, error) {
	apiResp, statusCode, err := b.doRequest(ctx, method, path, body)
	if err != nil {
		return nil, classifyTransportError(op, err)
	}
	if cerr := classifyHTTPError(op, statusCode, *apiResp); cerr != nil {
		return nil, cerr
	}
	return apiResp, nil
}

func (b *FleetBackend) execURL(ctx context.Context, op, method, rawURL string, body interface{}) (*apiResponse, error) {
	apiResp, statusCode, err := b.doRequestURL(ctx, method, rawURL, body)
	if err != nil {
		return nil, classifyTransportError(op, err)
	}
	if cerr := classifyHTTPError(op, statusCode, *apiResp); cerr != nil {
		return nil, cerr
	}
	return apiResp, nil
}

// execAsActor wraps doRequestAsActor with standard error classification.
// Only POST endpoints (claim / release) use this path; the method is therefore
// hardcoded rather than parameterized.
func (b *FleetBackend) execAsActor(ctx context.Context, op, path string, body interface{}, actor string) error {
	apiResp, statusCode, err := b.doRequestAsActor(ctx, path, body, actor)
	if err != nil {
		return classifyTransportError(op, err)
	}
	if cerr := classifyHTTPError(op, statusCode, *apiResp); cerr != nil {
		return cerr
	}
	return nil
}

// hasData returns true if the response Data field is present and non-null.
func hasData(resp *apiResponse) bool {
	return resp != nil && resp.Data != nil && string(resp.Data) != "null"
}

// unmarshalIssueList unmarshals FleetDB issue rows and converts them to
// Work Items summaries. Used by List.
//
// fleet-db list endpoints may return a bare array or {"issues": [...]}.
//
// Try the bare array first; on a JSON unmarshal type mismatch, fall back
// to the wrapper. Anything else is a real parse failure.
func unmarshalIssueList(resp *apiResponse, op string) ([]workitems.IssueSummary, error) {
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	wires, err := unmarshalListOrWrapper[fleetIssueWithCountsWire](resp.Data, op)
	if err != nil {
		return nil, err
	}
	return wireIssuesToSummaries(wires), nil
}

// unmarshalListOrWrapper handles fleet-db's two list dialects (bare array
// or `{"issues": [...]}` wrapper). Bare array is tried first; on a type
// mismatch it falls through to the wrapper. Used by every list endpoint
// (List/Ready/Blocked) and centralizes the "two-shape tolerance" so each
// callsite stays a one-liner.
func unmarshalListOrWrapper[T any](data []byte, op string) ([]T, error) {
	var bare []T
	if err := json.Unmarshal(data, &bare); err == nil {
		return bare, nil
	} else {
		var ute *json.UnmarshalTypeError
		if !errors.As(err, &ute) {
			return nil, workitems.AdapterInternal(op, "unmarshal response", err)
		}
	}
	var wrapper struct {
		Issues []T `json:"issues"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, workitems.AdapterInternal(op, "unmarshal response", err)
	}
	return wrapper.Issues, nil
}

// wireIssuesToSummaries converts FleetDB rows to the owner projection.
func wireIssuesToSummaries(wires []fleetIssueWithCountsWire) []workitems.IssueSummary {
	out := make([]workitems.IssueSummary, 0, len(wires))
	for _, w := range wires {
		out = append(out, w.toIssueSummary())
	}
	return out
}

// --- Query operations ---

func (b *FleetBackend) Get(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	id := query.IssueID
	resp, err := b.exec(ctx, "Get", "GET", "/issues/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterNotFound("Get", "issue not found")
	}
	var wire fleetIssueWithCountsWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, workitems.AdapterInternal("Get", "unmarshal response", err)
	}
	result := wire.fleetIssueWire.toIssueDetail()
	result.Labels = append([]string(nil), wire.Labels...)

	// fleet-db's GET /issues/{id} response is the slim issue record. Fetch
	// related data via dedicated list endpoints so IssueDetailData is fully
	// populated for callers that project dependencies/comments from Get().
	// Failures are non-fatal: the primary Get already succeeded and
	// empty-list is a reasonable degraded mode.
	if deps, dependents, err := b.fetchDependencies(ctx, id); err == nil {
		result.Dependencies = deps
		result.Dependents = dependents
	}
	if comments, err := b.ListComments(ctx, workitems.ListCommentsQuery{IssueID: id}); err == nil {
		result.Comments = comments
	}

	return &result, nil
}

func (b *FleetBackend) List(ctx context.Context, opts workitems.ListFilter) ([]workitems.IssueSummary, error) {
	if err := checkFleetUnsupportedFilters(opts); err != nil {
		return nil, err
	}
	serverOpts := listServerOpts(opts)
	path := "/issues?" + listFilterToQuery(serverOpts)
	resp, err := b.exec(ctx, "List", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	issues, err := unmarshalIssueList(resp, "List")
	if err != nil {
		return nil, err
	}
	return filterListIssues(issues, opts), nil
}

func (b *FleetBackend) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	serverQuery := readyServerQuery(query)
	path := "/issues/ready?" + readyQueryToQuery(serverQuery)
	resp, err := b.exec(ctx, "Ready", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	issues, err := unmarshalListOrWrapper[*readyIssueWithParent](resp.Data, "Ready")
	if err != nil {
		return nil, err
	}
	return filterAvailabilitySummaries(availabilityIssuesToSummaries(issues), query), nil
}

// Stats builds lifecycle counts from fleet-db's status count endpoint and
// canonical operational counts from FleetDB's computed ready/blocked/deferred
// views.
func (b *FleetBackend) Stats(ctx context.Context) (*workitems.Stats, error) {
	resp, err := b.exec(ctx, "Stats", "GET", "/issues/count?group_by=status", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("Stats", "nil response from CountIssues", nil)
	}
	var countResp countIssuesResponse
	if err := json.Unmarshal(resp.Data, &countResp); err != nil {
		return nil, workitems.AdapterInternal("Stats", "unmarshal response", err)
	}
	groups := countResp.Groups
	blocked, err := b.Blocked(ctx, workitems.AvailabilityQuery{})
	if err != nil {
		return nil, err
	}
	deferred, err := b.Deferred(ctx, workitems.AvailabilityQuery{})
	if err != nil {
		return nil, err
	}
	ready, err := b.Ready(ctx, workitems.AvailabilityQuery{})
	if err != nil {
		return nil, err
	}
	return &workitems.Stats{
		TotalIssues:      int(countResp.Total),
		OpenIssues:       int(groups[string(workitems.StatusOpen)]),
		InProgressIssues: int(groups[string(workitems.StatusInProgress)]),
		ClosedIssues:     int(groups[string(workitems.StatusClosed)]),
		BlockedIssues:    len(blocked),
		DeferredIssues:   len(deferred),
		ReadyIssues:      len(ready),
		TombstoneIssues:  int(groups[string(workitems.StatusTombstone)]),
		PinnedIssues:     int(groups[string(workitems.StatusPinned)]),
		// EpicsEligibleForClosure, AverageLeadTime: 0 (fleet-08yg).
		// StatusReview and StatusHooked counts are included in TotalIssues but have
		// no dedicated owner-stat field; they are silently omitted from per-status counts.
	}, nil
}

// Search performs a full-text search through fleet-db's dedicated search
// endpoint. The ordinary list endpoint does not support a text-query filter;
// sending query= there silently returns an unfiltered first page.
func (b *FleetBackend) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	if query.Query == "" {
		return nil, workitems.AdapterInvalid("Search", "query must not be empty")
	}
	if query.Limit < 0 {
		return nil, workitems.AdapterInvalid("Search", "limit must not be negative")
	}
	path := "/issues/search?q=" + url.QueryEscape(query.Query)
	if query.Limit > 0 {
		path += "&limit=" + strconv.Itoa(query.Limit)
	}
	resp, err := b.exec(ctx, "Search", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	wires, err := unmarshalListOrWrapper[fleetIssueWithCountsWire](resp.Data, "Search")
	if err != nil {
		return nil, err
	}
	out := make([]workitems.IssueSummary, 0, len(wires))
	for _, wire := range wires {
		out = append(out, wire.toIssueSummary())
	}
	return out, nil
}

// --- Mutation operations ---

func (b *FleetBackend) Create(ctx context.Context, params workitems.CreateCommand) (*workitems.IssueSummary, error) {
	result, err := b.createIssueOnce(ctx, params)
	if err != nil {
		return result, err
	}
	if err := b.addCreateDependencies(ctx, result.ID, params.Dependencies); err != nil {
		// The issue itself was created; return it alongside the error so
		// callers that inspect the partial result can still see the ID.
		return result, err
	}
	return result, nil
}

func (b *FleetBackend) createIssueOnce(ctx context.Context, params workitems.CreateCommand) (*workitems.IssueSummary, error) {
	body := createCommandToBody(params)
	apiResp, statusCode, respHeaders, err := b.doRequestHeaders(ctx, "POST", "/issues", body, params.IdempotencyHeaders())
	if err != nil {
		return nil, classifyTransportError("Create", err)
	}
	if cerr := classifyHTTPError("Create", statusCode, *apiResp); cerr != nil {
		return nil, cerr
	}
	if !hasData(apiResp) {
		return nil, workitems.AdapterInternal("Create", "empty response from server", nil)
	}
	var issue fleetIssueWire
	if err := json.Unmarshal(apiResp.Data, &issue); err != nil {
		return nil, workitems.AdapterInternal("Create", "unmarshal response", err)
	}
	logIdempotencyResponse(respHeaders, issue.ID)
	result := issue.toIssueSummary()
	return &result, nil
}

// shouldAssignBeforeStatus reports whether a requested assignee change must be
// applied before the status transition. For review/blocked targets the claim lock
// is released as the current assignee inside applyStatusUpdate, so the assign is
// deferred to keep that identity intact (LOOM-1); for every other status target
// the assign is safe to apply first.
func shouldAssignBeforeStatus(params workitems.PatchCommand) bool {
	return params.Assignee != nil && params.Status != nil &&
		*params.Status != "in_progress" && *params.Status != "open" &&
		*params.Status != "review" && *params.Status != "blocked"
}

func (b *FleetBackend) Patch(ctx context.Context, params workitems.PatchCommand) error {
	id := params.IssueID
	if id == "" {
		return workitems.AdapterInvalid("Patch", "id must not be empty")
	}
	handled := false

	req := patchCommandToRequest(params)
	if len(req) > 0 {
		if _, err := b.exec(ctx, "Patch", "PATCH", "/issues/"+url.PathEscape(id), req); err != nil {
			return err
		}
		handled = true
	}

	if err := b.applyLabelUpdates(ctx, id, params); err != nil {
		return err
	}
	if hasLabelUpdate(params) {
		handled = true
	}

	// review/blocked transitions out of in_progress release the claim lock inside
	// applyStatusUpdate, releasing as current.Assignee (the lock holder). Assigning
	// first would erase that identity, skip the release, and leak the lock — so
	// defer the assign for those targets (see applyStatusUpdate's "blocked"/"review"
	// case, LOOM-1). Any requested assignee change is applied afterward by the
	// normal assign step below.
	assignBefore := shouldAssignBeforeStatus(params)
	if assignBefore {
		if err := b.assignIssue(ctx, id, *params.Assignee); err != nil {
			return err
		}
		handled = true
	}

	assigneeHandledByStatus, err := b.applyStatusUpdate(ctx, id, params)
	if err != nil {
		return err
	}
	if params.Status != nil {
		handled = true
	}

	if params.Assignee != nil && !assignBefore && !assigneeHandledByStatus {
		if err := b.assignIssue(ctx, id, *params.Assignee); err != nil {
			return err
		}
		handled = true
	}

	if !handled {
		return workitems.AdapterInvalid("Patch", "no FleetDB-supported fields were provided")
	}
	return nil
}

func (b *FleetBackend) applyStatusUpdate(ctx context.Context, id string, params workitems.PatchCommand) (bool, error) {
	if params.Status == nil {
		return false, nil
	}
	target := strings.TrimSpace(*params.Status)
	if target == "" {
		return false, workitems.AdapterInvalid("Patch", "status must not be empty")
	}

	current, err := b.Get(ctx, workitems.GetQuery{IssueID: id})
	if err != nil {
		return false, err
	}
	clearAssigneeOnOpen := target == "open" && params.Assignee == nil
	if current != nil && current.Status == target {
		if clearAssigneeOnOpen && current.Assignee != "" {
			return false, b.assignIssue(ctx, id, "")
		}
		return target == "in_progress" && params.Assignee != nil, nil
	}

	switch target {
	case "in_progress":
		actor := b.claimActor(params.Assignee, current)
		if actor == "" {
			return false, workitems.AdapterInvalid("Patch", "assignee or configured actor is required to claim an issue")
		}
		return true, b.execAsActor(ctx, "Update", "/issues/"+url.PathEscape(id)+"/claim", nil, actor)
	case "open":
		return false, b.transitionToOpen(ctx, id, current, clearAssigneeOnOpen)
	case "closed":
		// Release the claim before closing (see Close): the issue is terminal
		// afterward and can't be unassigned. Best-effort.
		_ = b.releaseClaim(ctx, id)
		_, err := b.exec(ctx, "Update", "POST", "/issues/"+url.PathEscape(id)+"/close", map[string]interface{}{})
		return false, err
	case "deferred":
		until, err := parseOptionalFleetTime(params.DeferUntil)
		if err != nil {
			return false, err
		}
		return false, b.deferIssue(ctx, id, until)
	case "blocked", "review":
		return false, b.transitionToBlockedOrReview(ctx, id, target, current)
	default:
		return false, workitems.AdapterInvalid("Patch", "unsupported status for FleetDB workflow: "+target)
	}
}

// ReleaseClaim releases the claim held by actor. If the issue is still
// in_progress and assigned to actor, it uses /release so the task becomes
// open/unassigned and immediately claimable. If the issue already moved out of
// in_progress, it drops only the operational lock via /release-lock.
//
// The actor is caller-supplied from the completing agent's identity. Do not
// derive it from current.Assignee: a stale completion could otherwise release a
// newer agent's active lock after the old lock expires and the task is reclaimed.
func (b *FleetBackend) ReleaseClaim(ctx context.Context, id, actor string) error {
	if id == "" {
		return workitems.AdapterInvalid("ReleaseClaim", "id must not be empty")
	}
	if actor == "" {
		return workitems.AdapterInvalid("ReleaseClaim", "actor must not be empty")
	}
	current, err := b.Get(ctx, workitems.GetQuery{IssueID: id})
	if err != nil {
		return err
	}
	if current == nil || current.Assignee != actor {
		return nil
	}
	if current.Status == "in_progress" {
		return b.execAsActor(ctx, "ReleaseClaim",
			"/issues/"+url.PathEscape(id)+"/release", nil, actor)
	}
	return b.releaseIssueLock(ctx, "ReleaseClaim", id, actor, false)
}

// transitionToBlockedOrReview drops the claim lock (lock-only) before changing
// status so the planner's `--status review --assignee=""` flow doesn't leak the
// lock (LOOM-1). Unlike /release, /release-lock leaves status and assignee
// untouched, so we set the target status next and let Update's normal assign step
// apply any requested assignee change (the caller returns false: assignee not
// handled here). We release as current.Assignee (the lock holder); the assign is
// deferred (see shouldAssignBeforeStatus) so that identity is still intact here.
func (b *FleetBackend) transitionToBlockedOrReview(ctx context.Context, id, target string, current *workitems.IssueDetail) error {
	if current != nil && current.Status == "in_progress" && current.Assignee != "" {
		if err := b.releaseIssueLock(ctx, "Update", id, current.Assignee, false); err != nil {
			return err
		}
	}
	if _, err := b.exec(ctx, "Update", "PATCH", "/issues/"+url.PathEscape(id), map[string]interface{}{"status": target}); err != nil {
		return err
	}
	return nil
}

func (b *FleetBackend) transitionToOpen(ctx context.Context, id string, current *workitems.IssueDetail, clearAssignee bool) error {
	if current == nil {
		return workitems.AdapterNotFound("Patch", "issue not found")
	}
	clearAfterTransition := clearAssignee && current.Assignee != "" && current.Status != "in_progress"
	var err error
	switch current.Status {
	case "closed":
		_, err = b.exec(ctx, "Update", "POST", "/issues/"+url.PathEscape(id)+"/reopen", map[string]interface{}{})
	case "deferred":
		_, err = b.exec(ctx, "Update", "POST", "/issues/"+url.PathEscape(id)+"/undefer", nil)
	case "in_progress":
		if current.Assignee != "" {
			return b.execAsActor(ctx, "Update", "/issues/"+url.PathEscape(id)+"/release", nil, current.Assignee)
		}
		_, err = b.exec(ctx, "Update", "PATCH", "/issues/"+url.PathEscape(id), map[string]interface{}{"status": "open"})
	default:
		_, err = b.exec(ctx, "Update", "PATCH", "/issues/"+url.PathEscape(id), map[string]interface{}{"status": "open"})
	}
	if err != nil {
		return err
	}
	if clearAfterTransition {
		return b.assignIssue(ctx, id, "")
	}
	return nil
}

func (b *FleetBackend) claimActor(assignee *string, current *workitems.IssueDetail) string {
	if assignee != nil && *assignee != "" {
		return *assignee
	}
	if current != nil && current.Assignee != "" {
		return current.Assignee
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.actor
}

func (b *FleetBackend) assignIssue(ctx context.Context, id, assignee string) error {
	body := struct {
		Assignee string `json:"assignee"`
	}{Assignee: assignee}
	_, err := b.exec(ctx, "Update", "POST", "/issues/"+url.PathEscape(id)+"/assign", body)
	return err
}

// releaseClaim clears the assignee ("the agent currently working this") when a
// task leaves the in_progress workflow. Without it the claim lingers and the
// kanban renders a stale agent row on the reopened/closed card. It is a no-op
// server-side when there's no assignee. fleet-db rejects /assign on terminal
// issues, so close callers must release *before* closing.
func (b *FleetBackend) releaseClaim(ctx context.Context, id string) error {
	return b.assignIssue(ctx, id, "")
}

func (b *FleetBackend) deferIssue(ctx context.Context, id string, until time.Time) error {
	var body interface{}
	if !until.IsZero() {
		body = struct {
			DeferUntil time.Time `json:"defer_until"`
		}{DeferUntil: until}
	}
	_, err := b.exec(ctx, "DeferIssue", "POST", "/issues/"+url.PathEscape(id)+"/defer", body)
	return err
}

func parseOptionalFleetTime(raw *string) (time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, *raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, *raw); err == nil {
		return t, nil
	}
	return time.Time{}, workitems.AdapterInvalid("Patch", "defer_until must be RFC3339")
}

// Claim atomically claims an issue and returns the canonical owner projection.
//
// fleet-db's claim endpoint is per-issue: POST /issues/{id}/claim with an
// optional {"lock_ttl": seconds} body. A zero TTL asks the server to use its
// default; positive sub-second TTLs round up to one second because the wire
// contract is second-granular.
func (b *FleetBackend) Claim(ctx context.Context, command workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	id := command.IssueID
	if id == "" {
		return nil, workitems.AdapterInvalid("Claim", "id must not be empty")
	}
	body, err := claimIssueBody(0)
	if err != nil {
		return nil, err
	}
	if _, err = b.exec(ctx, "Claim", "POST", "/issues/"+url.PathEscape(id)+"/claim", body); err != nil {
		return nil, err
	}
	return b.Get(ctx, workitems.GetQuery{IssueID: id})
}

func (b *FleetBackend) Close(ctx context.Context, params workitems.CloseCommand) (*workitems.CloseResult, error) {
	id := params.IssueID
	type closeReq struct {
		Reason string `json:"reason,omitempty"`
	}
	req := closeReq{
		Reason: params.Reason,
	}
	// Release the agent claim before closing: a closed issue is terminal and
	// can't be re-assigned afterward, so the assignee would otherwise linger.
	// Best-effort — closing is the primary intent.
	_ = b.releaseClaim(ctx, id)
	resp, err := b.exec(ctx, "Close", "POST", "/issues/"+url.PathEscape(id)+"/close", req)
	if err != nil {
		return nil, err
	}
	// The close endpoint returns a close result with closed issue and unblocked issues.
	// TODO(fleet-q6ox): fleet-db does not yet return unblocked issues on close;
	// Unblocked will be empty until fleet-db adds unblocked-on-close support.
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("Close", "empty response from server", nil)
	}
	var cr closeResultJSON
	if err := json.Unmarshal(resp.Data, &cr); err != nil {
		return nil, workitems.AdapterInternal("Close", "unmarshal response", err)
	}
	if cr.Closed == nil && len(cr.Unblocked) == 0 {
		var issue fleetIssueWire
		if err := json.Unmarshal(resp.Data, &issue); err == nil && issue.ID != "" {
			closed := issue.toIssueSummary()
			return &workitems.CloseResult{Closed: &closed, Unblocked: []workitems.IssueSummary{}}, nil
		}
	}
	return closeResultJSONToResult(&cr), nil
}

func (b *FleetBackend) Reopen(ctx context.Context, params workitems.ReopenCommand) error {
	id := params.IssueID
	if id == "" {
		return workitems.AdapterInvalid("Reopen", "id must not be empty")
	}
	// fleet-db has a dedicated reopen route (see internal/api/issues.go:49);
	// previous implementation used PATCH status=open, but fleet-db's
	// UpdateIssueRequest schema doesn't accept `status` under
	// disallowUnknownFields, so every reopen 400'd with "unknown field
	// status". The per-issue endpoint is also semantically richer — it
	// runs the reopen state machine server-side and allows concurrent
	// close-reopen ordering guarantees.
	_, err := b.exec(ctx, "Reopen", "POST", "/issues/"+url.PathEscape(id)+"/reopen", map[string]interface{}{})
	if err != nil {
		return err
	}
	// Record the reopen reason as a Work Items comment.
	// Best-effort: the status transition already succeeded.
	if params.Reason != "" {
		type commentReq struct {
			Body string `json:"body"`
		}
		_, _ = b.exec(ctx, "Reopen", "POST", "/issues/"+url.PathEscape(id)+"/comments", commentReq{Body: params.Reason})
	}
	// A reopened task is no longer claimed by whoever last worked it; clear the
	// stale assignee so the kanban doesn't render a lingering agent on the
	// now-open card. Mirrors transitionToOpen's clearAfterTransition.
	// Best-effort: the reopen transition already succeeded.
	_ = b.releaseClaim(ctx, id)
	return nil
}

func (b *FleetBackend) Delete(ctx context.Context, params workitems.DeleteCommand) (workitems.DeleteResult, error) {
	id := params.IssueID
	if id == "" {
		return workitems.DeleteResult{}, workitems.AdapterInvalid("Delete", "id must not be empty")
	}
	if _, err := b.exec(ctx, "Delete", "DELETE", "/issues/"+url.PathEscape(id), nil); err != nil && !workitems.IsKind(err, workitems.KindNotFound) {
		return workitems.DeleteResult{}, err
	}
	return workitems.DeleteResult{DeletedCount: 1, DeletedIDs: []string{id}}, nil
}
