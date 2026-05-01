//go:build parity

package paritytest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// fleetDBAdapter is a minimal backend.IssueBackend implementation that speaks
// fleet-db's native HTTP API at /api/v1/{workspace}/... with bare JSON bodies.
//
// This exists because loomcli's production internal/backend/fleet.FleetBackend
// targets the loom-webui server's "/api/workspaces/{ws}/..." prefix and
// expects a {"success":true,"data":...} envelope — a shape fleet-db itself
// does not produce. See spawnFleetDB for the full rationale.
//
// Scope: only the methods exercised by the MVP fixture (crud_create_show) are
// implemented. Everything else is delegated to the embedded
// notImplementedBackend, which returns backend.ErrNotImplemented — so
// fixtures that hit an unwired method produce an informative diff rather
// than panicking. Each real method added here overrides the embedded stub
// via method set shadowing.
type fleetDBAdapter struct {
	notImplementedBackend

	client      *http.Client
	baseURL     string // e.g., "http://127.0.0.1:8080"
	workspaceID string
	actor       string
}

var _ backend.IssueBackend = (*fleetDBAdapter)(nil)

// newFleetDBAdapter builds an adapter pointing at a local fleet-db server.
func newFleetDBAdapter(baseURL, workspaceID, actor string) *fleetDBAdapter {
	return &fleetDBAdapter{
		client:      &http.Client{Timeout: 10 * time.Second},
		baseURL:     baseURL,
		workspaceID: workspaceID,
		actor:       actor,
	}
}

// BackendName identifies this adapter in diff report output. We use "fleet-db"
// to match upstream report expectations — the outer report's Mode field
// distinguishes fleet/beads columns.
func (a *fleetDBAdapter) BackendName() string { return "fleet-db" }

// wsPath builds the full URL for a workspace-scoped path.
func (a *fleetDBAdapter) wsPath(suffix string) string {
	return a.baseURL + "/api/v1/" + url.PathEscape(a.workspaceID) + suffix
}

// doJSON executes an HTTP request with standard actor/content headers and
// returns raw body bytes + status. Callers parse the shape.
func (a *fleetDBAdapter) doJSON(ctx context.Context, method, fullURL string, reqBody interface{}) ([]byte, int, error) {
	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Actor", a.actor)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	// Cap at 10 MB — parity responses are small.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

// classifyStatus maps an HTTP status + fleet-db error body to a
// *backend.BackendError so the diff layer sees the same ErrorKind whether
// the backend is beads or fleet-db.
func (a *fleetDBAdapter) classifyStatus(op string, status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}

	var env struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	msg := ""
	code := ""
	if env.Error != nil {
		msg = env.Error.Message
		code = env.Error.Code
	}
	if msg == "" {
		msg = string(body)
	}

	switch status {
	case http.StatusNotFound:
		return backend.ErrNotFound(op, msg)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return backend.ErrValidation(op, msg)
	case http.StatusConflict:
		return backend.ErrConflict(op, msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return backend.ErrUnavailable(op, msg, nil)
	default:
		return backend.ErrInternal(op, fmt.Sprintf("HTTP %d (%s)", status, code), nil)
	}
}

// --- Implemented methods (MVP surface) ---

// Create posts a new issue to /api/v1/{ws}/issues and returns the slim
// projection. fleet-db's response is a single bare JSON object matching
// models.Issue — we translate to backend.IssueData.
func (a *fleetDBAdapter) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	type req struct {
		Title       string   `json:"title"`
		Description string   `json:"description,omitempty"`
		Priority    int      `json:"priority"`
		Type        string   `json:"type"`
		Assignee    string   `json:"assignee,omitempty"`
		Owner       string   `json:"owner,omitempty"`
		Labels      []string `json:"labels,omitempty"`
		ParentID    string   `json:"parent_id,omitempty"`
	}
	r := req{
		Title:       params.Title,
		Description: params.Description,
		Priority:    params.Priority,
		Type:        params.IssueType,
		Assignee:    params.Assignee,
		Owner:       params.Owner,
		Labels:      params.Labels,
		ParentID:    params.Parent,
	}
	if r.Type == "" {
		r.Type = "task"
	}

	raw, status, err := a.doJSON(ctx, http.MethodPost, a.wsPath("/issues"), r)
	if err != nil {
		return nil, backend.ErrInternal("Create", "http", err)
	}
	if cerr := a.classifyStatus("Create", status, raw); cerr != nil {
		return nil, cerr
	}

	result, err := parseFleetIssue(raw)
	if err != nil {
		return nil, backend.ErrInternal("Create", "decode response", err)
	}
	return result, nil
}

