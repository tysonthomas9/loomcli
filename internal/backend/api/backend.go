// Package api implements backend.IssueBackend as an HTTP REST client against
// a loom server's workspace-scoped API endpoints. It is the remote-mode
// counterpart to the local FleetDB backend: when a CLI command is run with
// --server URL, it dispatches issue operations through this backend.
//
// Unlike the fleet backend (which talks to a dedicated fleet coordinator),
// api.Backend talks to the regular loom server and uses types generated from
// api/openapi.yaml for request/response wire format. Authentication is
// layered in via an injected *http.Client (typically wrapping
// internal/httpclient.Client for OIDC device-flow support).
package api

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

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// maxResponseBody limits response body reads to 50MB to prevent OOM on
// pathological responses.
const maxResponseBody = 50 << 20

// APIBackend implements backend.IssueBackend by forwarding calls to a loom
// server's REST API. It is safe for concurrent use.
type APIBackend struct {
	client      *http.Client
	baseURL     string // e.g., "http://localhost:8080" (no trailing slash)
	workspaceID string
}

// Compile-time interface check.
var _ backend.IssueBackend = (*APIBackend)(nil)

// apiResponse is the JSON envelope returned by loom server endpoints that
// follow the { success, data, error } convention.
type apiResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// New creates an APIBackend with the given configuration. It does NOT
// perform any network I/O — the first request triggers auth discovery and
// transport-level errors. This lazy behavior lets the backend be constructed
// before the server is known to be reachable.
func New(cfg Config) (*APIBackend, error) {
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
		httpClient = &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
			Timeout:   30 * time.Second,
		}
	}

	return &APIBackend{
		client:      httpClient,
		baseURL:     baseURL,
		workspaceID: cfg.WorkspaceID,
	}, nil
}

// BackendName returns the backend identifier string ("api").
func (b *APIBackend) BackendName() string { return "api" }

// workspaceBasePath returns the workspace-scoped URL path prefix for this
// backend, with the workspace ID URL-encoded.
func (b *APIBackend) workspaceBasePath() string {
	return "/api/workspaces/" + url.PathEscape(b.workspaceID)
}

// doRequest executes an HTTP request against the workspace-scoped API and
// parses the JSON envelope. Path must start with "/".
func (b *APIBackend) doRequest(ctx context.Context, method, path string, body interface{}) (*apiResponse, int, error) {
	return b.doRequestHeaders(ctx, method, path, body, nil)
}

// doRequestHeaders is doRequest with extra request headers (e.g. the
// idempotency headers on create, which must travel out-of-band because
// fleet-db's strict JSON decode rejects unknown body fields).
func (b *APIBackend) doRequestHeaders(ctx context.Context, method, path string, body interface{}, headers map[string]string) (*apiResponse, int, error) {
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
func (b *APIBackend) exec(ctx context.Context, op, method, path string, body interface{}) (*apiResponse, error) {
	return b.execHeaders(ctx, op, method, path, body, nil)
}

// execHeaders is exec with extra request headers.
func (b *APIBackend) execHeaders(ctx context.Context, op, method, path string, body interface{}, headers map[string]string) (*apiResponse, error) {
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

// unmarshalIssueList unmarshals a []gen.Issue response and converts to
// []backend.IssueData. Used by List, Ready, GetChildren, and SearchIssues.
func unmarshalIssueList(resp *apiResponse, op string) ([]backend.IssueData, error) {
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	var issues []gen.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal(op, "unmarshal response", err)
	}
	result := make([]backend.IssueData, 0, len(issues))
	for _, i := range issues {
		result = append(result, issueToData(i))
	}
	return result, nil
}

// --- Query operations ---

func (b *APIBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	resp, err := b.exec(ctx, "Get", http.MethodGet, "/issues/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrNotFound("Get", "issue not found")
	}
	var issue gen.IssueResponse
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, backend.ErrInternal("Get", "unmarshal response", err)
	}
	result := issueResponseToDetailData(issue)
	return &result, nil
}

