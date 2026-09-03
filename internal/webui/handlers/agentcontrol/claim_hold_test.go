package agentcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type holdCall struct {
	op   string
	args json.RawMessage
}

func newMockHoldFn(result *AgentControlResult, err error) (ClaimHoldFn, *[]holdCall) {
	var calls []holdCall
	fn := func(op string, args json.RawMessage) (*AgentControlResult, error) {
		calls = append(calls, holdCall{op, args})
		return result, err
	}
	return fn, &calls
}

func holdStatusData(t *testing.T, actor string) json.RawMessage {
	t.Helper()
	return json.RawMessage(fmt.Sprintf(
		`{"hold":{"held":true,"actor":%q,"reason":"redeploy","since":"2026-08-19T01:00:00Z"},`+
			`"running":[{"agent":"falcon","task_id":"PUPPET-1","pid":42}],"gated":6}`, actor))
}

func decodeHoldStatus(t *testing.T, rec *httptest.ResponseRecorder) ClaimHoldStatusView {
	t.Helper()
	var got ClaimHoldStatusView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body %q: %v", rec.Body.String(), err)
	}
	return got
}

// --- GET ---

func TestHandleClaimHoldGet_ReturnsStatus(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{Success: true, Data: holdStatusData(t, "deployer")}, nil)

	rec := serveRequest(handleClaimHoldGet(fn), "GET", "/api/workspaces/ws1/claims/hold", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0].op != "claims_hold_get" {
		t.Fatalf("calls = %+v, want one claims_hold_get", *calls)
	}
	got := decodeHoldStatus(t, rec)
	if got.Hold == nil || got.Hold.Actor != "deployer" {
		t.Errorf("hold = %+v, want actor deployer", got.Hold)
	}
	if got.Gated != 6 {
		t.Errorf("gated = %d, want 6", got.Gated)
	}
	if len(got.Running) != 1 || got.Running[0].Agent != "falcon" {
		t.Errorf("running = %+v, want one falcon entry", got.Running)
	}
}

