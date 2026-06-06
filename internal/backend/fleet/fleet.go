// Package fleet implements backend.IssueBackend as an HTTP REST client against
// the fleet server's workspace-scoped API endpoints. It translates IssueBackend
// method calls into HTTP requests, parses the JSON response envelopes, and
// converts server-side types to backend wire types.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// maxResponseBody limits response body reads to 50MB to prevent OOM.
const maxResponseBody = 50 << 20

// FleetBackend implements backend.IssueBackend by forwarding calls to a fleet
// server's REST API. It is safe for concurrent use.
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
var _ backend.IssueBackend = (*FleetBackend)(nil)
var _ backend.CursorMutationBackend = (*FleetBackend)(nil)
var _ backend.ClaimReleaser = (*FleetBackend)(nil)

// apiResponse is the generic JSON envelope returned by fleet server endpoints.
type apiResponse struct {
	Success bool              `json:"success"`
	Data    json.RawMessage   `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
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
// Safe to call concurrently with IssueBackend method calls.
func (b *FleetBackend) SetAuthToken(token string) {
	b.mu.Lock()
	b.authToken = token
	b.mu.Unlock()
}

// SetAPIKey updates the API key for subsequent requests.
// Safe to call concurrently with IssueBackend method calls.
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

// doRequestHeaders is doRequest with extra request headers (the idempotency
// headers on create — fleet-db's strict JSON decode rejects unknown body
// fields, so they must travel out-of-band). It also surfaces the idempotency
// response headers: dedup metadata would otherwise be invisible because the
// backend contract returns only IssueData.
func (b *FleetBackend) doRequestHeaders(ctx context.Context, method, path string, body interface{}, headers map[string]string) (*apiResponse, int, http.Header, error) {
	b.mu.RLock()
	auth := fleethttp.Auth{BearerToken: b.authToken, APIKey: b.apiKey, Actor: b.actor}
	b.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, b.baseWorkspaceURL+path, auth, body)
	if err != nil {
		return nil, 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("read response body: %w", err)
	}

	apiResp, err := parseFleetResponse(respBody, resp.StatusCode)
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	return apiResp, resp.StatusCode, resp.Header, nil
}

// doRequestAsActor performs a POST request with the X-Actor header overridden.
// Only POST is needed in practice (claim / release endpoints); see execAsActor.
func (b *FleetBackend) doRequestAsActor(ctx context.Context, path string, body interface{}, actor string) (*apiResponse, int, error) {
	b.mu.RLock()
	auth := fleethttp.Auth{BearerToken: b.authToken, APIKey: b.apiKey, Actor: b.actor}
	b.mu.RUnlock()
	if actor != "" {
		auth.Actor = actor
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
	hasEnvelopeFields := envErr == nil && (env.Success || env.Error != "" || env.Data != nil)
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
		return &apiResponse{Success: false, Error: errEnv.Error.Message, Meta: errEnv.Error.Meta}, nil
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

// unmarshalIssueList unmarshals a []*types.IssueWithCounts response and
// converts to []backend.IssueData. Used by List, GetChildren, and SearchIssues.
//
// fleet-db list endpoints may return a bare array or {"issues": [...]}.
//
// Try the bare array first; on a JSON unmarshal type mismatch, fall back
// to the wrapper. Anything else is a real parse failure.
func unmarshalIssueList(resp *apiResponse, op string) ([]backend.IssueData, error) {
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	wires, err := unmarshalListOrWrapper[fleetIssueWithCountsWire](resp.Data, op)
	if err != nil {
		return nil, err
	}
	return wireIssuesToData(wires), nil
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
			return nil, backend.ErrInternal(op, "unmarshal response", err)
		}
	}
	var wrapper struct {
		Issues []T `json:"issues"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, backend.ErrInternal(op, "unmarshal response", err)
	}
	return wrapper.Issues, nil
}

// wireIssuesToData converts a slice of the fleet-db wire shape into the
// backend.IssueData projection downstream code consumes.
func wireIssuesToData(wires []fleetIssueWithCountsWire) []backend.IssueData {
	out := make([]backend.IssueData, 0, len(wires))
	for _, w := range wires {
		out = append(out, w.toIssueData())
	}
	return out
}

// --- Query operations ---