func (b *APIBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	path := "/issues"
	if q := listOptsToQuery(opts); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "List", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "List")
}

func (b *APIBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	path := "/ready"
	if q := readyOptsToQuery(opts); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Ready", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "Ready")
}

func (b *APIBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	path := "/blocked"
	if q := blockedOptsToQuery(opts); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Blocked", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	var issues []gen.BlockedIssue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal("Blocked", "unmarshal response", err)
	}
	result := make([]backend.IssueData, 0, len(issues))
	for _, i := range issues {
		result = append(result, blockedIssueToData(i))
	}
	return result, nil
}

// Stats fetches per-workspace statistics. The server returns a raw
// Statistics object directly (not wrapped in the envelope) at
// GET /api/workspaces/{ws}/stats.
func (b *APIBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	// The /stats endpoint does not use the envelope — call via raw doRequest
	// and decode directly into Statistics.
	fullURL := b.baseURL + b.workspaceBasePath() + "/stats"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, backend.ErrInternal("Stats", "create request", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, classifyTransportError("Stats", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, backend.ErrInternal("Stats", "read response body", err)
	}
	if resp.StatusCode >= 400 {
		return nil, classifyHTTPError("Stats", resp.StatusCode, apiResponse{Error: string(body)})
	}
	var stats gen.Statistics
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, backend.ErrInternal("Stats", "unmarshal response", err)
	}
	result := statisticsToData(stats)
	return &result, nil
}

func (b *APIBackend) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, backend.ErrNotImplemented("Count", "server has no count endpoint")
}

// GetChildren returns the direct children of an issue by calling /issues with
// a parent filter. Returns an empty slice if the issue has no children.
func (b *APIBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	if id == "" {
		return nil, backend.ErrValidation("GetChildren", "id must not be empty")
	}
	path := "/issues?parent_id=" + url.QueryEscape(id)
	resp, err := b.exec(ctx, "GetChildren", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "GetChildren")
}

// SearchIssues performs a full-text search via the /issues endpoint using the
// q query parameter. Returns an empty slice if no results match.
// Note: the loom server uses "q" (not "query") for its search param — see
// internal/backend/api/params.go addListSearchFilters.
func (b *APIBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	if query == "" {
		return nil, backend.ErrValidation("SearchIssues", "query must not be empty")
	}
	if limit < 0 {
		return nil, backend.ErrValidation("SearchIssues", "limit must not be negative")
	}
	path := "/issues?q=" + url.QueryEscape(query)
	if limit > 0 {
		path += "&limit=" + strconv.Itoa(limit)
	}
	resp, err := b.exec(ctx, "SearchIssues", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "SearchIssues")
}

// --- Mutation operations ---

func (b *APIBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	req := createParamsToCreateRequest(params)
	resp, err := b.execHeaders(ctx, "Create", http.MethodPost, "/issues", req, params.IdempotencyHeaders())
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("Create", "empty response from server", nil)
	}
	var issue gen.IssueResponse
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, backend.ErrInternal("Create", "unmarshal response", err)
	}
	result := issueResponseToData(issue)
	return &result, nil
}

func (b *APIBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	if params.Claim {
		return backend.ErrValidation("Update", "Claim field is not supported in APIBackend.Update; use APIBackend.ClaimIssue instead")
	}
	req := updateParamsToPatchRequest(params)
	_, err := b.exec(ctx, "Update", http.MethodPatch, "/issues/"+url.PathEscape(id), req)
	return err
}

// ClaimIssue atomically claims an issue via POST /issues/{id}/claim. A zero
// TTL asks the server to use its default; positive TTLs are forwarded as
// second-granular lock_ttl values. Returns KindConflict when the issue is
// already claimed by a different agent and KindNotFound when it does not
// exist.
func (b *APIBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	body, err := claimIssueBody(lockTTL)
	if err != nil {
		return err
	}
	path := "/issues/" + url.PathEscape(id) + "/claim"
	_, err = b.exec(ctx, "ClaimIssue", http.MethodPost, path, body)
	return err
}

