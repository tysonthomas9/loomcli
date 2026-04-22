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
// implemented. Unimplemented methods return backend.ErrNotImplemented so
// fixtures that hit them produce an informative diff rather than panicking.
// Each method added here should be paired with a fixture that covers it.
type fleetDBAdapter struct {
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
	return &backend.IssueDetailData{IssueData: *slim}, nil
}

// --- Unimplemented methods: return ErrNotImplemented so fixtures that try
// them produce a clean diff rather than crashing. ---

func (a *fleetDBAdapter) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return nil, notImpl("List")
}

func (a *fleetDBAdapter) Ready(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, notImpl("Ready")
}

func (a *fleetDBAdapter) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, notImpl("Blocked")
}

func (a *fleetDBAdapter) Stats(_ context.Context) (*backend.StatsData, error) {
	return nil, notImpl("Stats")
}

func (a *fleetDBAdapter) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, notImpl("Count")
}

func (a *fleetDBAdapter) GetChildren(_ context.Context, _ string) ([]backend.IssueData, error) {
	return nil, notImpl("GetChildren")
}

func (a *fleetDBAdapter) SearchIssues(_ context.Context, _ string, _ int) ([]backend.IssueData, error) {
	return nil, notImpl("SearchIssues")
}

func (a *fleetDBAdapter) Update(_ context.Context, _ string, _ backend.UpdateParams) error {
	return notImpl("Update")
}

func (a *fleetDBAdapter) ClaimIssue(_ context.Context, _ string, _ time.Duration) error {
	return notImpl("ClaimIssue")
}

func (a *fleetDBAdapter) DeferIssue(_ context.Context, _ string, _ time.Time) error {
	return notImpl("DeferIssue")
}

func (a *fleetDBAdapter) UndeferIssue(_ context.Context, _ string) error {
	return notImpl("UndeferIssue")
}

func (a *fleetDBAdapter) Close(_ context.Context, _ string, _ backend.CloseParams) (*backend.CloseResult, error) {
	return nil, notImpl("Close")
}

func (a *fleetDBAdapter) Reopen(_ context.Context, _ string, _ backend.ReopenParams) error {
	return notImpl("Reopen")
}

func (a *fleetDBAdapter) Delete(_ context.Context, _ backend.DeleteParams) error {
	return notImpl("Delete")
}

func (a *fleetDBAdapter) AddDependency(_ context.Context, _ backend.DepAddParams) error {
	return notImpl("AddDependency")
}

func (a *fleetDBAdapter) RemoveDependency(_ context.Context, _ backend.DepRemoveParams) error {
	return notImpl("RemoveDependency")
}

func (a *fleetDBAdapter) AddLabel(_ context.Context, _, _ string) error {
	return notImpl("AddLabel")
}

func (a *fleetDBAdapter) RemoveLabel(_ context.Context, _, _ string) error {
	return notImpl("RemoveLabel")
}

func (a *fleetDBAdapter) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, notImpl("ListComments")
}

func (a *fleetDBAdapter) AddComment(_ context.Context, _ backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, notImpl("AddComment")
}

func (a *fleetDBAdapter) ListEvents(_ context.Context, _ string, _ int) ([]backend.EventData, error) {
	return nil, notImpl("ListEvents")
}

func (a *fleetDBAdapter) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, notImpl("Batch")
}

func (a *fleetDBAdapter) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, notImpl("GetMutations")
}

func (a *fleetDBAdapter) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, notImpl("WaitForMutations")
}

func notImpl(op string) error {
	return backend.ErrNotImplemented(op, "not implemented in paritytest fleetDBAdapter MVP")
}

// createFleetWorkspace POSTs /api/v1/admin/workspaces to create the default
// workspace used by the harness. fleet-db needs this before any issue ops
// will succeed.
func createFleetWorkspace(baseURL, key string) error {
	body := map[string]string{
		"key":         key,
		"name":        "Parity harness workspace",
		"description": "created by loomcli paritytest",
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