func (b *FleetBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	resp, err := b.exec(ctx, "Get", "GET", "/issues/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrNotFound("Get", "issue not found")
	}
	var wire fleetIssueWithCountsWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, backend.ErrInternal("Get", "unmarshal response", err)
	}
	issue := wire.toIssue()
	details := types.IssueDetails{Issue: issue}
	result := detailsToDetailData(&details)
	result.IssueData.Labels = append([]string(nil), wire.Labels...)
	result.IssueData.DependencyCount = wire.DependencyCount
	result.IssueData.DependentCount = wire.DependentCount

	// fleet-db's GET /issues/{id} response is the slim issue record. Fetch
	// related data via dedicated list endpoints so IssueDetailData is fully
	// populated for callers that project dependencies/comments from Get().
	// Failures are non-fatal: the primary Get already succeeded and
	// empty-list is a reasonable degraded mode.
	if deps, dependents, err := b.fetchDependencies(ctx, id); err == nil {
		result.Dependencies = deps
		result.Dependents = dependents
	}
	if comments, err := b.ListComments(ctx, id); err == nil {
		result.Comments = comments
	}

	return &result, nil
}

func (b *FleetBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	if err := checkFleetUnsupportedFilters(opts); err != nil {
		return nil, err
	}
	path := "/issues?" + listOptsToQuery(opts)
	resp, err := b.exec(ctx, "List", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "List")
}