// ReleaseIssueLock releases only the operational lock on the issue. The
// generic API backend (a thin OpenAPI client) does not expose a lock-only
// release endpoint, so this returns KindNotImplemented and lets callers fall
// back to TTL expiry. Use the fleet backend for explicit lock release.
func (b *APIBackend) ReleaseIssueLock(_ context.Context, _, _ string) error {
	return backend.ErrNotImplemented("ReleaseIssueLock", "APIBackend does not support explicit lock release; rely on TTL expiry")
}

func claimIssueBody(lockTTL time.Duration) (any, error) {
	if lockTTL < 0 {
		return nil, backend.ErrValidation("ClaimIssue", "lockTTL must not be negative")
	}
	if lockTTL == 0 {
		return nil, nil
	}
	seconds := int((lockTTL + time.Second - time.Nanosecond) / time.Second)
	return struct {
		LockTTL int `json:"lock_ttl"`
	}{LockTTL: seconds}, nil
}

// DeferIssue defers an issue via PATCH with status="deferred" and optional
// defer_until. A zero `until` means status-only defer with no end date.
func (b *APIBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	if id == "" {
		return backend.ErrValidation("DeferIssue", "id must not be empty")
	}
	status := gen.PatchIssueRequestStatus("deferred")
	req := gen.PatchIssueRequest{Status: &status}
	if !until.IsZero() {
		formatted := until.Format(time.RFC3339)
		req.DeferUntil = &formatted
	}
	_, err := b.exec(ctx, "DeferIssue", http.MethodPatch, "/issues/"+url.PathEscape(id), req)
	return err
}

// UndeferIssue restores a deferred issue to "open" status and clears the
// defer_until field by sending an empty string.
func (b *APIBackend) UndeferIssue(ctx context.Context, id string) error {
	if id == "" {
		return backend.ErrValidation("UndeferIssue", "id must not be empty")
	}
	status := gen.PatchIssueRequestStatus("open")
	emptyStr := ""
	req := gen.PatchIssueRequest{Status: &status, DeferUntil: &emptyStr}
	_, err := b.exec(ctx, "UndeferIssue", http.MethodPatch, "/issues/"+url.PathEscape(id), req)
	return err
}

func (b *APIBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
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
	closed := backend.IssueData{ID: id}
	return &backend.CloseResult{
		Closed:    &closed,
		Unblocked: []backend.IssueData{},
	}, nil
}

// Archive tombstones an issue via the dedicated archive route. tombstone is
// not a settable status on PATCH, so this cannot be expressed as an update.
func (b *APIBackend) Archive(ctx context.Context, id string, params backend.ArchiveParams) error {
	if id == "" {
		return backend.ErrValidation("Archive", "id must not be empty")
	}
	req := gen.ArchiveRequest{}
	if params.Reason != "" {
		req.Reason = &params.Reason
	}
	_, err := b.exec(ctx, "Archive", http.MethodPost, "/issues/"+url.PathEscape(id)+"/archive", req)
	return err
}

// Unarchive restores an archived issue via the dedicated unarchive route.
func (b *APIBackend) Unarchive(ctx context.Context, id string) error {
	if id == "" {
		return backend.ErrValidation("Unarchive", "id must not be empty")
	}
	_, err := b.exec(ctx, "Unarchive", http.MethodPost, "/issues/"+url.PathEscape(id)+"/unarchive", nil)
	return err
}

