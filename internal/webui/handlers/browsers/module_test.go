package browsers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// spyBackend records every FleetDB call and the delegation it carried.
type spyBackend struct {
	mu    sync.Mutex
	calls []spyCall
	err   error
	// onCall runs before returning; used to assert ordering against notify.
	onCall func()
}

type spyCall struct {
	op, ws, delegation, id string
}

func (b *spyBackend) record(op, ws, delegation, id string) error {
	b.mu.Lock()
	b.calls = append(b.calls, spyCall{op, ws, delegation, id})
	on := b.onCall
	b.mu.Unlock()
	if on != nil {
		on()
	}
	return b.err
}

func (b *spyBackend) Calls() []spyCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]spyCall(nil), b.calls...)
}

func (b *spyBackend) Create(_ context.Context, ws, d string, req domain.BrowserCreate) (*domain.Browser, error) {
	if err := b.record("create", ws, d, ""); err != nil {
		return nil, err
	}
	return &domain.Browser{ID: "b1", WorkspaceKey: ws, Name: req.Name, RequestID: req.RequestID, Status: domain.BrowserStatusStarting}, nil
}

func (b *spyBackend) List(_ context.Context, ws, d string) ([]domain.Browser, error) {
	if err := b.record("list", ws, d, ""); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *spyBackend) Get(_ context.Context, ws, d, id string) (*domain.Browser, error) {
	if err := b.record("get", ws, d, id); err != nil {
		return nil, err
	}
	return &domain.Browser{ID: id, WorkspaceKey: ws}, nil
}

func (b *spyBackend) Select(_ context.Context, ws, d, id string) (*domain.Browser, error) {
	if err := b.record("select", ws, d, id); err != nil {
		return nil, err
	}
	return &domain.Browser{ID: id, WorkspaceKey: ws, Selected: true}, nil
}

type harness struct {
	t        *testing.T
	store    *memstore.Store
	backend  *spyBackend
	agents   *browserauth.AgentSessionRegistry
	ops      *browserauth.OperatorSessionRegistry
	pub      ed25519.PublicKey
	notified []string
	mu       sync.Mutex
	mux      *http.ServeMux
	module   *Module
}

type harnessOpt func(*Config)

func newHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	for _, ws := range []string{"ws", "ws2"} {
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
			t.Fatal(err)
		}
	}
	mustRole := func(ws, name, kind string) {
		if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: ws, Name: name, Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}
	mustAgent := func(ws, name, role string) {
		if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: ws, Name: name, RoleName: role}); err != nil {
			t.Fatal(err)
		}
	}
	mustRole("ws", "lead", "")
	mustRole("ws", "pair", "interactive")
	mustRole("ws", "coder", "worker")
	mustAgent("ws", "lead", "lead")
	mustAgent("ws", "pairer", "pair")
	mustAgent("ws", "bob", "coder")
	mustRole("ws2", "coder", "worker")
	mustAgent("ws2", "remote-only", "coder")

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := browserauth.NewSigner("k1", priv, "loom", "fleet-db")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, store: st, backend: &spyBackend{}, pub: pub,
		agents: browserauth.NewAgentSessionRegistry(), ops: browserauth.NewOperatorSessionRegistry()}
	cfg := Config{
		Store: st, Backend: h.backend, Signer: signer,
		AgentSessions: h.agents, OperatorSessions: h.ops,
		Notify: func(ws, owner, action string) {
			h.mu.Lock()
			h.notified = append(h.notified, ws+"/"+owner+"/"+action)
			h.mu.Unlock()
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	h.module = NewModule(cfg)
	h.mux = http.NewServeMux()
	h.module.Register(h.mux)
	h.module.RegisterAgentRoutes(h.mux)
	return h
}

func (h *harness) do(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	return h.doVia(h.mux, method, path, body, headers)
}

func (h *harness) doVia(handler http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if s, ok := body.(string); ok {
			buf.WriteString(s)
		} else {
			_ = json.NewEncoder(&buf).Encode(body)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	for k, v := range headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func (h *harness) agentToken(ws, agent, terminal string) string {
	h.t.Helper()
	tok, _, err := h.agents.Issue(ws, agent, "orch-1", terminal)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func (h *harness) claims(delegation string) jwt.MapClaims {
	h.t.Helper()
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(delegation, claims, func(*jwt.Token) (any, error) { return h.pub, nil },
		jwt.WithValidMethods([]string{"EdDSA"})); err != nil {
		h.t.Fatalf("delegation does not verify: %v", err)
	}
	return claims
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body["code"]
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, status, rec.Body.String())
	}
	if code != "" {
		if got := errorCode(t, rec); got != code {
			t.Fatalf("code = %q, want %q; body=%s", got, code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Agent-session routes
// ---------------------------------------------------------------------------

func TestAgentCreateBindsOwnerFromSession(t *testing.T) {
	h := newHarness(t)
	tok := h.agentToken("ws", "lead", "term-lead")
	rec := h.do(http.MethodPost, AgentRoutePrefix, map[string]string{"name": "Docs", "request_id": "r1"},
		map[string]string{browserauth.AgentSessionHeader: tok, "X-Actor": "pairer"})
	expect(t, rec, http.StatusCreated, "")
	calls := h.backend.Calls()
	if len(calls) != 1 || calls[0].op != "create" || calls[0].ws != "ws" {
		t.Fatalf("calls = %+v", calls)
	}
	c := h.claims(calls[0].delegation)
	if c["owner_agent_id"] != "lead" || c["principal_kind"] != "agent_session" || c["workspace_key"] != "ws" {
		t.Fatalf("delegation claims = %v", c)
	}
	ops, _ := c["operations"].([]any)
	if len(ops) != 1 || ops[0] != domain.BrowserOpCreate {
		t.Fatalf("operations = %v (must be exactly the op being performed)", ops)
	}
	if len(h.notified) != 1 || h.notified[0] != "ws/lead/browser.create" {
		t.Fatalf("notified = %v", h.notified)
	}
}

func TestAgentRoutesRequireSession(t *testing.T) {
	h := newHarness(t)
	forged := map[string]string{
		"Host": "127.0.0.1:8080", "Origin": "http://127.0.0.1:8080",
		"X-Actor": "lead", "X-Loom-Agent-Name": "lead",
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, AgentRoutePrefix},
		{http.MethodGet, AgentRoutePrefix},
		{http.MethodGet, AgentRoutePrefix + "/b1"},
		{http.MethodPost, AgentRoutePrefix + "/b1/select"},
	} {
		var body any
		if tc.method == http.MethodPost && tc.path == AgentRoutePrefix {
			body = map[string]string{"name": "x", "request_id": "r"}
		}
		expect(t, h.do(tc.method, tc.path, body, forged), http.StatusUnauthorized, CodeAgentSessionRequired)
		bad := map[string]string{browserauth.AgentSessionHeader: "not-a-real-session"}
		expect(t, h.do(tc.method, tc.path, body, bad), http.StatusUnauthorized, CodeAgentSessionRequired)
	}
	if n := len(h.backend.Calls()); n != 0 {
		t.Fatalf("backend called %d times without a session", n)
	}
}

func TestAgentCannotActForAnotherAgent(t *testing.T) {
	h := newHarness(t)
	tok := h.agentToken("ws", "lead", "term-lead")
	hdr := map[string]string{browserauth.AgentSessionHeader: tok}
	rec := h.do(http.MethodPost, AgentRoutePrefix, map[string]string{"name": "x", "request_id": "r", "owner_agent_id": "pairer"}, hdr)
	expect(t, rec, http.StatusForbidden, CodeForbidden)
	expect(t, h.do(http.MethodGet, AgentRoutePrefix+"?owner_agent_id=pairer", nil, hdr), http.StatusForbidden, CodeForbidden)
	expect(t, h.do(http.MethodGet, AgentRoutePrefix+"?workspace=ws2", nil, hdr), http.StatusForbidden, CodeForbidden)
	// A matching claim is fine.
	expect(t, h.do(http.MethodGet, AgentRoutePrefix+"?owner_agent_id=lead", nil, hdr), http.StatusOK, "")
	if calls := h.backend.Calls(); len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestAgentSessionForNonInteractiveTargetsIsRejected(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct{ ws, agent string }{
		{"ws", "bob"},          // worker
		{"ws", "ghost"},        // missing
		{"ws2", "remote-only"}, // worker in another workspace
	} {
		tok := h.agentToken(tc.ws, tc.agent, "term-"+tc.agent)
		rec := h.do(http.MethodGet, AgentRoutePrefix, nil, map[string]string{browserauth.AgentSessionHeader: tok})
		expect(t, rec, http.StatusNotFound, CodeAgentNotFound)
	}
	if n := len(h.backend.Calls()); n != 0 {
		t.Fatalf("backend called for invalid targets: %d", n)
	}
}

func TestAgentSessionRevokedAfterPTYEnds(t *testing.T) {
	h := newHarness(t)
	tok := h.agentToken("ws", "lead", "term-lead")
	hdr := map[string]string{browserauth.AgentSessionHeader: tok}
	expect(t, h.do(http.MethodGet, AgentRoutePrefix, nil, hdr), http.StatusOK, "")
	h.agents.RevokeToken(tok)
	expect(t, h.do(http.MethodGet, AgentRoutePrefix, nil, hdr), http.StatusUnauthorized, CodeAgentSessionRequired)
}

func TestAgentListShape(t *testing.T) {
	h := newHarness(t)
	tok := h.agentToken("ws", "pairer", "term-p")
	rec := h.do(http.MethodGet, AgentRoutePrefix, nil, map[string]string{browserauth.AgentSessionHeader: tok})
	expect(t, rec, http.StatusOK, "")
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["workspace"] != "ws" || got["owner_agent_id"] != "pairer" {
		t.Fatalf("body = %v", got)
	}
	if b, ok := got["browsers"].([]any); !ok || len(b) != 0 {
		t.Fatalf("browsers must be [] not null: %s", rec.Body.String())
	}
}

func TestAgentCreateStrictBody(t *testing.T) {
	h := newHarness(t)
	hdr := map[string]string{browserauth.AgentSessionHeader: h.agentToken("ws", "lead", "t")}
	expect(t, h.do(http.MethodPost, AgentRoutePrefix, `{"name":"x","request_id":"r","created_by":"root"}`, hdr), http.StatusBadRequest, CodeInvalid)
	expect(t, h.do(http.MethodPost, AgentRoutePrefix, `{"name":"x"}`, hdr), http.StatusBadRequest, CodeInvalid)
	if n := len(h.backend.Calls()); n != 0 {
		t.Fatalf("backend called with invalid body: %d", n)
	}
}

func TestBackendErrorsMapAndNeverNotify(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrNotFound, http.StatusNotFound, CodeNotFound},
		{domain.ErrBrowserRequestConflict, http.StatusConflict, CodeRequestConflict},
		{domain.ErrBrowserUnauthorized, http.StatusServiceUnavailable, CodeUnavailable},
		{errors.New("dial tcp: refused"), http.StatusServiceUnavailable, CodeUnavailable},
	}
	for _, tc := range cases {
		h := newHarness(t)
		h.backend.err = tc.err
		hdr := map[string]string{browserauth.AgentSessionHeader: h.agentToken("ws", "lead", "t")}
		expect(t, h.do(http.MethodPost, AgentRoutePrefix, map[string]string{"name": "x", "request_id": "r"}, hdr), tc.status, tc.code)
		if len(h.notified) != 0 {
			t.Fatalf("%v: notified on failure: %v", tc.err, h.notified)
		}
	}
}

func TestNotifyRunsAfterCommit(t *testing.T) {
	h := newHarness(t)
	h.backend.onCall = func() {
		if len(h.notified) != 0 {
			t.Error("notify ran before FleetDB returned")
		}
	}
	hdr := map[string]string{browserauth.AgentSessionHeader: h.agentToken("ws", "lead", "t")}
	expect(t, h.do(http.MethodPost, AgentRoutePrefix+"/b1/select", nil, hdr), http.StatusOK, "")
	if len(h.notified) != 1 || h.notified[0] != "ws/lead/browser.select" {
		t.Fatalf("notified = %v", h.notified)
	}
}

func TestMissingBackendOrSignerIs503AfterAuth(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.Signer = nil })
	expect(t, h.do(http.MethodGet, AgentRoutePrefix, nil, nil), http.StatusUnauthorized, CodeAgentSessionRequired)
	hdr := map[string]string{browserauth.AgentSessionHeader: h.agentToken("ws", "lead", "t")}
	expect(t, h.do(http.MethodGet, AgentRoutePrefix, nil, hdr), http.StatusServiceUnavailable, CodeUnavailable)
}

// ---------------------------------------------------------------------------
// Local desktop operator routes
// ---------------------------------------------------------------------------

func operatorPath(ws, agent, suffix string) string {
	return "/api/workspaces/" + ws + "/agents/" + agent + "/browsers" + suffix
}

func TestLocalOperatorRequiresNativeSession(t *testing.T) {
	h := newHarness(t)
	// A regular browser tab: loopback Host/Origin, X-Actor, even an agent
	// session header — none of it is an operator session.
	forged := map[string]string{
		"Host": "127.0.0.1:8080", "Origin": "http://127.0.0.1:8080",
		"X-Actor": "lead", browserauth.AgentSessionHeader: h.agentToken("ws", "lead", "t"),
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, operatorPath("ws", "lead", "")},
		{http.MethodGet, operatorPath("ws", "lead", "/b1")},
		{http.MethodPost, operatorPath("ws", "lead", "/b1/select")},
	} {
		expect(t, h.do(tc.method, tc.path, nil, forged), http.StatusUnauthorized, CodeOperatorSessionRequired)
	}
	if n := len(h.backend.Calls()); n != 0 {
		t.Fatalf("backend called without an operator session: %d", n)
	}
}

func TestLocalOperatorHappyPathAndTargets(t *testing.T) {
	h := newHarness(t)
	tok, sess, err := h.ops.Issue("ws", 501)
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{browserauth.OperatorSessionHeader: tok}
	rec := h.do(http.MethodGet, operatorPath("ws", "pairer", ""), nil, hdr)
	expect(t, rec, http.StatusOK, "")
	if strings.TrimSpace(rec.Body.String()) != `{"browsers":[]}` {
		t.Fatalf("operator list body = %s", rec.Body.String())
	}
	calls := h.backend.Calls()
	c := h.claims(calls[0].delegation)
	if c["principal_kind"] != "workspace_operator" || c["auth_mode"] != "local_desktop" ||
		c["local_operator_session_id"] != sess.ID || c["sub"] != "local-os-user:501" || c["owner_agent_id"] != "pairer" {
		t.Fatalf("operator claims = %v", c)
	}
	if strings.Contains(calls[0].delegation, tok) {
		t.Fatal("operator bearer leaked into the delegation")
	}

	expect(t, h.do(http.MethodGet, operatorPath("ws", "bob", ""), nil, hdr), http.StatusNotFound, CodeAgentNotFound)
	expect(t, h.do(http.MethodGet, operatorPath("ws", "ghost", ""), nil, hdr), http.StatusNotFound, CodeAgentNotFound)
	expect(t, h.do(http.MethodGet, operatorPath("ws2", "remote-only", ""), nil, hdr), http.StatusForbidden, CodeForbidden)
	if n := len(h.backend.Calls()); n != 1 {
		t.Fatalf("backend calls = %d, want 1", n)
	}

	// Select notifies for the target owner after commit.
	expect(t, h.do(http.MethodPost, operatorPath("ws", "lead", "/b9/select"), nil, hdr), http.StatusOK, "")
	if len(h.notified) != 1 || h.notified[0] != "ws/lead/browser.select" {
		t.Fatalf("notified = %v", h.notified)
	}
}

func TestLocalOperatorOutlivesAgentSession(t *testing.T) {
	h := newHarness(t)
	agentTok := h.agentToken("ws", "lead", "t")
	h.agents.RevokeToken(agentTok)
	opTok, _, _ := h.ops.Issue("ws", 501)
	expect(t, h.do(http.MethodGet, operatorPath("ws", "lead", ""), nil, map[string]string{browserauth.OperatorSessionHeader: opTok}), http.StatusOK, "")
}

func TestLocalOperatorExpiredAndRevoked(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.ops.SetClock(func() time.Time { return now })
	tok, _, _ := h.ops.Issue("ws", 501)
	now = now.Add(browserauth.OperatorIdleTTL + time.Second)
	expect(t, h.do(http.MethodGet, operatorPath("ws", "lead", ""), nil, map[string]string{browserauth.OperatorSessionHeader: tok}),
		http.StatusUnauthorized, CodeOperatorSessionExpired)
	tok2, _, _ := h.ops.Issue("ws", 501)
	h.ops.Revoke(tok2)
	expect(t, h.do(http.MethodGet, operatorPath("ws", "lead", ""), nil, map[string]string{browserauth.OperatorSessionHeader: tok2}),
		http.StatusUnauthorized, CodeOperatorSessionRequired)
}

func TestLocalOperatorBridgeDownIs503(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.OperatorSessions = nil })
	expect(t, h.do(http.MethodGet, operatorPath("ws", "lead", ""), nil, nil), http.StatusServiceUnavailable, CodeBridgeUnavailable)
}

