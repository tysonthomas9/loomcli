package driverapi

// SDK v1 contract freeze (SP1): these tests pin the server side of the
// published @loom/sdk surface — driver op names, the explicitly registered
// two-segment routes, and the {code, message, retryable, details?} error
// envelope. A failure here means a BREAKING CHANGE to the frozen contract:
// update sdk/api-surface.v1.json, the SDK contract tests, and bump the SDK
// major version instead of silently renaming.

import (
	"net/http"
	"slices"
	"testing"
)

// frozenDriverOps is the v1 set of single-segment driver op names served by
// POST /api/workspaces/{ws}/driver/{op}. Removing or renaming any entry is a
// breaking change; additions must be reflected here and in the SDK manifest.
var frozenDriverOps = []string{
	"active-task-runs",
	"agent-orchestration-session",
	"claim-ready",
	"claim-task",
	"complete-task",
	"connector-dispatch",
	"deliver-agent-message",
	"deliver-lead-assignment",
	"emit-event",
	"epic-get",
	"epic-snapshot",
	"exec-task",
	"issue-add-label",
	"issue-comment",
	"issue-get",
	"issue-list",
	"issue-list-comments",
	"issue-remove-label",
	"issue-update",
	"list-agents",
	"recover-stale-tasks",
	"release-task",
	"role-get",
	"task-run-get",
	"update-agent-parent",
}

func TestContractDriverOpNamesFrozen(t *testing.T) {
	h := newTestHarness(t, "")
	got := make([]string, 0, len(h.module.ops))
	for op := range h.module.ops {
		got = append(got, op)
	}
	slices.Sort(got)
	if !slices.Equal(got, frozenDriverOps) {
		t.Fatalf("driver op surface changed (SDK v1 contract):\n got: %v\nwant: %v", got, frozenDriverOps)
	}
}

// TestContractExplicitRoutesFrozen proves the two-segment routes (which the
// generic {op} pattern cannot match) stay registered under their frozen
// paths: a request reaches the module handler instead of the mux 404.
func TestContractExplicitRoutesFrozen(t *testing.T) {
	h := newTestHarness(t, "")
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/WS/driver/watch/epic"},
		{http.MethodPost, "/api/workspaces/WS/driver/events/await"},
		{http.MethodGet, "/api/workspaces/WS/driver/events/awaits"},
		{http.MethodPost, "/api/workspaces/WS/driver/workflows/start"},
		{http.MethodPost, "/api/workspaces/WS/driver/workflows/await"},
	}
	for _, route := range routes {
		req, err := http.NewRequest(route.method, h.server.URL+route.path, nil)
		if err != nil {
			t.Fatalf("new request %s: %v", route.path, err)
		}
		for name, value := range h.ownerHeaders() {
			req.Header.Set(name, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do %s %s: %v", route.method, route.path, err)
		}
		_ = resp.Body.Close()
		// Reaching the handler yields either success or a structured JSON
		// error; the mux fallback would be a text/plain 404 / 405.
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			t.Fatalf("%s %s = %d: frozen route no longer registered", route.method, route.path, resp.StatusCode)
		}
	}
}

// TestContractErrorEnvelopeFrozen pins the envelope key set: exactly
// {code, message, retryable} plus the OPTIONAL additive details object.
func TestContractErrorEnvelopeFrozen(t *testing.T) {
	h := newTestHarness(t, "")

	t.Run("details carries machine-readable context on unknown_op", func(t *testing.T) {
		resp, decoded := h.do(t, opRequest{op: "definitely-not-an-op", headers: h.ownerHeaders()})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
		envelope := requireEnvelope(t, decoded)
		assertEnvelopeKeys(t, envelope, []string{"code", "details", "message", "retryable"})
		if code := envelope["code"]; code != "unknown_op" {
			t.Fatalf("code = %v, want unknown_op", code)
		}
		details, ok := envelope["details"].(map[string]any)
		if !ok {
			t.Fatalf("details = %v, want object", envelope["details"])
		}
		if details["op"] != "definitely-not-an-op" {
			t.Fatalf("details.op = %v, want definitely-not-an-op", details["op"])
		}
	})

	t.Run("details is omitted (not null) when absent", func(t *testing.T) {
		resp, decoded := h.do(t, opRequest{op: "release-task", body: map[string]any{}, headers: h.ownerHeaders()})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		envelope := requireEnvelope(t, decoded)
		assertEnvelopeKeys(t, envelope, []string{"code", "message", "retryable"})
		if code := envelope["code"]; code != "invalid" {
			t.Fatalf("code = %v, want invalid", code)
		}
	})
}

func requireEnvelope(t *testing.T, decoded map[string]any) map[string]any {
	t.Helper()
	envelope, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no structured error envelope: %v", decoded)
	}
	return envelope
}

func assertEnvelopeKeys(t *testing.T, envelope map[string]any, want []string) {
	t.Helper()
	got := make([]string, 0, len(envelope))
	for key := range envelope {
		got = append(got, key)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("error envelope keys = %v, want %v (frozen v1 contract)", got, want)
	}
}
