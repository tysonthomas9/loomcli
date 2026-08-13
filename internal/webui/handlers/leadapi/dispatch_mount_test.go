package leadapi

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type dispatchRouteSpec struct {
	name     string
	method   string
	path     string
	mutating bool
	want     int
}

func dispatchRouteSpecs(ws string) []dispatchRouteSpec {
	base := "/api/workspaces/" + ws + "/lead/dispatch"
	return []dispatchRouteSpec{
		{"epic run", http.MethodPost, base + "/epic-run", true, http.StatusAccepted},
		{"run status", http.MethodGet, base + "/runs/own-run", false, http.StatusOK},
	}
}

func TestDispatchRoutePatternsArePinned(t *testing.T) {
	if got := len(dispatchRoutePatterns); got != 2 {
		t.Fatalf("dispatch route patterns = %d, want 2", got)
	}
	module := &Module{}
	selected := make(map[uintptr]string, len(dispatchRoutePatterns))
	labels := make(map[string]struct{}, len(dispatchRoutePatterns))
	for _, route := range dispatchRoutePatterns {
		handler := route.pick(module)
		if handler == nil {
			t.Fatalf("%s %s selected a nil handler", route.method, route.suffix)
		}
		identity := reflect.ValueOf(handler).Pointer()
		if prior := selected[identity]; prior != "" {
			t.Fatalf("handler selected by both %s and %s %s", prior, route.method, route.suffix)
		}
		selected[identity] = route.method + " " + route.suffix
		if _, exists := labels[route.label]; exists {
			t.Fatalf("duplicate route label %q", route.label)
		}
		labels[route.label] = struct{}{}
	}
}

func TestDispatchMount_AuthMatrix(t *testing.T) {
	states := []domain.PlacementState{
		domain.PlacementStateReleasing, domain.PlacementStateReleased,
		domain.PlacementStateLost, "", "paused",
	}
	for _, route := range dispatchRouteSpecs("WS") {
		t.Run(route.name, func(t *testing.T) {
			testDispatchAuthFailure(t, route, "no bearer", http.StatusUnauthorized, "unauthenticated", nil)
			testDispatchAuthFailure(t, route, "wrong signing key", http.StatusUnauthorized, "unauthenticated",
				func(t *testing.T, h *dataMountHarness) string {
					claims := leadtoken.OccupantClaims{WorkspaceKey: "WS", PlacementID: "p1", Generation: 7,
						Caps: []string{leadtoken.CapLeadDispatch}}
					token, err := leadtoken.MintOccupantToken(claims, bytes.Repeat([]byte{0x77}, 32), time.Hour)
					if err != nil {
						t.Fatal(err)
					}
					return token
				})
			testDispatchAuthFailure(t, route, "expired", http.StatusUnauthorized, "token_expired",
				expiredDispatchToken)
			testDispatchAuthFailure(t, route, "wrong workspace claim", http.StatusUnauthorized, "identity_mismatch",
				func(t *testing.T, h *dataMountHarness) string {
					return h.token(t, func(c *leadtoken.OccupantClaims) {
						c.WorkspaceKey = "OTHER"
						c.Caps = []string{leadtoken.CapLeadDispatch}
					})
				})
			testDispatchAuthFailure(t, route, "data-only cap", http.StatusForbidden, "cap_denied",
				func(t *testing.T, h *dataMountHarness) string { return h.token(t, nil) })
			t.Run("placement absent", func(t *testing.T) {
				be := &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{"epic-1": dispatchEpic("epic-1", "repo-1")}}
				h := newDataMountHarness(t, dataMountHarnessOptions{
					issueBackend: func(context.Context) backend.IssueBackend { return be },
				})
				result := dispatchRouteRequest(t, h, route, dispatchToken(t, h, "p1"))
				requireDispatchCode(t, result, http.StatusUnauthorized, "placement_absent")
			})
			for _, state := range states {
				t.Run("placement state "+string(state), func(t *testing.T) {
					h := newDispatchMountHarness(t)
					setDataPlacementState(t, h, state)
					requireDispatchCode(t, dispatchRouteRequest(t, h, route, dispatchToken(t, h, "p1")),
						http.StatusUnauthorized, "placement_released")
				})
			}
			testDispatchAuthFailure(t, route, "stale generation", http.StatusUnauthorized, "generation_fenced",
				func(t *testing.T, h *dataMountHarness) string {
					return h.token(t, func(c *leadtoken.OccupantClaims) {
						c.Generation--
						c.Caps = []string{leadtoken.CapLeadDispatch}
					})
				})
			t.Run("valid", func(t *testing.T) {
				h := newDispatchMountHarness(t)
				requireDispatchCode(t, dispatchRouteRequest(t, h, route, dispatchToken(t, h, "p1")), route.want, "")
			})
		})
	}
}

