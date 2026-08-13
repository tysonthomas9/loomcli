package leadapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type dataRouteSpec struct {
	name     string
	method   string
	path     string
	mutating bool
}

func dataRouteSpecs(ws string) []dataRouteSpec {
	base := "/api/workspaces/" + ws + "/lead/data"
	return []dataRouteSpec{
		{"list issues", http.MethodGet, base + "/issues", false},
		{"create issue", http.MethodPost, base + "/issues", true},
		{"get issue", http.MethodGet, base + "/issues/issue-1", false},
		{"patch issue", http.MethodPatch, base + "/issues/issue-1", true},
		{"close issue", http.MethodPost, base + "/issues/issue-1/close", true},
		{"claim issue", http.MethodPost, base + "/issues/issue-1/claim", true},
		{"add comment", http.MethodPost, base + "/issues/issue-1/comments", true},
		{"add dependency", http.MethodPost, base + "/issues/issue-1/dependencies", true},
		{"remove dependency", http.MethodDelete, base + "/issues/issue-1/dependencies/issue-2", true},
		{"ready", http.MethodGet, base + "/ready", false},
		{"blocked", http.MethodGet, base + "/blocked", false},
		{"stats", http.MethodGet, base + "/stats", false},
	}
}

func TestDataRoutePatternsCoverEveryDataRouteFieldExactlyOnce(t *testing.T) {
	routeType := reflect.TypeOf(DataRoutes{})
	if got, want := len(dataRoutePatterns), routeType.NumField(); got != want {
		t.Fatalf("route patterns = %d, DataRoutes fields = %d", got, want)
	}
	if got := len(dataRoutePatterns); got != 12 {
		t.Fatalf("route patterns = %d, want 12", got)
	}

	routes := distinguishableDataRoutes()
	routeValue := reflect.ValueOf(routes)
	fieldByHandler := make(map[uintptr]string, routeType.NumField())
	for i := 0; i < routeType.NumField(); i++ {
		identity := routeValue.Field(i).Pointer()
		if prior := fieldByHandler[identity]; prior != "" {
			t.Fatalf("DataRoutes fields %s and %s have indistinguishable handlers", prior, routeType.Field(i).Name)
		}
		fieldByHandler[identity] = routeType.Field(i).Name
	}

	selected := make(map[uintptr]string, len(dataRoutePatterns))
	for _, pattern := range dataRoutePatterns {
		handler := pattern.pick(&routes)
		identity := reflect.ValueOf(handler).Pointer()
		field, ok := fieldByHandler[identity]
		if !ok {
			t.Fatalf("%s %s selected a handler outside DataRoutes", pattern.method, pattern.suffix)
		}
		if prior := selected[identity]; prior != "" {
			t.Fatalf("%s selected by both %s and %s %s", field, prior, pattern.method, pattern.suffix)
		}
		selected[identity] = pattern.method + " " + pattern.suffix
	}
	if got := len(selected); got != 12 {
		t.Fatalf("distinct selected DataRoutes fields = %d, want 12", got)
	}
}

func distinguishableDataRoutes() DataRoutes {
	return DataRoutes{
		ListIssues:       func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "ListIssues") },
		CreateIssue:      func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "CreateIssue") },
		GetIssue:         func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "GetIssue") },
		PatchIssue:       func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "PatchIssue") },
		CloseIssue:       func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "CloseIssue") },
		ClaimIssue:       func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "ClaimIssue") },
		AddComment:       func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "AddComment") },
		AddDependency:    func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "AddDependency") },
		RemoveDependency: func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "RemoveDependency") },
		Ready:            func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "Ready") },
		Blocked:          func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "Blocked") },
		Stats:            func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Route", "Stats") },
	}
}

type dataMountHarness struct {
	store      store.Store
	key        []byte
	module     *Module
	mux        *http.ServeMux
	workspace  string
	placement  string
	generation int64
	called     atomic.Int64
}

type dataMountHarnessOptions struct {
	workspace         string
	createNode        bool
	createSession     bool
	data              *DataRoutes
	openAuthMode      bool
	allowOpenAuthMode bool
	configure         func(*dataMountHarness)
}