// Get issues GET /api/v1/{ws}/issues/{id}. Returns ErrNotFound (404) or a
// slim-projected IssueData. Full IssueDetailData fields (Dependencies,
// Dependents, Comments) are left zero until a fixture exercises them — the
// current MVP fixture only reads title/status/priority from Get.
func (a *fleetDBAdapter) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	if id == "" {
		return nil, backend.ErrValidation("Get", "id must not be empty")
	}
	raw, status, err := a.doJSON(ctx, http.MethodGet, a.wsPath("/issues/"+url.PathEscape(id)), nil)
	if err != nil {
		return nil, backend.ErrInternal("Get", "http", err)
	}
	if cerr := a.classifyStatus("Get", status, raw); cerr != nil {
		return nil, cerr
	}
	slim, err := parseFleetIssue(raw)
	if err != nil {
		return nil, backend.ErrInternal("Get", "decode response", err)
	}
	detail := &backend.IssueDetailData{IssueData: *slim}
	if deps, err := a.fetchDependencies(ctx, id); err == nil {
		detail.Dependencies = deps
	}
	return detail, nil
}

func (a *fleetDBAdapter) listIssueIDs(ctx context.Context) ([]string, error) {
	raw, status, err := a.doJSON(ctx, http.MethodGet, a.wsPath("/issues"), nil)
	if err != nil {
		return nil, backend.ErrInternal("List", "http", err)
	}
	if cerr := a.classifyStatus("List", status, raw); cerr != nil {
		return nil, cerr
	}
	var wrap struct {
		Issues []struct {
			ID string `json:"id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, backend.ErrInternal("List", "decode response", err)
	}
	ids := make([]string, 0, len(wrap.Issues))
	for _, issue := range wrap.Issues {
		ids = append(ids, issue.ID)
	}
	return ids, nil
}

// Update issues PATCH /api/v1/{ws}/issues/{id}. fleet-db's PATCH body is a
// narrow subset of UpdateParams (title, description, priority, type, design,
// notes, owner, due_at) and rejects unknown fields with 400 — so we only
// forward the overlap. Status changes must go through /close and /reopen;
// label mutations go through /labels; everything else surfaces as
// ErrNotImplemented at the method level so the diff is honest about what's
// not wired up.
//
// Our interface returns only error, so after a successful PATCH the runner
// relies on a follow-up issue.show to surface the post-update state.
func (a *fleetDBAdapter) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	if id == "" {
		return backend.ErrValidation("Update", "id must not be empty")
	}
	if params.Claim {
		return backend.ErrValidation("Update", "Claim field is not supported in fleetDBAdapter.Update")
	}

	// Build the narrow PATCH body. Nil pointers stay absent so fleet-db's
	// "don't change" semantics apply.
	req := map[string]any{}
	if params.Title != nil {
		req["title"] = *params.Title
	}
	if params.Description != nil {
		req["description"] = *params.Description
	}
	if params.Priority != nil {
		req["priority"] = *params.Priority
	}
	if params.IssueType != nil {
		req["type"] = *params.IssueType
	}
	if params.Design != nil {
		req["design"] = *params.Design
	}
	if params.Notes != nil {
		req["notes"] = *params.Notes
	}
	if params.Owner != nil {
		req["owner"] = *params.Owner
	}
	if params.DueAt != nil && *params.DueAt != "" {
		req["due_at"] = *params.DueAt
	}

	if len(req) == 0 {
		// Nothing the native API accepts — pretend success so the diff focuses
		// on the post-update show step. This mirrors bd's treatment of empty
		// updates as a no-op.
		return nil
	}

	raw, status, err := a.doJSON(ctx, http.MethodPatch, a.wsPath("/issues/"+url.PathEscape(id)), req)
	if err != nil {
		return backend.ErrInternal("Update", "http", err)
	}
	if cerr := a.classifyStatus("Update", status, raw); cerr != nil {
		return cerr
	}
	return nil
}

// Close issues POST /api/v1/{ws}/issues/{id}/close with an optional
// {"reason": "..."} body. fleet-db returns the closed Issue object — we
// wrap it in a backend.CloseResult with Unblocked left empty (fleet-db
// does not yet return unblocked-on-close; see fleet-q6ox). Session /
// SuggestNext / Force fields on CloseParams are not supported by fleet-db's
// native API and are silently dropped — fixtures that rely on their side
// effects will naturally diff against bd.
func (a *fleetDBAdapter) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	if id == "" {
		return nil, backend.ErrValidation("Close", "id must not be empty")
	}
	body := map[string]any{}
	if params.Reason != "" {
		body["reason"] = params.Reason
	}
	raw, status, err := a.doJSON(ctx, http.MethodPost, a.wsPath("/issues/"+url.PathEscape(id)+"/close"), body)
	if err != nil {
		return nil, backend.ErrInternal("Close", "http", err)
	}
	if cerr := a.classifyStatus("Close", status, raw); cerr != nil {
		return nil, cerr
	}
	closed, err := parseFleetIssue(raw)
	if err != nil {
		return nil, backend.ErrInternal("Close", "decode response", err)
	}
	return &backend.CloseResult{Closed: closed}, nil
}

// Reopen issues POST /api/v1/{ws}/issues/{id}/reopen. fleet-db's endpoint
// returns the reopened Issue but the IssueBackend.Reopen signature only
// returns error — we mirror the production FleetBackend pattern and discard
// the response body. ReopenParams.Reason is best-effort-recorded as a
// comment via POST /comments if non-empty; a comment failure is non-fatal
// since the status transition already succeeded.
func (a *fleetDBAdapter) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	if id == "" {
		return backend.ErrValidation("Reopen", "id must not be empty")
	}
	raw, status, err := a.doJSON(ctx, http.MethodPost, a.wsPath("/issues/"+url.PathEscape(id)+"/reopen"), nil)
	if err != nil {
		return backend.ErrInternal("Reopen", "http", err)
	}
	if cerr := a.classifyStatus("Reopen", status, raw); cerr != nil {
		return cerr
	}
	if params.Reason != "" {
		// Best-effort comment. fleet-db uses {"body": "..."} for comments;
		// see CreateCommentRequest in fleet-db/internal/api/request.go.
		commentBody := map[string]string{"body": params.Reason}
		_, _, _ = a.doJSON(ctx, http.MethodPost, a.wsPath("/issues/"+url.PathEscape(id)+"/comments"), commentBody)
	}
	return nil
}

func (a *fleetDBAdapter) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	if lockTTL < 0 {
		return backend.ErrValidation("ClaimIssue", "lockTTL must not be negative")
	}
	var body any
	if lockTTL > 0 {
		seconds := int((lockTTL + time.Second - time.Nanosecond) / time.Second)
		body = struct {
			LockTTL int `json:"lock_ttl"`
		}{LockTTL: seconds}
	}
	raw, status, err := a.doJSON(ctx, http.MethodPost, a.wsPath("/issues/"+url.PathEscape(id)+"/claim"), body)
	if err != nil {
		return backend.ErrInternal("ClaimIssue", "http", err)
	}
	return a.classifyStatus("ClaimIssue", status, raw)
}

type heartbeatResult struct {
	Success bool   `json:"success"`
	TTL     int    `json:"ttl,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (a *fleetDBAdapter) heartbeatWorker(ctx context.Context, workerID string) (*heartbeatResult, error) {
	if workerID == "" {
		return nil, backend.ErrValidation("HeartbeatWorker", "workerID must not be empty")
	}
	raw, status, err := a.doJSON(ctx, http.MethodPost, a.wsPath("/workers/"+url.PathEscape(workerID)+"/heartbeat"), nil)
	if err != nil {
		return nil, backend.ErrInternal("HeartbeatWorker", "http", err)
	}
	if cerr := a.classifyStatus("HeartbeatWorker", status, raw); cerr != nil {
		return nil, cerr
	}
	var result heartbeatResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, backend.ErrInternal("HeartbeatWorker", "decode response", err)
	}
	return &result, nil
}

func (a *fleetDBAdapter) Delete(ctx context.Context, params backend.DeleteParams) error {
	if len(params.IDs) == 0 {
		return backend.ErrValidation("Delete", "IDs must not be empty")
	}
	for _, id := range params.IDs {
		raw, status, err := a.doJSON(ctx, http.MethodDelete, a.wsPath("/issues/"+url.PathEscape(id)), nil)
		if err != nil {
			return backend.ErrInternal("Delete", "http", err)
		}
		if cerr := a.classifyStatus("Delete", status, raw); cerr != nil {
			if params.Force && backend.IsKind(cerr, backend.KindNotFound) {
				continue
			}
			return cerr
		}
	}
	return nil
}

func (a *fleetDBAdapter) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	if params.FromID == "" || params.ToID == "" {
		return backend.ErrValidation("AddDependency", "from_id and to_id must not be empty")
	}
	depType := params.DepType
	if depType == "" {
		depType = "blocks"
	}
	body := map[string]string{
		"depends_on_id": params.ToID,
		"type":          depType,
	}
	raw, status, err := a.doJSON(ctx, http.MethodPost, a.wsPath("/issues/"+url.PathEscape(params.FromID)+"/deps"), body)
	if err != nil {
		return backend.ErrInternal("AddDependency", "http", err)
	}
	return a.classifyStatus("AddDependency", status, raw)
}