// ---------------------------------------------------------------------------
// Remote operator through the real JWT middleware
// ---------------------------------------------------------------------------

type remoteIssuer struct {
	key    *rsa.PrivateKey
	server *httptest.Server
	cache  *middleware.JWKSCache
}

func newRemoteIssuer(t *testing.T) *remoteIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "rk", "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	cache := middleware.NewJWKSCacheNoFetch(srv.URL, srv.Client(), nil)
	if err := cache.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &remoteIssuer{key: key, server: srv, cache: cache}
}

func (ri *remoteIssuer) token(t *testing.T, sub string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": sub, "iss": "auth", "aud": "loom",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
	})
	tok.Header["kid"] = "rk"
	s, err := tok.SignedString(ri.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRemoteOperatorThroughJWTMiddleware(t *testing.T) {
	ri := newRemoteIssuer(t)
	var seen []string
	h := newHarness(t, func(c *Config) {
		c.RemoteAuth = true
		c.OperatorSessions = nil
		c.ResolvePermission = func(_ context.Context, ws string, id middleware.UserIdentity, target, op string) error {
			seen = append(seen, id.UserID+"@"+ws+"/"+target+":"+op)
			if id.UserID != "alice" {
				return errors.New("denied")
			}
			return nil
		}
	})
	chain := middleware.Auth(middleware.AuthConfig{JWKSCache: ri.cache, Issuer: "auth", Audience: "loom"})(h.mux)
	path := operatorPath("ws", "lead", "")

	// No bearer: the real middleware rejects before the module runs.
	if rec := h.doVia(chain, http.MethodGet, path, nil, map[string]string{"X-Actor": "alice"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer = %d", rec.Code)
	}
	// A local operator session is not a remote identity.
	opTok, _, _ := h.ops.Issue("ws", 501)
	if rec := h.doVia(chain, http.MethodGet, path, nil, map[string]string{browserauth.OperatorSessionHeader: opTok}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("local session in remote mode = %d", rec.Code)
	}
	// Valid identity without permission: 403 and no FleetDB call.
	rec := h.doVia(chain, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + ri.token(t, "mallory")})
	expect(t, rec, http.StatusForbidden, CodeForbidden)
	if n := len(h.backend.Calls()); n != 0 {
		t.Fatalf("backend called for denied user: %d", n)
	}
	// Permitted identity.
	rec = h.doVia(chain, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + ri.token(t, "alice")})
	expect(t, rec, http.StatusOK, "")
	c := h.claims(h.backend.Calls()[0].delegation)
	if c["auth_mode"] != "remote_user" || c["sub"] != "user:alice" {
		t.Fatalf("remote claims = %v", c)
	}
	if _, ok := c["local_operator_session_id"]; ok {
		t.Fatal("remote delegation carries a local session id")
	}
	if len(seen) != 2 || seen[1] != "alice@ws/lead:"+domain.BrowserOpList {
		t.Fatalf("resolver saw %v", seen)
	}
}