func newDataMountHarness(t *testing.T, opts dataMountHarnessOptions) *dataMountHarness {
	t.Helper()
	ws := opts.workspace
	if ws == "" {
		ws = "WS"
	}
	h := &dataMountHarness{
		store:      memstore.New(),
		key:        bytes.Repeat([]byte{0x55}, 32),
		workspace:  ws,
		placement:  "p1",
		generation: 7,
	}
	if opts.createNode {
		createNodeRecord(t, context.Background(), h.store, ws, h.placement, "lead", h.generation, domain.PlacementStateActive)
	}
	if opts.createSession {
		createSessionRecord(t, context.Background(), h.store, ws, "lead-session", "lead", h.placement, domain.AgentSessionRunning)
	}
	data := opts.data
	if data == nil {
		data = h.spyDataRoutes(t)
	}
	h.module = NewModule(Config{
		Store:             h.store,
		TokenKey:          h.key,
		Data:              data,
		OpenAuthMode:      opts.openAuthMode,
		AllowOpenAuthMode: opts.allowOpenAuthMode,
	})
	if opts.configure != nil {
		opts.configure(h)
	}
	h.mux = http.NewServeMux()
	h.module.Register(h.mux)
	return h
}

func (h *dataMountHarness) spyDataRoutes(t *testing.T) *DataRoutes {
	t.Helper()
	handler := func(w http.ResponseWriter, r *http.Request) {
		if _, ok := middleware.UserIdentityFromContext(r.Context()); ok {
			t.Error("occupant route carried a UserIdentity")
		}
		actor, ok := middleware.ActorFromContext(r.Context())
		if !ok || actor.Validate() != nil || actor.Kind() != middleware.ActorKindOccupant || actor.BackendActor() != leadtoken.OccupantActor(h.placement) {
			t.Errorf("occupant actor = (%+v, %t), want valid %q", actor, ok, leadtoken.OccupantActor(h.placement))
		}
		h.called.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}
	return &DataRoutes{
		ListIssues: handler, CreateIssue: handler, GetIssue: handler, PatchIssue: handler,
		CloseIssue: handler, ClaimIssue: handler, AddComment: handler,
		AddDependency: handler, RemoveDependency: handler,
		Ready: handler, Blocked: handler, Stats: handler,
	}
}

func (h *dataMountHarness) token(t *testing.T, mutate func(*leadtoken.OccupantClaims)) string {
	t.Helper()
	claims := leadtoken.OccupantClaims{
		WorkspaceKey: h.workspace,
		PlacementID:  h.placement,
		Generation:   h.generation,
		Caps:         []string{leadtoken.CapLeadData},
	}
	if mutate != nil {
		mutate(&claims)
	}
	token, err := leadtoken.MintOccupantToken(claims, h.key, time.Hour)
	if err != nil {
		t.Fatalf("MintOccupantToken: %v", err)
	}
	return token
}

func (h *dataMountHarness) expiredToken(t *testing.T) string {
	t.Helper()
	now := time.Now()
	claims := leadtoken.OccupantClaims{
		WorkspaceKey: h.workspace,
		PlacementID:  h.placement,
		Generation:   h.generation,
		Caps:         []string{leadtoken.CapLeadData},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   leadtoken.OccupantActor(h.placement),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.key)
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	return token
}