func (a *fleetDBAdapter) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	if params.FromID == "" || params.ToID == "" {
		return backend.ErrValidation("RemoveDependency", "from_id and to_id must not be empty")
	}
	path := "/issues/" + url.PathEscape(params.FromID) + "/deps/" + url.PathEscape(params.ToID)
	if params.DepType != "" {
		path += "?type=" + url.QueryEscape(params.DepType)
	}
	raw, status, err := a.doJSON(ctx, http.MethodDelete, a.wsPath(path), nil)
	if err != nil {
		return backend.ErrInternal("RemoveDependency", "http", err)
	}
	return a.classifyStatus("RemoveDependency", status, raw)
}

func (a *fleetDBAdapter) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	if params.IssueID == "" {
		return nil, backend.ErrValidation("AddComment", "issue_id must not be empty")
	}
	if params.Text == "" {
		return nil, backend.ErrValidation("AddComment", "text must not be empty")
	}
	raw, status, err := a.doJSON(ctx, http.MethodPost, a.wsPath("/issues/"+url.PathEscape(params.IssueID)+"/comments"), map[string]string{
		"body": params.Text,
	})
	if err != nil {
		return nil, backend.ErrInternal("AddComment", "http", err)
	}
	if cerr := a.classifyStatus("AddComment", status, raw); cerr != nil {
		return nil, cerr
	}
	comment, err := parseFleetComment(raw)
	if err != nil {
		return nil, backend.ErrInternal("AddComment", "decode response", err)
	}
	return comment, nil
}