func (b *FleetBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	path := "/issues/ready?" + readyOptsToQuery(opts)
	resp, err := b.exec(ctx, "Ready", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	issues, err := unmarshalListOrWrapper[*readyIssueWithParent](resp.Data, "Ready")
	if err != nil {
		return nil, err
	}
	return readyIssuesToData(issues), nil
}

func (b *FleetBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	path := "/issues/blocked?" + blockedOptsToQuery(opts)
	resp, err := b.exec(ctx, "Blocked", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	return unmarshalBlockedIssueList(resp.Data, "Blocked")
}

// Stats builds StatsData from the fleet server's count endpoint with
// group_by=status. ReadyIssues, EpicsEligibleForClosure, and AverageLeadTime
// are unavailable until fleet-db adds server-side stats aggregation (fleet-08yg).
func (b *FleetBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	resp, err := b.exec(ctx, "Stats", "GET", "/issues/count?group_by=status", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("Stats", "nil response from CountIssues", nil)
	}
	var countResp countIssuesResponse
	if err := json.Unmarshal(resp.Data, &countResp); err != nil {
		return nil, backend.ErrInternal("Stats", "unmarshal response", err)
	}
	groups := countResp.Groups
	return &backend.StatsData{
		TotalIssues:      int(countResp.Total),
		OpenIssues:       int(groups[string(types.StatusOpen)]),
		InProgressIssues: int(groups[string(types.StatusInProgress)]),
		ClosedIssues:     int(groups[string(types.StatusClosed)]),
		BlockedIssues:    int(groups[string(types.StatusBlocked)]),
		DeferredIssues:   int(groups[string(types.StatusDeferred)]),
		TombstoneIssues:  int(groups[string(types.StatusTombstone)]),
		PinnedIssues:     int(groups[string(types.StatusPinned)]),
		// ReadyIssues, EpicsEligibleForClosure, AverageLeadTime: 0 (fleet-08yg).
		// StatusReview and StatusHooked counts are included in TotalIssues but have
		// no dedicated StatsData field; they are silently omitted from per-status counts.
	}, nil
}

// Count returns the number of issues matching opts via fleet-db's
// /issues/count endpoint. It wires a subset of backend.CountOpts into the
// endpoint's supported query params (status/type/assignee/label/repo) and
// returns the Total field from the response.
//
// Grouping is rejected: when opts.GroupBy is non-empty, the caller wants a
// breakdown that cannot fit into a single int return value. The grouped
// variant is exposed through Stats(), which calls /issues/count?group_by=status
// directly. Future work (fleet-08yg) may expand Stats to cover more groupings
// without changing Count's signature.
func (b *FleetBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	if opts.GroupBy != "" {
		return 0, backend.ErrNotImplemented("Count",
			"group_by is not supported by Count (use Stats for grouped status counts)")
	}
	if err := checkFleetUnsupportedCountFilters(opts); err != nil {
		return 0, err
	}
	path := "/issues/count"
	if q := countOptsToQuery(opts); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Count", "GET", path, nil)
	if err != nil {
		return 0, err
	}
	if !hasData(resp) {
		return 0, backend.ErrInternal("Count", "empty response from server", nil)
	}
	var countResp countIssuesResponse
	if err := json.Unmarshal(resp.Data, &countResp); err != nil {
		return 0, backend.ErrInternal("Count", "unmarshal response", err)
	}
	return int(countResp.Total), nil
}

// GetChildren returns the direct children of the given issue (typically an epic)
// by calling the fleet-db list endpoint with a parent filter.
func (b *FleetBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	if id == "" {
		return nil, backend.ErrValidation("GetChildren", "id must not be empty")
	}
	path := "/issues?parent_id=" + url.QueryEscape(id)
	resp, err := b.exec(ctx, "GetChildren", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "GetChildren")
}

// SearchIssues performs a full-text search via the fleet-db list endpoint with
// the query parameter set. Future fleet-db work can route this to a dedicated
// FT.SEARCH endpoint.
// Note: fleet-db uses "query" (not "q") for its search param — see
// internal/backend/fleet/params.go addListSearchFilters.
func (b *FleetBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	if query == "" {
		return nil, backend.ErrValidation("SearchIssues", "query must not be empty")
	}
	if limit < 0 {
		return nil, backend.ErrValidation("SearchIssues", "limit must not be negative")
	}
	path := "/issues?query=" + url.QueryEscape(query)
	if limit > 0 {
		path += "&limit=" + strconv.Itoa(limit)
	}
	resp, err := b.exec(ctx, "SearchIssues", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "SearchIssues")
}

// --- Mutation operations ---

func (b *FleetBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	body := createParamsToBody(params)
	apiResp, statusCode, respHeaders, err := b.doRequestHeaders(ctx, "POST", "/issues", body, params.IdempotencyHeaders())
	if err != nil {
		return nil, classifyTransportError("Create", err)
	}
	if cerr := classifyHTTPError("Create", statusCode, *apiResp); cerr != nil {
		return nil, cerr
	}
	resp := apiResp
	if !hasData(resp) {
		return nil, backend.ErrInternal("Create", "empty response from server", nil)
	}
	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, backend.ErrInternal("Create", "unmarshal response", err)
	}
	// Dedup observability: the backend contract returns only IssueData, so
	// surface replay/soft-duplicate signals in the log (visible in serve logs).
	if respHeaders.Get("X-Idempotency-Replayed") == "true" {
		slog.Info("idempotent create replayed existing issue", "issue", issue.ID)
	}
	if warn := respHeaders.Get("X-Idempotency-Warning"); warn != "" {
		slog.Info("create returned existing issue", "warning", warn, "issue", issue.ID)
	}
	result := issueToData(&issue)
	return &result, nil
}

// shouldAssignBeforeStatus reports whether a requested assignee change must be
// applied before the status transition. For review/blocked targets the claim lock
// is released as the current assignee inside applyStatusUpdate, so the assign is
// deferred to keep that identity intact (LOOM-1); for every other status target
// the assign is safe to apply first.
func shouldAssignBeforeStatus(params backend.UpdateParams) bool {
	return params.Assignee != nil && params.Status != nil &&
		*params.Status != "in_progress" && *params.Status != "open" &&
		*params.Status != "review" && *params.Status != "blocked"
}

func (b *FleetBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	if params.Claim {
		return backend.ErrValidation("Update", "Claim field is not supported in FleetBackend.Update; use ClaimIssue instead")
	}
	if id == "" {
		return backend.ErrValidation("Update", "id must not be empty")
	}
	handled := false

	req := updateParamsToPatchRequest(params)
	if len(req) > 0 {
		if _, err := b.exec(ctx, "Update", "PATCH", "/issues/"+url.PathEscape(id), req); err != nil {
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
		return backend.ErrValidation("Update", "no FleetDB-supported fields were provided")
	}
	return nil
}

func (b *FleetBackend) applyStatusUpdate(ctx context.Context, id string, params backend.UpdateParams) (bool, error) {
	if params.Status == nil {
		return false, nil
	}
	target := strings.TrimSpace(*params.Status)
	if target == "" {
		return false, backend.ErrValidation("Update", "status must not be empty")
	}

	current, err := b.Get(ctx, id)
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
			return false, backend.ErrValidation("Update", "assignee or configured actor is required to claim an issue")
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
		return false, backend.ErrValidation("Update", "unsupported status for FleetDB workflow: "+target)
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
		return backend.ErrValidation("ReleaseClaim", "id must not be empty")
	}
	if actor == "" {
		return backend.ErrValidation("ReleaseClaim", "actor must not be empty")
	}
	current, err := b.Get(ctx, id)
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
func (b *FleetBackend) transitionToBlockedOrReview(ctx context.Context, id, target string, current *backend.IssueDetailData) error {
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

func (b *FleetBackend) transitionToOpen(ctx context.Context, id string, current *backend.IssueDetailData, clearAssignee bool) error {
	if current == nil {
		return backend.ErrNotFound("Update", "issue not found")
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

func (b *FleetBackend) claimActor(assignee *string, current *backend.IssueDetailData) string {
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
	return time.Time{}, backend.ErrValidation("Update", "defer_until must be RFC3339")
}

func (b *FleetBackend) applyLabelUpdates(ctx context.Context, id string, params backend.UpdateParams) error {
	for _, label := range params.AddLabels {
		if err := b.AddLabel(ctx, id, label); err != nil {
			return err
		}
		if err := b.waitForLabelState(ctx, id, label, true); err != nil {
			return err
		}
	}
	for _, label := range params.RemoveLabels {
		if err := b.RemoveLabel(ctx, id, label); err != nil {
			return err
		}
		if err := b.waitForLabelState(ctx, id, label, false); err != nil {
			return err
		}
	}
	if len(params.SetLabels) == 0 {
		return nil
	}
	current, err := b.Get(ctx, id)
	if err != nil {
		return err
	}
	for _, label := range current.Labels {
		if !containsString(params.SetLabels, label) {
			if err := b.RemoveLabel(ctx, id, label); err != nil {
				return err
			}
			if err := b.waitForLabelState(ctx, id, label, false); err != nil {
				return err
			}
		}
	}
	for _, label := range params.SetLabels {
		if !containsString(current.Labels, label) {
			if err := b.AddLabel(ctx, id, label); err != nil {
				return err
			}
			if err := b.waitForLabelState(ctx, id, label, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *FleetBackend) waitForLabelState(ctx context.Context, id, label string, wantPresent bool) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	var lastErr error
	for {
		detail, err := b.Get(ctx, id)
		if err == nil && detail != nil && containsString(detail.Labels, label) == wantPresent {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return backend.ErrTimeout("Update", "label projection did not settle", ctx.Err())
		case <-timeout.C:
			return backend.ErrTimeout("Update", "label projection did not settle", lastErr)
		case <-ticker.C:
		}
	}
}

func hasLabelUpdate(params backend.UpdateParams) bool {
	return len(params.AddLabels) > 0 || len(params.RemoveLabels) > 0 || len(params.SetLabels) > 0
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// ClaimIssue atomically claims an issue via the fleet claim endpoint.
//
// fleet-db's claim endpoint is per-issue: POST /issues/{id}/claim with an
// optional {"lock_ttl": seconds} body. A zero TTL asks the server to use its
// default; positive sub-second TTLs round up to one second because the wire
// contract is second-granular.
func (b *FleetBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	body, err := claimIssueBody(lockTTL)
	if err != nil {
		return err
	}
	_, err = b.exec(ctx, "ClaimIssue", "POST", "/issues/"+url.PathEscape(id)+"/claim", body)
	return err
}

// DeferIssue defers an issue via fleet-db's workflow endpoint. A zero until
// means status-only defer with no end date.
func (b *FleetBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	if id == "" {
		return backend.ErrValidation("DeferIssue", "id must not be empty")
	}
	return b.deferIssue(ctx, id, until)
}

// UndeferIssue restores a deferred issue to "open" status and clears defer_until.
func (b *FleetBackend) UndeferIssue(ctx context.Context, id string) error {
	if id == "" {
		return backend.ErrValidation("UndeferIssue", "id must not be empty")
	}
	_, callErr := b.exec(ctx, "UndeferIssue", "POST", "/issues/"+url.PathEscape(id)+"/undefer", nil)
	return callErr
}

func (b *FleetBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
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
		return nil, backend.ErrInternal("Close", "empty response from server", nil)
	}
	var cr closeResultJSON
	if err := json.Unmarshal(resp.Data, &cr); err != nil {
		return nil, backend.ErrInternal("Close", "unmarshal response", err)
	}
	if cr.Closed == nil && len(cr.Unblocked) == 0 {
		var issue types.Issue
		if err := json.Unmarshal(resp.Data, &issue); err == nil && issue.ID != "" {
			closed := issueToData(&issue)
			return &backend.CloseResult{Closed: &closed, Unblocked: []backend.IssueData{}}, nil
		}
	}
	return closeResultJSONToData(&cr), nil
}

func (b *FleetBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	if id == "" {
		return backend.ErrValidation("Reopen", "id must not be empty")
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
	// Record reason as a comment per the IssueBackend interface contract.
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

func (b *FleetBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	if len(params.IDs) == 0 {
		return backend.ErrValidation("Delete", "IDs must not be empty")
	}
	// The server's DELETE endpoint handles single issue. Delete each one.
	for _, id := range params.IDs {
		_, err := b.exec(ctx, "Delete", "DELETE", "/issues/"+url.PathEscape(id), nil)
		if err != nil {
			if params.Force && backend.IsKind(err, backend.KindNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}