func TestHandleClaimHoldGet_FreeReturnsNullHold(t *testing.T) {
	fn, _ := newMockHoldFn(&AgentControlResult{
		Success: true, Data: json.RawMessage(`{"hold":null,"running":[],"gated":0}`)}, nil)

	rec := serveRequest(handleClaimHoldGet(fn), "GET", "/api/workspaces/ws1/claims/hold", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"hold":null`) {
		t.Errorf("body = %s, want hold null", rec.Body.String())
	}
}

// --- POST ---

func TestHandleClaimHoldSet_SendsArgsAndReturns200(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{Success: true, Data: holdStatusData(t, "alice")}, nil)

	rec := serveRequest(handleClaimHoldSet(fn), "POST", "/api/workspaces/ws1/claims/hold",
		`{"reason":"redeploy","ttl_seconds":3600,"actor":"alice"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0].op != "claims_hold_set" {
		t.Fatalf("calls = %+v, want one claims_hold_set", *calls)
	}
	var args claimHoldSetArgs
	if err := json.Unmarshal((*calls)[0].args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if !args.Held || args.Actor != "alice" || args.Reason != "redeploy" || args.TTLSeconds != 3600 {
		t.Errorf("args = %+v, want held alice/redeploy/3600", args)
	}
}

func TestHandleClaimHoldSet_MissingReasonReturns400(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{Success: true}, nil)

	rec := serveRequest(handleClaimHoldSet(fn), "POST", "/api/workspaces/ws1/claims/hold", `{"actor":"alice"}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %+v, want none — the daemon must not be asked", *calls)
	}
}

func TestHandleClaimHoldSet_ForeignHolderReturns409(t *testing.T) {
	fn, _ := newMockHoldFn(&AgentControlResult{
		Success: false,
		Error:   "claims held by deployer since 2026-08-19T01:00:00Z; use --force to replace"}, nil)

	rec := serveRequest(handleClaimHoldSet(fn), "POST", "/api/workspaces/ws1/claims/hold", `{"reason":"mine"}`)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
}

// --- DELETE ---

func TestHandleClaimHoldRelease_Returns200AndReleasesHeldFalse(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{
		Success: true, Data: json.RawMessage(`{"hold":null,"running":[],"gated":0}`)}, nil)

	rec := serveRequest(handleClaimHoldRelease(fn), "DELETE", "/api/workspaces/ws1/claims/hold", `{"actor":"alice"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var args claimHoldSetArgs
	if err := json.Unmarshal((*calls)[0].args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args.Held || args.Actor != "alice" || args.Force {
		t.Errorf("args = %+v, want release by alice without force", args)
	}
}

func TestHandleClaimHoldRelease_ForeignHolderWithoutForceReturns409(t *testing.T) {
	fn, _ := newMockHoldFn(&AgentControlResult{
		Success: false,
		Error:   "claims held by deployer since 2026-08-19T01:00:00Z; use --force to release"}, nil)

	rec := serveRequest(handleClaimHoldRelease(fn), "DELETE", "/api/workspaces/ws1/claims/hold", "")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "claims held by deployer") {
		t.Errorf("body = %s, want the daemon's own refusal text", rec.Body.String())
	}
}

func TestHandleClaimHoldRelease_ForcePropagates(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{
		Success: true, Data: json.RawMessage(`{"hold":null,"running":[],"gated":0}`)}, nil)

	rec := serveRequest(handleClaimHoldRelease(fn), "DELETE", "/api/workspaces/ws1/claims/hold",
		`{"actor":"alice","force":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var args claimHoldSetArgs
	if err := json.Unmarshal((*calls)[0].args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if !args.Force {
		t.Errorf("args = %+v, want force true", args)
	}
}

// --- daemon down ---

func TestClaimHoldRoutes_DaemonDownReturns503(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
	}{
		{"get", "GET", ""},
		{"set", "POST", `{"reason":"redeploy"}`},
		{"release", "DELETE", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, _ := newMockHoldFn(nil, fmt.Errorf("daemon is not running (no control socket at /tmp/x.sock)"))
			var h http.HandlerFunc
			switch tt.method {
			case "GET":
				h = handleClaimHoldGet(fn)
			case "POST":
				h = handleClaimHoldSet(fn)
			default:
				h = handleClaimHoldRelease(fn)
			}
			rec := serveRequest(h, tt.method, "/api/workspaces/ws1/claims/hold", tt.body)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// --- actor resolution ---

func TestResolveActor_PrecedenceBodyThenHeader(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/claims/hold", nil)
	req.Header.Set("X-Actor", "from-header")

	if got := resolveActor(req, "from-body"); got != "from-body" {
		t.Errorf("explicit actor = %q, want from-body", got)
	}
	if got := resolveActor(req, "  "); got != "from-header" {
		t.Errorf("header actor = %q, want from-header", got)
	}
}

func TestResolveActor_EnvBeatsOSUser(t *testing.T) {
	t.Setenv("LOOM_ACTOR", "union-autodeploy")
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/claims/hold", nil)

	if got := resolveActor(req, ""); got != "union-autodeploy" {
		t.Errorf("env actor = %q, want union-autodeploy", got)
	}
}

func TestResolveActor_NeverEmpty(t *testing.T) {
	t.Setenv("LOOM_ACTOR", "")
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/claims/hold", nil)

	if got := resolveActor(req, ""); got == "" {
		t.Error("actor is empty; the daemon rejects an empty actor outright")
	}
}

// The browser client cannot attach a body to a DELETE, so the query form must
// carry force just as faithfully as the body form.
func TestHandleClaimHoldRelease_QueryParamsCarryActorAndForce(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{
		Success: true, Data: json.RawMessage(`{"hold":null,"running":[],"gated":0}`)}, nil)

	rec := serveRequest(handleClaimHoldRelease(fn), "DELETE",
		"/api/workspaces/ws1/claims/hold?actor=alice&force=true", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var args claimHoldSetArgs
	if err := json.Unmarshal((*calls)[0].args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args.Actor != "alice" || !args.Force {
		t.Errorf("args = %+v, want alice with force", args)
	}
}

// --- repo scope ---

func TestHandleClaimHoldSet_ForwardsRepoScope(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{Success: true, Data: holdStatusData(t, "alice")}, nil)

	rec := serveRequest(handleClaimHoldSet(fn), "POST", "/api/workspaces/ws1/claims/hold",
		`{"reason":"redeploy","actor":"alice","repos":["fleet-db"]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var args claimHoldSetArgs
	if err := json.Unmarshal((*calls)[0].args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if len(args.Repos) != 1 || args.Repos[0] != "fleet-db" {
		t.Fatalf("args.Repos = %v, want [fleet-db]", args.Repos)
	}
}

// Omitting repos must stay a workspace-wide hold, and must not put an empty
// list on the wire where the daemon would have to tell it from "unset".
func TestHandleClaimHoldSet_NoReposIsWorkspaceWide(t *testing.T) {
	fn, calls := newMockHoldFn(&AgentControlResult{Success: true, Data: holdStatusData(t, "alice")}, nil)

	serveRequest(handleClaimHoldSet(fn), "POST", "/api/workspaces/ws1/claims/hold",
		`{"reason":"redeploy","actor":"alice"}`)

	if strings.Contains(string((*calls)[0].args), "repos") {
		t.Fatalf("args = %s, want no repos field at all", (*calls)[0].args)
	}
}

func TestHandleClaimHoldGet_EchoesTheRepoScope(t *testing.T) {
	fn, _ := newMockHoldFn(&AgentControlResult{Success: true, Data: json.RawMessage(
		`{"hold":{"held":true,"actor":"deployer","reason":"redeploy",` +
			`"since":"2026-08-19T01:00:00Z","repos":["fleet-db"]},"running":[],"gated":0}`)}, nil)

	rec := serveRequest(handleClaimHoldGet(fn), "GET", "/api/workspaces/ws1/claims/hold", "")

	got := decodeHoldStatus(t, rec)
	if got.Hold == nil || len(got.Hold.Repos) != 1 || got.Hold.Repos[0] != "fleet-db" {
		t.Fatalf("hold = %+v, want the scope echoed through", got.Hold)
	}
}