func (a *fleetDBAdapter) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	if id == "" {
		return nil, backend.ErrValidation("ListComments", "id must not be empty")
	}
	raw, status, err := a.doJSON(ctx, http.MethodGet, a.wsPath("/issues/"+url.PathEscape(id)+"/comments"), nil)
	if err != nil {
		return nil, backend.ErrInternal("ListComments", "http", err)
	}
	if cerr := a.classifyStatus("ListComments", status, raw); cerr != nil {
		return nil, cerr
	}
	var wrap struct {
		Comments []fleetCommentJSON `json:"comments"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, backend.ErrInternal("ListComments", "decode response", err)
	}
	out := make([]backend.CommentData, 0, len(wrap.Comments))
	for _, c := range wrap.Comments {
		out = append(out, c.toData())
	}
	return out, nil
}

func (a *fleetDBAdapter) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	if id == "" {
		return nil, backend.ErrValidation("ListEvents", "id must not be empty")
	}
	path := "/issues/" + url.PathEscape(id) + "/history"
	if limit > 0 {
		path += "?limit=" + url.QueryEscape(fmt.Sprintf("%d", limit))
	}
	raw, status, err := a.doJSON(ctx, http.MethodGet, a.wsPath(path), nil)
	if err != nil {
		return nil, backend.ErrInternal("ListEvents", "http", err)
	}
	if cerr := a.classifyStatus("ListEvents", status, raw); cerr != nil {
		return nil, cerr
	}
	var wrap struct {
		History []struct {
			ID        string    `json:"id"`
			Timestamp time.Time `json:"timestamp"`
			Actor     string    `json:"actor"`
			Action    string    `json:"action"`
		} `json:"history"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, backend.ErrInternal("ListEvents", "decode response", err)
	}
	out := make([]backend.EventData, 0, len(wrap.History))
	for _, e := range wrap.History {
		out = append(out, backend.EventData{
			ID:        e.ID,
			IssueID:   id,
			Kind:      e.Action,
			Actor:     e.Actor,
			CreatedAt: e.Timestamp,
		})
	}
	return out, nil
}