func TestDispatchMount_CanonicalWorkspaceMismatchFencesBeforeHandler(t *testing.T) {
	be := &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{"epic-1": dispatchEpic("epic-1", "repo-1")}}
	h := newDispatchHarness(t, dispatchHarnessOptions{workspace: "alias", createSession: true, seedRepo: true, issueBackend: be})
	route := dispatchRouteSpecs("alias")[0]
	rec := h.request(t, dataRouteSpec{method: route.method, path: route.path}, dispatchToken(t, h, "p1"),
		"canonical", strings.NewReader(`{"epicId":"epic-1"}`))
	requireDispatchCode(t, &dispatchHTTPResult{status: rec.Code, body: rec.Body.String()},
		http.StatusUnauthorized, "identity_mismatch")
	if len(be.actors) != 0 {
		t.Fatalf("handler called with actors %v", be.actors)
	}
}

func TestDispatchMount_OnlyAllowlistedRoutes(t *testing.T) {
	be := &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{"epic-1": dispatchEpic("epic-1", "repo-1")}}
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true, issueBackend: be})
	token := dispatchToken(t, h, "p1")
	for _, tt := range []struct{ method, path string }{
		{http.MethodPost, "/api/workspaces/WS/lead/dispatch/workflows/x/versions"},
		{http.MethodPost, "/api/workspaces/WS/lead/dispatch/issues"},
		{http.MethodGet, "/api/workspaces/WS/lead/dispatch/agents"},
		{http.MethodGet, "/api/workspaces/WS/lead/dispatch/epic-run"},
		{http.MethodPost, "/api/workspaces/WS/lead/dispatch/runs/run-1"},
		{http.MethodGet, "/api/workspaces/WS/lead/dispatch/"},
		{http.MethodGet, "/api/workspaces/WS/lead/dispatch/epic-run/extra"},
	} {
		result := dispatchRequest(t, h, tt.method, tt.path, token, `{}`)
		requireDispatchCode(t, result, http.StatusNotFound, "not_found")
	}
	if len(be.actors) != 0 {
		t.Fatalf("epic-run handler calls = %d, want 0", len(be.actors))
	}
}