func (h *dataMountHarness) request(t *testing.T, spec dataRouteSpec, token, canonicalWS string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	if body == nil {
		body = strings.NewReader("{}")
	}
	req := httptest.NewRequest(spec.method, spec.path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req = req.WithContext(middleware.WithWorkspace(req.Context(), canonicalWS))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func TestDataMount_AuthMatrix(t *testing.T) {
	states := []domain.PlacementState{
		domain.PlacementStateReleasing,
		domain.PlacementStateReleased,
		domain.PlacementStateLost,
		"",
		"paused",
	}
	for _, route := range dataRouteSpecs("WS") {
		t.Run(route.name, func(t *testing.T) {
			testDataRouteAuthFailure(t, route, "no bearer", http.StatusUnauthorized, "unauthenticated", nil)
			testDataRouteAuthFailure(t, route, "wrong signing key", http.StatusUnauthorized, "unauthenticated", func(t *testing.T, h *dataMountHarness) string {
				claims := leadtoken.OccupantClaims{WorkspaceKey: h.workspace, PlacementID: h.placement, Generation: h.generation, Caps: []string{leadtoken.CapLeadData}}
				token, err := leadtoken.MintOccupantToken(claims, bytes.Repeat([]byte{0x77}, 32), time.Hour)
				if err != nil {
					t.Fatalf("wrong-key token: %v", err)
				}
				return token
			})
			testDataRouteAuthFailure(t, route, "expired", http.StatusUnauthorized, "token_expired", func(t *testing.T, h *dataMountHarness) string { return h.expiredToken(t) })
			testDataRouteAuthFailure(t, route, "wrong workspace claim", http.StatusUnauthorized, "identity_mismatch", func(t *testing.T, h *dataMountHarness) string {
				return h.token(t, func(c *leadtoken.OccupantClaims) { c.WorkspaceKey = "OTHER" })
			})
			testDataRouteAuthFailure(t, route, "missing cap", http.StatusForbidden, "cap_denied", func(t *testing.T, h *dataMountHarness) string {
				return h.token(t, func(c *leadtoken.OccupantClaims) { c.Caps = nil })
			})
			t.Run("placement absent", func(t *testing.T) {
				h := newDataMountHarness(t, dataMountHarnessOptions{})
				assertDataRouteResult(t, h, h.request(t, route, h.token(t, nil), "WS", nil), http.StatusUnauthorized, "placement_absent", false)
			})
			for _, state := range states {
				t.Run("placement state "+string(state), func(t *testing.T) {
					h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true})
					setDataPlacementState(t, h, state)
					assertDataRouteResult(t, h, h.request(t, route, h.token(t, nil), "WS", nil), http.StatusUnauthorized, "placement_released", false)
				})
			}
			testDataRouteAuthFailure(t, route, "stale generation", http.StatusUnauthorized, "generation_fenced", func(t *testing.T, h *dataMountHarness) string {
				return h.token(t, func(c *leadtoken.OccupantClaims) { c.Generation-- })
			})
			t.Run("valid", func(t *testing.T) {
				h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true})
				assertDataRouteResult(t, h, h.request(t, route, h.token(t, nil), "WS", nil), http.StatusNoContent, "", true)
			})
		})
	}
}

func testDataRouteAuthFailure(t *testing.T, route dataRouteSpec, name string, wantStatus int, wantCode string, tokenFn func(*testing.T, *dataMountHarness) string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true})
		token := ""
		if tokenFn != nil {
			token = tokenFn(t, h)
		}
		assertDataRouteResult(t, h, h.request(t, route, token, "WS", nil), wantStatus, wantCode, false)
	})
}

func assertDataRouteResult(t *testing.T, h *dataMountHarness, rec *httptest.ResponseRecorder, wantStatus int, wantCode string, wantCalled bool) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	if wantCode != "" {
		var envelope dataErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode error envelope: %v; body = %s", err, rec.Body.String())
		}
		if envelope.Success || envelope.Error == "" || envelope.Code != wantCode {
			t.Fatalf("error envelope = %+v, want success=false code=%q", envelope, wantCode)
		}
	}
	if got := h.called.Load(); (got > 0) != wantCalled {
		t.Fatalf("handler calls = %d, want called=%t", got, wantCalled)
	}
}

func setDataPlacementState(t *testing.T, h *dataMountHarness, state domain.PlacementState) {
	t.Helper()
	node, err := h.store.Nodes().Get(context.Background(), h.workspace, h.placement)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	placement := *node.Placement
	placement.State = state
	placementPtr := &placement
	if _, err := h.store.Nodes().Update(context.Background(), h.workspace, h.placement, store.NodeUpdate{Placement: &placementPtr}); err != nil {
		t.Fatalf("update placement state: %v", err)
	}
}