func (a *fleetDBAdapter) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	results := make([]backend.BatchResult, len(ops))
	for i, op := range ops {
		switch strings.ToLower(op.Operation) {
		case "create":
			results[i] = a.batchCreateOne(ctx, op.Args)
		case "update":
			results[i] = a.batchUpdateOne(ctx, op.Args)
		case "close":
			results[i] = a.batchCloseOne(ctx, op.Args)
		case "delete":
			results[i] = a.batchDeleteOne(ctx, op.Args)
		default:
			results[i] = backend.BatchResult{
				Success: false,
				Error:   fmt.Sprintf("backend [%s] Batch: unsupported batch operation %q", backend.KindValidation, op.Operation),
			}
		}
	}
	return results, nil
}

func (a *fleetDBAdapter) batchCreateOne(ctx context.Context, raw json.RawMessage) backend.BatchResult {
	var p backend.CreateParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return batchFailure(backend.ErrValidation("Batch", "unmarshal create args: "+err.Error()))
	}
	created, err := a.Create(ctx, p)
	if err != nil {
		return batchFailure(err)
	}
	data, err := json.Marshal(created)
	if err != nil {
		return batchFailure(backend.ErrInternal("Batch", "marshal create result", err))
	}
	return backend.BatchResult{Success: true, Data: data}
}

func (a *fleetDBAdapter) batchUpdateOne(ctx context.Context, raw json.RawMessage) backend.BatchResult {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return batchFailure(backend.ErrValidation("Batch", "unmarshal update args: "+err.Error()))
	}
	if p.ID == "" {
		return batchFailure(backend.ErrValidation("Batch", "update op missing id"))
	}
	var params backend.UpdateParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return batchFailure(backend.ErrValidation("Batch", "unmarshal update args: "+err.Error()))
	}
	if err := a.Update(ctx, p.ID, params); err != nil {
		return batchFailure(err)
	}
	return backend.BatchResult{Success: true}
}

func (a *fleetDBAdapter) batchCloseOne(ctx context.Context, raw json.RawMessage) backend.BatchResult {
	var p struct {
		ID     string `json:"id"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return batchFailure(backend.ErrValidation("Batch", "unmarshal close args: "+err.Error()))
	}
	if p.ID == "" {
		return batchFailure(backend.ErrValidation("Batch", "close op missing id"))
	}
	if _, err := a.Close(ctx, p.ID, backend.CloseParams{Reason: p.Reason}); err != nil {
		return batchFailure(err)
	}
	return backend.BatchResult{Success: true}
}

func (a *fleetDBAdapter) batchDeleteOne(ctx context.Context, raw json.RawMessage) backend.BatchResult {
	var p struct {
		ID      string   `json:"id"`
		IDs     []string `json:"ids"`
		Reason  string   `json:"reason,omitempty"`
		Force   bool     `json:"force,omitempty"`
		Cascade bool     `json:"cascade,omitempty"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return batchFailure(backend.ErrValidation("Batch", "unmarshal delete args: "+err.Error()))
	}
	ids := p.IDs
	if p.ID != "" {
		ids = append([]string{p.ID}, ids...)
	}
	if err := a.Delete(ctx, backend.DeleteParams{
		IDs: ids, Reason: p.Reason, Force: p.Force, Cascade: p.Cascade,
	}); err != nil {
		return batchFailure(err)
	}
	return backend.BatchResult{Success: true}
}