func TestDispatchMount_StampsOccupantActorAndNeverUserIdentity(t *testing.T) {
	be := &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{"epic-1": dispatchEpic("epic-1", "repo-1")}}
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true, issueBackend: be})
	h.module.issueBackend = func(ctx context.Context) backend.IssueBackend {
		if _, ok := middleware.UserIdentityFromContext(ctx); ok {
			t.Error("occupant route carried a UserIdentity")
		}
		actor, ok := middleware.ActorFromContext(ctx)
		if !ok || actor.Validate() != nil || actor.Kind() != middleware.ActorKindOccupant ||
			actor.BackendActor() != leadtoken.OccupantActor(h.placement) {
			t.Errorf("occupant actor = (%+v, %t), want valid %q", actor, ok, leadtoken.OccupantActor(h.placement))
		}
		return be
	}
	requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"epic-1"}`), http.StatusAccepted, "")
	if !reflect.DeepEqual(be.actors, []string{"lead-occupant:p1"}) {
		t.Fatalf("actors = %v", be.actors)
	}
}

func TestDispatchMount_EachRouteUsesItsPinnedRateLimitBucket(t *testing.T) {
	h := newDispatchMountHarness(t)
	h.module.limiter.mutateBurst = 1
	h.module.limiter.mutate = 0
	limiterKey := h.workspace + "\x00" + h.placement
	if !h.module.limiter.allow(limiterKey, true) {
		t.Fatal("failed to drain mutate bucket")
	}
	productionMutating, productionReads := 0, 0
	for _, route := range dispatchRoutePatterns {
		if route.mutating {
			productionMutating++
		} else {
			productionReads++
		}
	}
	if productionMutating != 1 || productionReads != 1 {
		t.Fatalf("production route classes = mutate %d read %d, want 1/1", productionMutating, productionReads)
	}

	mutating, reads := 0, 0
	for _, route := range dispatchRouteSpecs("WS") {
		result := dispatchRouteRequest(t, h, route, dispatchToken(t, h, "p1"))
		if route.mutating {
			mutating++
			requireDispatchCode(t, result, http.StatusTooManyRequests, "rate_limited")
		} else {
			reads++
			requireDispatchCode(t, result, http.StatusOK, "")
		}
	}
	if mutating != 1 || reads != 1 {
		t.Fatalf("route classes = mutate %d read %d, want 1/1", mutating, reads)
	}
}

func TestDispatchMount_LimiterKeyIncludesWorkspace(t *testing.T) {
	h := newDispatchMountHarness(t)
	h.module.limiter.readBurst = 1
	h.module.limiter.read = 0
	createNodeRecord(t, context.Background(), h.store, "WS-TWO", "p1", "lead", 7, domain.PlacementStateActive)
	seedEpicRunner(t, h.store, "WS-TWO")
	seedDriverRunStatus(t, h.store, "WS-TWO", "own-run", "epic-status", domain.DriverRunQueued,
		domain.DriverRunSourceLeadOccupant, leadtoken.OccupantActor("p1"))

	first := dispatchRouteSpecs("WS")[1]
	tokenOne := dispatchToken(t, h, "p1")
	requireDispatchCode(t, dispatchRouteRequest(t, h, first, tokenOne), http.StatusOK, "")
	requireDispatchCode(t, dispatchRouteRequest(t, h, first, tokenOne), http.StatusTooManyRequests, "rate_limited")

	second := dispatchRouteSpecs("WS-TWO")[1]
	tokenTwo := h.token(t, func(c *leadtoken.OccupantClaims) {
		c.WorkspaceKey = "WS-TWO"
		c.Caps = []string{leadtoken.CapLeadDispatch}
	})
	rec := h.request(t, dataRouteSpec{method: second.method, path: second.path}, tokenTwo, "WS-TWO", nil)
	requireDispatchCode(t, &dispatchHTTPResult{status: rec.Code, body: rec.Body.String()}, http.StatusOK, "")
}

func TestDispatchMount_DisabledInOpenAuthMode(t *testing.T) {
	be := &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{"epic-1": dispatchEpic("epic-1", "repo-1")}}
	h := newDataMountHarness(t, dataMountHarnessOptions{
		createNode: true, createSession: true, openAuthMode: true,
		issueBackend: func(context.Context) backend.IssueBackend { return be },
	})
	token := dispatchToken(t, h, "p1")
	result := dispatchRequest(t, h, http.MethodPost, "/api/workspaces/WS/lead/dispatch/epic-run", token, `{"epicId":"epic-1"}`)
	if result.status != http.StatusNotFound {
		t.Fatalf("dispatch status = %d, want 404", result.status)
	}
	heartbeatToken := h.token(t, func(c *leadtoken.OccupantClaims) { c.Caps = []string{leadtoken.CapLeadSession} })
	rec := h.request(t, dataRouteSpec{method: http.MethodPost, path: "/api/workspaces/WS/lead/heartbeat"},
		heartbeatToken, "WS", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	override := newDataMountHarness(t, dataMountHarnessOptions{
		createNode: true, createSession: true, openAuthMode: true, allowOpenAuthMode: true,
		issueBackend: func(context.Context) backend.IssueBackend { return be },
	})
	seedEpicRunner(t, override.store, "WS")
	seedDispatchRepo(t, override.store, "WS", "repo-1", "source-1", "https://github.com/o/r", "main")
	requireDispatchCode(t, dispatchEpicRun(t, override, `{"epicId":"epic-1"}`), http.StatusAccepted, "")
}

func TestDispatchMount_AbsentWithoutIssueBackend(t *testing.T) {
	h := newDispatchMountHarness(t)
	h.module.issueBackend = nil
	h.mux = http.NewServeMux()
	h.module.Register(h.mux)
	result := dispatchRequest(t, h, http.MethodPost, "/api/workspaces/WS/lead/dispatch/epic-run",
		dispatchToken(t, h, "p1"), `{"epicId":"epic-1"}`)
	if result.status != http.StatusNotFound {
		t.Fatalf("dispatch status = %d, want 404", result.status)
	}
	dataResult := h.request(t, dataRouteSpecs("WS")[0], h.token(t, nil), "WS", nil)
	if dataResult.Code != http.StatusNoContent {
		t.Fatalf("data status = %d, want 204", dataResult.Code)
	}
}

func TestDispatchMount_RegistersAlongsideDataAndLeadOp(t *testing.T) {
	h := newDispatchMountHarness(t)
	allCaps := h.token(t, func(c *leadtoken.OccupantClaims) {
		c.Caps = []string{leadtoken.CapLeadSession, leadtoken.CapLeadData, leadtoken.CapLeadDispatch}
	})
	rec := h.request(t, dataRouteSpec{method: http.MethodPost, path: "/api/workspaces/WS/lead/heartbeat"}, allCaps, "WS", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d", rec.Code)
	}
	rec = h.request(t, dataRouteSpecs("WS")[1], allCaps, "WS", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("data status = %d", rec.Code)
	}
	result := dispatchRequest(t, h, http.MethodPost, "/api/workspaces/WS/lead/dispatch/epic-run",
		allCaps, `{"epicId":"epic-1"}`)
	requireDispatchCode(t, result, http.StatusAccepted, "")
}

func TestDispatchMount_HEADRunStatusUsesReadBucket(t *testing.T) {
	h := newDispatchMountHarness(t)
	h.module.limiter.mutateBurst = 1
	h.module.limiter.mutate = 0
	if !h.module.limiter.allow(h.workspace+"\x00"+h.placement, true) {
		t.Fatal("failed to drain mutate bucket")
	}
	result := dispatchRequest(t, h, http.MethodHead, "/api/workspaces/WS/lead/dispatch/runs/own-run",
		dispatchToken(t, h, "p1"), "")
	requireDispatchCode(t, result, http.StatusOK, "")
	if result.header.Get("Content-Type") != "application/json" || strings.Contains(result.body, "payload") {
		t.Fatalf("HEAD projection headers/body = %v / %s", result.header, result.body)
	}
}

func newDispatchMountHarness(t *testing.T) *dataMountHarness {
	t.Helper()
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	seedDriverRunStatus(t, h.store, "WS", "own-run", "epic-status", domain.DriverRunQueued,
		domain.DriverRunSourceLeadOccupant, leadtoken.OccupantActor("p1"))
	return h
}

func dispatchRouteRequest(t *testing.T, h *dataMountHarness, route dispatchRouteSpec, token string) *dispatchHTTPResult {
	t.Helper()
	body := ""
	if route.mutating {
		body = `{"epicId":"epic-1"}`
	}
	return dispatchRequest(t, h, route.method, route.path, token, body)
}

func testDispatchAuthFailure(t *testing.T, route dispatchRouteSpec, name string,
	wantStatus int, wantCode string, tokenFn func(*testing.T, *dataMountHarness) string,
) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		h := newDispatchMountHarness(t)
		token := ""
		if tokenFn != nil {
			token = tokenFn(t, h)
		}
		requireDispatchCode(t, dispatchRouteRequest(t, h, route, token), wantStatus, wantCode)
	})
}

func expiredDispatchToken(t *testing.T, h *dataMountHarness) string {
	t.Helper()
	now := time.Now()
	claims := leadtoken.OccupantClaims{
		WorkspaceKey: h.workspace, PlacementID: h.placement, Generation: h.generation,
		Caps: []string{leadtoken.CapLeadDispatch},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: leadtoken.OccupantActor(h.placement), IssuedAt: jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.key)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