func TestRemoteOperatorWithoutResolverIsDenied(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RemoteAuth = true; c.ResolvePermission = nil })
	req := httptest.NewRequest(http.MethodGet, operatorPath("ws", "lead", ""), nil)
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "alice"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	expect(t, rec, http.StatusForbidden, CodeForbidden)
}

// TestRemoteOperatorGrantsAreWorkspaceScoped drives the production grant
// resolver through the real JWT middleware: a subject granted in one workspace
// must not reach browsers in another, and nothing reaches FleetDB on denial.
func TestRemoteOperatorGrantsAreWorkspaceScoped(t *testing.T) {
	ri := newRemoteIssuer(t)
	resolve, _, err := ParseOperatorGrants(`[{"workspace":"ws","subjects":["alice"]},{"workspace":"ws2","subjects":["carol"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(c *Config) {
		c.RemoteAuth = true
		c.OperatorSessions = nil
		c.ResolvePermission = resolve
	})
	chain := middleware.Auth(middleware.AuthConfig{JWKSCache: ri.cache, Issuer: "auth", Audience: "loom"})(h.mux)
	bearer := func(sub string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + ri.token(t, sub)}
	}

	// alice is granted in ws only: ws2 is denied before FleetDB for every op.
	expect(t, h.doVia(chain, http.MethodGet, operatorPath("ws2", "remote-only", ""), nil, bearer("alice")), http.StatusForbidden, CodeForbidden)
	expect(t, h.doVia(chain, http.MethodGet, operatorPath("ws2", "remote-only", "/b1"), nil, bearer("alice")), http.StatusForbidden, CodeForbidden)
	expect(t, h.doVia(chain, http.MethodPost, operatorPath("ws2", "remote-only", "/b1/select"), nil, bearer("alice")), http.StatusForbidden, CodeForbidden)
	// carol is granted in ws2 only: ws is denied.
	expect(t, h.doVia(chain, http.MethodGet, operatorPath("ws", "lead", ""), nil, bearer("carol")), http.StatusForbidden, CodeForbidden)
	if n := len(h.backend.Calls()); n != 0 {
		t.Fatalf("backend called for cross-workspace request: %d", n)
	}

	// Allowed workspace succeeds with a delegation bound to that workspace.
	rec := h.doVia(chain, http.MethodGet, operatorPath("ws", "lead", ""), nil, bearer("alice"))
	expect(t, rec, http.StatusOK, "")
	calls := h.backend.Calls()
	if len(calls) != 1 {
		t.Fatalf("backend calls = %d", len(calls))
	}
	c := h.claims(calls[0].delegation)
	if c["sub"] != "user:alice" || c["auth_mode"] != "remote_user" {
		t.Fatalf("claims = %v", c)
	}
}