func batchFailure(err error) backend.BatchResult {
	if err == nil {
		return backend.BatchResult{Success: false}
	}
	return backend.BatchResult{Success: false, Error: err.Error()}
}

func (a *fleetDBAdapter) fetchDependencies(ctx context.Context, id string) ([]backend.DependencyData, error) {
	raw, status, err := a.doJSON(ctx, http.MethodGet, a.wsPath("/issues/"+url.PathEscape(id)+"/deps"), nil)
	if err != nil {
		return nil, backend.ErrInternal("GetDependencies", "http", err)
	}
	if cerr := a.classifyStatus("GetDependencies", status, raw); cerr != nil {
		return nil, cerr
	}
	var wrap struct {
		Dependencies []struct {
			IssueID     string    `json:"issue_id"`
			DependsOnID string    `json:"depends_on_id"`
			Type        string    `json:"type"`
			CreatedAt   time.Time `json:"created_at"`
			CreatedBy   string    `json:"created_by"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, backend.ErrInternal("GetDependencies", "decode response", err)
	}
	out := make([]backend.DependencyData, 0, len(wrap.Dependencies))
	for _, d := range wrap.Dependencies {
		out = append(out, backend.DependencyData{
			IssueID:     d.IssueID,
			DependsOnID: d.DependsOnID,
			Type:        d.Type,
			CreatedAt:   d.CreatedAt,
			CreatedBy:   d.CreatedBy,
		})
	}
	return out, nil
}

type fleetCommentJSON struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issue_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func parseFleetComment(raw []byte) (*backend.CommentData, error) {
	var c fleetCommentJSON
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	data := c.toData()
	return &data, nil
}

func (c fleetCommentJSON) toData() backend.CommentData {
	return backend.CommentData{
		IssueID:   c.IssueID,
		Author:    c.Author,
		Text:      c.Body,
		CreatedAt: c.CreatedAt,
	}
}

// createFleetWorkspace POSTs /api/v1/admin/workspaces to create the default
// workspace used by the harness. fleet-db needs this before any issue ops
// will succeed.
func createFleetWorkspace(baseURL, key string) error {
	body := map[string]any{
		"key":         key,
		"name":        "Parity harness workspace",
		"description": "created by loomcli paritytest",
		"repos":       []string{"org/repo-a", "org/repo-b"},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		baseURL+"/api/v1/admin/workspaces", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", parityActor)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("create workspace: HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// parseFleetIssue decodes a fleet-db Issue JSON object into backend.IssueData.
// fleet-db uses models.Issue — we pick off the fields the diff cares about.
func parseFleetIssue(raw []byte) (*backend.IssueData, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := &backend.IssueData{
		ID:        asString(m["id"]),
		Title:     asString(m["title"]),
		Status:    asString(m["status"]),
		Priority:  asInt(m["priority"]),
		IssueType: asString(m["type"]),
		Assignee:  asString(m["assignee"]),
		Owner:     asString(m["owner"]),
		Parent:    asString(m["parent_id"]),
		CreatedBy: asString(m["created_by"]),
	}
	// Created/updated timestamps — optional, best effort.
	out.CreatedAt = asTime(m["created_at"])
	out.UpdatedAt = asTime(m["updated_at"])
	if labels, ok := m["labels"].([]any); ok {
		for _, l := range labels {
			if s, ok := l.(string); ok {
				out.Labels = append(out.Labels, s)
			}
		}
	}
	return out, nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func asTime(v any) time.Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