func TestDataMount_OnlyAllowlistedRoutes(t *testing.T) {
	// /issues/graph is syntactically an {id} and reaches GetIssue; the real
	// backend returns not-found for that nonexistent ID. Search has its own
	// explicit deny and never reaches GetIssue.
	var getIssueIDs []string
	h := newDataMountHarness(t, dataMountHarnessOptions{
		createNode: true,
		data: &DataRoutes{GetIssue: func(w http.ResponseWriter, r *http.Request) {
			getIssueIDs = append(getIssueIDs, r.PathValue("id"))
			denyDataRoute(w, r)
		}},
	})
	token := h.token(t, nil)
	tests := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/api/workspaces/WS/lead/data/issues/issue-1/move", http.StatusNotFound},
		{http.MethodDelete, "/api/workspaces/WS/lead/data/issues/issue-1", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/workspaces/WS/lead/data/issues/issue-1/reopen", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/WS/lead/data/issues/search", http.StatusNotFound},
		{http.MethodPatch, "/api/workspaces/WS/lead/data/issues/search", http.StatusNotFound},
		{http.MethodPost, "/api/workspaces/WS/lead/data/issues/search", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/WS/lead/data/issues/issue-1/events", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/WS/lead/data/issues/issue-1/comments", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/workspaces/WS/lead/data/issues/issue-1/dependencies", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/workspaces/WS/lead/data/issues/graph", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/WS/lead/data/readyz", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/WS/lead/data/repos", http.StatusNotFound},
		{http.MethodPost, "/api/workspaces/WS/lead/data/workflows/x/versions", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/WS/lead/data/agents", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/WS/lead/data/monitor/status", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := h.request(t, dataRouteSpec{method: tt.method, path: tt.path}, token, "WS", nil)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
	if !reflect.DeepEqual(getIssueIDs, []string{"graph"}) {
		t.Fatalf("GetIssue IDs = %v, want [graph]", getIssueIDs)
	}
}

func TestDataMount_AllRoutesStampOnlyOccupantActor(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true})
	for _, route := range dataRouteSpecs("WS") {
		t.Run(route.name, func(t *testing.T) {
			rec := h.request(t, route, h.token(t, nil), "WS", nil)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
	if got, want := h.called.Load(), int64(len(dataRouteSpecs("WS"))); got != want {
		t.Fatalf("handler calls = %d, want %d", got, want)
	}
}

func TestDataMount_NilHandlerAuthPrecedesUnavailable(t *testing.T) {
	routes := &DataRoutes{}
	h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true, data: routes})
	route := dataRouteSpecs("WS")[0]
	if rec := h.request(t, route, "", "WS", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bearer-less status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
	rec := h.request(t, route, h.token(t, nil), "WS", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("authorized nil-handler status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
}

func TestDataMount_CanonicalWorkspaceMismatchFencesBeforeHandler(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{workspace: "alias", createNode: true})
	route := dataRouteSpecs("alias")[0]
	rec := h.request(t, route, h.token(t, nil), "canonical", nil)
	assertDataRouteResult(t, h, rec, http.StatusUnauthorized, "identity_mismatch", false)
}

func TestDataMount_DisabledInOpenAuthMode(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true, createSession: true, openAuthMode: true})
	for _, route := range dataRouteSpecs("WS") {
		if rec := h.request(t, route, h.token(t, nil), "WS", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404; body = %s", route.name, rec.Code, rec.Body.String())
		}
	}
	leadToken := h.token(t, func(c *leadtoken.OccupantClaims) { c.Caps = []string{leadtoken.CapLeadSession} })
	rec := h.request(t, dataRouteSpec{method: http.MethodPost, path: "/api/workspaces/WS/lead/heartbeat"}, leadToken, "WS", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("lead heartbeat status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	override := newDataMountHarness(t, dataMountHarnessOptions{createNode: true, openAuthMode: true, allowOpenAuthMode: true})
	if rec := override.request(t, dataRouteSpecs("WS")[0], override.token(t, nil), "WS", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("override status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
}

func TestDataMount_AbsentWithoutDataRoutes(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true, data: nil})
	h.module.data = nil
	h.mux = http.NewServeMux()
	h.module.Register(h.mux)
	if rec := h.request(t, dataRouteSpecs("WS")[0], h.token(t, nil), "WS", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestDataMount_NoRouteCollisionWithLeadOp(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true, createSession: true})
	leadToken := h.token(t, func(c *leadtoken.OccupantClaims) {
		c.Caps = []string{leadtoken.CapLeadSession, leadtoken.CapLeadData}
	})
	if rec := h.request(t, dataRouteSpec{method: http.MethodPost, path: "/api/workspaces/WS/lead/heartbeat"}, leadToken, "WS", nil); rec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec := h.request(t, dataRouteSpec{method: http.MethodPost, path: "/api/workspaces/WS/lead/data/issues"}, leadToken, "WS", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("data create status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
}

func TestDataMount_RateLimitEnvelopeAndRetryAfter(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{
		createNode: true,
		configure: func(h *dataMountHarness) {
			h.module.limiter.readBurst = 1
			h.module.limiter.read = 0
		},
	})
	route := dataRouteSpecs("WS")[0]
	token := h.token(t, nil)
	if rec := h.request(t, route, token, "WS", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want 204", rec.Code)
	}
	rec := h.request(t, route, token, "WS", nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("throttled response = status %d Retry-After %q, want 429/1", rec.Code, rec.Header().Get("Retry-After"))
	}
	var envelope dataErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || envelope.Code != "rate_limited" || envelope.Success {
		t.Fatalf("rate envelope = %+v, err=%v", envelope, err)
	}
}

func TestDataMount_RateLimiterSeparatesWorkspacesSharingPlacementID(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{
		workspace:  "WS-ONE",
		createNode: true,
		configure: func(h *dataMountHarness) {
			h.module.limiter.readBurst = 1
			h.module.limiter.read = 0
		},
	})
	createNodeRecord(t, context.Background(), h.store, "WS-TWO", h.placement, "lead", h.generation, domain.PlacementStateActive)

	firstRoute := dataRouteSpecs("WS-ONE")[0]
	firstToken := h.token(t, nil)
	if rec := h.request(t, firstRoute, firstToken, "WS-ONE", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("first workspace initial status = %d, want 204", rec.Code)
	}
	if rec := h.request(t, firstRoute, firstToken, "WS-ONE", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("first workspace repeated status = %d, want 429", rec.Code)
	}

	secondRoute := dataRouteSpecs("WS-TWO")[0]
	secondToken := h.token(t, func(c *leadtoken.OccupantClaims) { c.WorkspaceKey = "WS-TWO" })
	if rec := h.request(t, secondRoute, secondToken, "WS-TWO", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("second workspace initial status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
}

func TestDataMount_EachRouteUsesItsPinnedRateLimitBucket(t *testing.T) {
	h := newDataMountHarness(t, dataMountHarnessOptions{
		createNode: true,
		configure: func(h *dataMountHarness) {
			h.module.limiter.mutateBurst = 1
			h.module.limiter.mutate = 0
		},
	})
	limiterKey := h.workspace + "\x00" + h.placement
	if !h.module.limiter.allow(limiterKey, true) {
		t.Fatal("failed to drain the mutate bucket")
	}

	token := h.token(t, nil)
	mutating429s := 0
	for _, route := range dataRouteSpecs("WS") {
		rec := h.request(t, route, token, "WS", nil)
		if rec.Code == http.StatusTooManyRequests {
			mutating429s++
		}
		if route.mutating {
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("%s status = %d, want 429; body = %s", route.name, rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s status = %d, want 204; body = %s", route.name, rec.Code, rec.Body.String())
		}
	}
	if mutating429s != 7 {
		t.Fatalf("mutating routes throttled = %d, want 7", mutating429s)
	}
}

func TestDataMount_PlacementTransitionFencesNewRequests(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}
	routes := &DataRoutes{CreateIssue: handler}
	h := newDataMountHarness(t, dataMountHarnessOptions{createNode: true, data: routes})
	route := dataRouteSpecs("WS")[1]
	token := h.token(t, nil)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- h.request(t, route, token, "WS", nil) }()
	<-entered
	setDataPlacementState(t, h, domain.PlacementStateReleasing)
	close(release)
	if rec := <-done; rec.Code != http.StatusNoContent {
		t.Fatalf("admitted request status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	rec := h.request(t, route, token, "WS", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-transition status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}