func (b *APIBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	if id == "" {
		return backend.ErrValidation("Reopen", "id must not be empty")
	}
	status := gen.PatchIssueRequestStatus("open")
	req := gen.PatchIssueRequest{Status: &status}
	_, err := b.exec(ctx, "Reopen", http.MethodPatch, "/issues/"+url.PathEscape(id), req)
	if err != nil {
		return err
	}
	// Record reason as a comment per the IssueBackend contract. Best-effort:
	// the status transition already succeeded.
	if params.Reason != "" {
		_, _ = b.exec(ctx, "Reopen", http.MethodPost, "/issues/"+url.PathEscape(id)+"/comments", gen.CommentRequest{Text: params.Reason})
	}
	return nil
}

func (b *APIBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	if len(params.IDs) == 0 {
		return backend.ErrValidation("Delete", "IDs must not be empty")
	}
	for _, id := range params.IDs {
		_, err := b.exec(ctx, "Delete", http.MethodDelete, "/issues/"+url.PathEscape(id), nil)
		if err != nil {
			if params.Force && backend.IsKind(err, backend.KindNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

// --- Dependency operations ---

func (b *APIBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	req := gen.AddDependencyRequest{
		DependsOnId: params.ToID,
	}
	if params.DepType != "" {
		req.DepType = &params.DepType
	}
	_, err := b.exec(ctx, "AddDependency", http.MethodPost, "/issues/"+url.PathEscape(params.FromID)+"/dependencies", req)
	return err
}

func (b *APIBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	path := "/issues/" + url.PathEscape(params.FromID) + "/dependencies/" + url.PathEscape(params.ToID)
	_, err := b.exec(ctx, "RemoveDependency", http.MethodDelete, path, nil)
	return err
}

// --- Label operations ---

func (b *APIBackend) AddLabel(ctx context.Context, id, label string) error {
	addLabels := []string{label}
	req := gen.PatchIssueRequest{AddLabels: &addLabels}
	_, err := b.exec(ctx, "AddLabel", http.MethodPatch, "/issues/"+url.PathEscape(id), req)
	return err
}

func (b *APIBackend) RemoveLabel(ctx context.Context, id, label string) error {
	removeLabels := []string{label}
	req := gen.PatchIssueRequest{RemoveLabels: &removeLabels}
	_, err := b.exec(ctx, "RemoveLabel", http.MethodPatch, "/issues/"+url.PathEscape(id), req)
	return err
}

// --- Comment operations ---

func (b *APIBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	detail, err := b.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if detail.Comments == nil {
		return []backend.CommentData{}, nil
	}
	return detail.Comments, nil
}

func (b *APIBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	req := gen.CommentRequest{Text: params.Text}
	resp, err := b.exec(ctx, "AddComment", http.MethodPost, "/issues/"+url.PathEscape(params.IssueID)+"/comments", req)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("AddComment", "empty response from server", nil)
	}
	var comment gen.Comment
	if err := json.Unmarshal(resp.Data, &comment); err != nil {
		return nil, backend.ErrInternal("AddComment", "unmarshal response", err)
	}
	result := commentToData(comment)
	// Server may not populate IssueID in the response; ensure we pass it
	// through from the params.
	if result.IssueID == "" {
		result.IssueID = params.IssueID
	}
	return &result, nil
}

// --- Event operations ---

func (b *APIBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	path := "/issues/" + url.PathEscape(id) + "/events"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	resp, err := b.exec(ctx, "ListEvents", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.EventData{}, nil
	}
	var events []gen.IssueEvent
	if err := json.Unmarshal(resp.Data, &events); err != nil {
		return nil, backend.ErrInternal("ListEvents", "unmarshal response", err)
	}
	result := make([]backend.EventData, 0, len(events))
	for _, e := range events {
		result = append(result, eventToData(e))
	}
	return result, nil
}

// --- Batch operations ---

func (b *APIBackend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, backend.ErrNotImplemented("Batch", "server has no batch endpoint")
}

// --- Mutation polling ---

func (b *APIBackend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("GetMutations", "api backend does not implement SSE subscription; use the SSE endpoint directly")
}

func (b *APIBackend) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("WaitForMutations", "api backend does not implement SSE subscription; use the SSE endpoint directly")
}
