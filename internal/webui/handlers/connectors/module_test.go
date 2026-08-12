package connectors

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// newTestServer wires the connectors module over a fresh memstore. No
// localSettingsDir is needed: these tests never set reuse_runtime_credential, so
// the Settings-token vault bridge is not exercised.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	NewModule(memstore.New(), "").Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	return resp.StatusCode, out
}

// TestCreateConnectorIsIdempotent pins the "ensure" contract the create-agent
// gallery relies on: the first create returns 201, and re-activating the same
// template (same connector_id) returns 200 with the existing connector rather
// than a 409 that would fail the whole activation.
func TestCreateConnectorIsIdempotent(t *testing.T) {
	srv := newTestServer(t)
	const path = "/api/workspaces/WS/connectors"
	req := map[string]any{"source": "github", "connector_id": "github"}

	status, raw := postJSON(t, srv, path, req)
	if status != http.StatusCreated {
		t.Fatalf("first create: status = %d, want 201 (body %s)", status, raw)
	}

	status, raw = postJSON(t, srv, path, req)
	if status != http.StatusOK {
		t.Fatalf("re-create: status = %d, want 200 ensure (body %s)", status, raw)
	}
	var conn struct {
		ConnectorID string `json:"connector_id"`
	}
	if err := json.Unmarshal(raw, &conn); err != nil {
		t.Fatalf("decode connector: %v (body %s)", err, raw)
	}
	if conn.ConnectorID != "github" {
		t.Fatalf("ensure returned connector_id = %q, want github", conn.ConnectorID)
	}
}

// TestCreateGrantIsIdempotent pins the same ensure contract for grants: a
// repeated grant returns 200 (not 409), while a genuinely different action is
// still created (201) — guarding findGrant's match-by-derived-id logic.
func TestCreateGrantIsIdempotent(t *testing.T) {
	srv := newTestServer(t)
	const grantsPath = "/api/workspaces/WS/connectors/github/grants"
	grant := map[string]any{
		"binding_id":       "s2-review-loop",
		"action":           "github.pull_request.read",
		"resource_pattern": "repo:octocat/hello",
	}

	status, raw := postJSON(t, srv, grantsPath, grant)
	if status != http.StatusCreated {
		t.Fatalf("first grant: status = %d, want 201 (body %s)", status, raw)
	}
	var first struct {
		GrantID string `json:"grant_id"`
	}
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("decode grant: %v (body %s)", err, raw)
	}
	if first.GrantID != "grant-s2-review-loop-github-pull_request-read" {
		t.Fatalf("derived grant_id = %q, want grant-s2-review-loop-github-pull_request-read", first.GrantID)
	}

	// Re-activating the same grant is exists-ok, not a 409.
	status, raw = postJSON(t, srv, grantsPath, grant)
	if status != http.StatusOK {
		t.Fatalf("re-grant: status = %d, want 200 ensure (body %s)", status, raw)
	}
	var second struct {
		GrantID string `json:"grant_id"`
	}
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("decode ensure grant: %v (body %s)", err, raw)
	}
	if second.GrantID != first.GrantID {
		t.Fatalf("ensure returned grant_id = %q, want the existing %q", second.GrantID, first.GrantID)
	}

	// A different action is a distinct grant (distinct derived id) — created.
	grant["action"] = "github.review.post"
	status, raw = postJSON(t, srv, grantsPath, grant)
	if status != http.StatusCreated {
		t.Fatalf("distinct-action grant: status = %d, want 201 (body %s)", status, raw)
	}
}
