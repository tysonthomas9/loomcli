package leadapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type testHarness struct {
	server *httptest.Server
	store  store.Store
	key    []byte

	workspace  string
	nodeID     string
	agentID    string
	sessionID  string
	generation int64
}

type harnessOptions struct {
	createNode    bool
	createSession bool
	generation    int64
	configure     func(*testHarness, *Module)
}

func newHarness(t *testing.T, opts harnessOptions) *testHarness {
	t.Helper()
	h := &testHarness{
		store:      memstore.New(),
		key:        bytes.Repeat([]byte{0x33}, 32),
		workspace:  "WS",
		nodeID:     "lead-node-1",
		agentID:    "lead",
		sessionID:  "lead-session-1",
		generation: opts.generation,
	}
	if h.generation == 0 {
		h.generation = 7
	}
	h.seed(t, opts)
	module := NewModule(Config{Store: h.store, TokenKey: h.key})
	if opts.configure != nil {
		opts.configure(h, module)
	}
	mux := http.NewServeMux()
	module.Register(mux)
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

func (h *testHarness) seed(t *testing.T, opts harnessOptions) {
	t.Helper()
	ctx := context.Background()
	if opts.createNode {
		createNode(t, ctx, h)
	}
	if opts.createSession {
		createSession(t, ctx, h)
	}
}

func createNode(t *testing.T, ctx context.Context, h *testHarness) {
	t.Helper()
	createNodeRecord(t, ctx, h.store, h.workspace, h.nodeID, h.agentID, h.generation, domain.PlacementStateActive)
}

func createSession(t *testing.T, ctx context.Context, h *testHarness) {
	t.Helper()
	createSessionRecord(t, ctx, h.store, h.workspace, h.sessionID, h.agentID, h.nodeID, domain.AgentSessionRunning)
}

func createNodeRecord(t *testing.T, ctx context.Context, st store.Store, ws, nodeID, owner string, gen int64, state domain.PlacementState) {
	t.Helper()
	_, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    ws,
		NodeID:          nodeID,
		OwnerActor:      owner,
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement:       &domain.NodePlacement{SandboxID: "sandbox-" + nodeID, Generation: gen, State: state},
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Hour,
	})
	if err != nil {
		t.Fatalf("create node %s/%s: %v", ws, nodeID, err)
	}
}

func createSessionRecord(t *testing.T, ctx context.Context, st store.Store, ws, sessionID, agentID, nodeID string, status domain.AgentSessionStatus) {
	t.Helper()
	_, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: ws,
		SessionID:    sessionID,
		AgentID:      agentID,
		NodeID:       nodeID,
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       status,
	})
	if err != nil {
		t.Fatalf("create session %s/%s: %v", ws, sessionID, err)
	}
}

func (h *testHarness) mintToken(t *testing.T, ttl time.Duration, mutate func(*leadtoken.OccupantClaims)) string {
	t.Helper()
	claims := leadtoken.OccupantClaims{
		WorkspaceKey: h.workspace,
		PlacementID:  h.nodeID,
		Generation:   h.generation,
		Caps:         []string{leadtoken.CapLeadSession},
	}
	if mutate != nil {
		mutate(&claims)
	}
	token, err := leadtoken.MintOccupantToken(claims, h.key, ttl)
	if err != nil {
		t.Fatalf("MintOccupantToken: %v", err)
	}
	return token
}

func (h *testHarness) postHeartbeat(t *testing.T, pathWS, token string) (*http.Response, map[string]any) {
	t.Helper()
	return h.postLeadOp(t, pathWS, "heartbeat", token, []byte("{}"))
}

func (h *testHarness) postLeadOp(t *testing.T, pathWS, op, token string, body []byte) (*http.Response, map[string]any) {
	t.Helper()
	return h.postLeadOpWithAuth(t, pathWS, op, bearerHeader(token), body)
}

func (h *testHarness) postLeadOpWithAuth(t *testing.T, pathWS, op, auth string, body []byte) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/"+pathWS+"/lead/"+op, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST heartbeat: %v", err)
	}
	defer resp.Body.Close()
	return resp, decodeBody(t, resp)
}

func bearerHeader(token string) string {
	if token == "" {
		return ""
	}
	return "Bearer " + token
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func TestLeadAPIAuthRejections(t *testing.T) {
	tests := rejectionCases()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.opts)
			token := ""
			if tt.mint != nil {
				token = tt.mint(t, h)
			}
			resp, decoded := h.postHeartbeat(t, tt.pathWS, token)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d (%v), want %d", resp.StatusCode, decoded, tt.wantStatus)
			}
			if code := errorCode(t, decoded); code != tt.wantCode {
				t.Fatalf("error code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func TestLeadAPIAuthHappyPath(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
}

func TestLeadAPITokenExpiredIsRetryable(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	resp, decoded := h.postHeartbeat(t, "WS", expiredToken(t, h))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d (%v), want 401", resp.StatusCode, decoded)
	}
	assertError(t, decoded, "token_expired", true)
}

func TestLeadAPIUnknownOp(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	resp, decoded := h.postLeadOp(t, "WS", "missing", h.mintToken(t, time.Hour, nil), []byte("{}"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d (%v), want 404", resp.StatusCode, decoded)
	}
	assertError(t, decoded, "unknown_op", false)
}

func TestLeadAPIOpsDeclareCapabilities(t *testing.T) {
	module := NewModule(Config{TokenKey: bytes.Repeat([]byte{0x33}, 32)})
	for op, entry := range module.ops {
		if strings.TrimSpace(entry.cap) == "" {
			t.Fatalf("lead op %q has no capability", op)
		}
	}
}

func TestLeadAPIOpWithoutCapabilityDenied(t *testing.T) {
	called := false
	h := newHarness(t, harnessOptions{
		createNode:    true,
		createSession: true,
		configure: func(_ *testHarness, m *Module) {
			m.ops["uncapped"] = leadOp{handler: func(context.Context, string, occupantIdentity, []byte) (any, error) {
				called = true
				return map[string]string{"ok": "no"}, nil
			}}
		},
	})
	resp, decoded := h.postLeadOp(t, "WS", "uncapped", h.mintToken(t, time.Hour, nil), []byte("{}"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d (%v), want 403", resp.StatusCode, decoded)
	}
	assertError(t, decoded, "cap_denied", false)
	if called {
		t.Fatal("uncapped handler was called")
	}
}

type rejectionCase struct {
	name       string
	opts       harnessOptions
	pathWS     string
	mint       func(*testing.T, *testHarness) string
	wantStatus int
	wantCode   string
}

func rejectionCases() []rejectionCase {
	withRecord := harnessOptions{createNode: true, createSession: true}
	return []rejectionCase{
		{"no bearer", withRecord, "WS", nil, http.StatusUnauthorized, "unauthenticated"},
		{"wrong key", withRecord, "WS", wrongKeyToken, http.StatusUnauthorized, "unauthenticated"},
		{"wrong workspace", withRecord, "OTHER", validToken, http.StatusUnauthorized, "identity_mismatch"},
		{"generation mismatch", withRecord, "WS", staleGenerationToken, http.StatusUnauthorized, "generation_fenced"},
		{"missing cap", withRecord, "WS", missingCapToken, http.StatusForbidden, "cap_denied"},
		{"placement absent", harnessOptions{}, "WS", validToken, http.StatusUnauthorized, "placement_absent"},
	}
}

func validToken(t *testing.T, h *testHarness) string {
	t.Helper()
	return h.mintToken(t, time.Hour, nil)
}

func staleGenerationToken(t *testing.T, h *testHarness) string {
	t.Helper()
	return h.mintToken(t, time.Hour, func(c *leadtoken.OccupantClaims) { c.Generation-- })
}

func missingCapToken(t *testing.T, h *testHarness) string {
	t.Helper()
	return h.mintToken(t, time.Hour, func(c *leadtoken.OccupantClaims) { c.Caps = nil })
}

func wrongKeyToken(t *testing.T, h *testHarness) string {
	t.Helper()
	claims := leadtoken.OccupantClaims{
		WorkspaceKey: h.workspace,
		PlacementID:  h.nodeID,
		Generation:   h.generation,
		Caps:         []string{leadtoken.CapLeadSession},
	}
	token, err := leadtoken.MintOccupantToken(claims, bytes.Repeat([]byte{0x44}, 32), time.Hour)
	if err != nil {
		t.Fatalf("MintOccupantToken wrong key: %v", err)
	}
	return token
}

func expiredToken(t *testing.T, h *testHarness) string {
	t.Helper()
	now := time.Now()
	claims := leadtoken.OccupantClaims{
		WorkspaceKey: h.workspace,
		PlacementID:  h.nodeID,
		Generation:   h.generation,
		Caps:         []string{leadtoken.CapLeadSession},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   leadtoken.OccupantActor(h.nodeID),
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

func errorCode(t *testing.T, decoded map[string]any) string {
	t.Helper()
	errObj, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response missing error envelope: %v", decoded)
	}
	code, _ := errObj["code"].(string)
	return code
}

func assertError(t *testing.T, decoded map[string]any, wantCode string, wantRetryable bool) {
	t.Helper()
	errObj, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response missing error envelope: %v", decoded)
	}
	if code, _ := errObj["code"].(string); code != wantCode {
		t.Fatalf("error code = %q, want %q", code, wantCode)
	}
	if retryable, _ := errObj["retryable"].(bool); retryable != wantRetryable {
		t.Fatalf("retryable = %t, want %t in %v", retryable, wantRetryable, decoded)
	}
}

func errorMessage(t *testing.T, decoded map[string]any) string {
	t.Helper()
	errObj, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response missing error envelope: %v", decoded)
	}
	msg, _ := errObj["message"].(string)
	return msg
}

func TestLeadAPIHeartbeatBumpsNodeAndSession(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	beforeNode := getNode(t, h)
	beforeSession := getSession(t, h)
	time.Sleep(2 * time.Millisecond)

	resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	assertHeartbeatAdvanced(t, beforeNode.LastHeartbeat, getNode(t, h).LastHeartbeat, "node")
	assertHeartbeatAdvanced(t, beforeSession.LastHeartbeat, getSession(t, h).LastHeartbeat, "session")
}

func TestLeadAPIRequestBodyCannotSelectIdentity(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	ctx := context.Background()
	createNodeRecord(t, ctx, h.store, "OTHER", "other-node", "other-agent", 3, domain.PlacementStateActive)
	createSessionRecord(t, ctx, h.store, "OTHER", "other-session", "other-agent", "other-node", domain.AgentSessionRunning)
	otherNode := getNodeByID(t, h, "OTHER", "other-node")
	otherSession := getSessionByID(t, h, "OTHER", "other-session")
	beforeNode := getNode(t, h)
	beforeSession := getSession(t, h)
	body := []byte(`{"workspaceKey":"OTHER","nodeId":"other-node","sessionId":"other-session"}`)
	time.Sleep(2 * time.Millisecond)

	resp, decoded := h.postLeadOp(t, "WS", "heartbeat", h.mintToken(t, time.Hour, nil), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	assertHeartbeatAdvanced(t, beforeNode.LastHeartbeat, getNode(t, h).LastHeartbeat, "token node")
	assertHeartbeatAdvanced(t, beforeSession.LastHeartbeat, getSession(t, h).LastHeartbeat, "token session")
	assertHeartbeatUnchanged(t, otherNode.LastHeartbeat, getNodeByID(t, h, "OTHER", "other-node").LastHeartbeat, "body node")
	assertHeartbeatUnchanged(t, otherSession.LastHeartbeat, getSessionByID(t, h, "OTHER", "other-session").LastHeartbeat, "body session")
}

func TestLeadAPIUsesNodeNotOwnerActorForSession(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	setNodeOwnerActor(t, h, "tyson")
	resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
}

func TestLeadAPIMultipleActiveSessionsViolatesInvariant(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	setNodeOwnerActor(t, h, "")
	createSessionRecord(t, context.Background(), h.store, h.workspace, "lead-session-2", "other-agent", h.nodeID, domain.AgentSessionRunning)

	resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d (%v), want 500", resp.StatusCode, decoded)
	}
	msg := errorMessage(t, decoded)
	if !strings.Contains(msg, h.nodeID) || !strings.Contains(msg, "2 active orchestration sessions") {
		t.Fatalf("error message = %q, want node and count", msg)
	}
}

func TestLeadAPISessionLookupDoesNotTruncateBeforeActiveFilter(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	ctx := context.Background()
	time.Sleep(2 * time.Millisecond)
	for i := 0; i < 9; i++ {
		sessionID := "finished-session-" + strconv.Itoa(i)
		createSessionRecord(t, ctx, h.store, h.workspace, sessionID, h.agentID, h.nodeID, domain.AgentSessionCompleted)
		markSessionFinished(t, h, sessionID)
	}

	resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
}

func TestLeadAPIHeartbeatRenewsNearExpiry(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	wantCaps := []string{leadtoken.CapLeadSession, "lead:custom"}
	token := h.mintToken(t, 29*time.Minute, func(c *leadtoken.OccupantClaims) {
		c.Caps = append([]string(nil), wantCaps...)
	})
	resp, decoded := h.postHeartbeat(t, "WS", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	raw, _ := decoded["occupantToken"].(string)
	if raw == "" {
		t.Fatalf("occupantToken empty in response %v", decoded)
	}
	claims, err := leadtoken.ParseOccupantToken(raw, h.key)
	if err != nil {
		t.Fatalf("ParseOccupantToken renewed: %v", err)
	}
	if claims.Generation != h.generation {
		t.Fatalf("renewed generation = %d, want %d", claims.Generation, h.generation)
	}
	if claims.WorkspaceKey != h.workspace || claims.PlacementID != h.nodeID {
		t.Fatalf("renewed workspace/placement = %q/%q", claims.WorkspaceKey, claims.PlacementID)
	}
	if !reflect.DeepEqual(claims.Caps, wantCaps) {
		t.Fatalf("renewed caps = %v, want %v", claims.Caps, wantCaps)
	}
}

func TestLeadAPIRechecksPlacementBeforeRenewal(t *testing.T) {
	h := newHarness(t, harnessOptions{
		createNode:    true,
		createSession: true,
		configure: func(h *testHarness, m *Module) {
			base := h.store.Nodes()
			m.store = overrideStore{Store: h.store, nodes: overrideNodeStore{
				NodeStore: base,
				heartbeat: func(ctx context.Context, ws, nodeID string, ttl time.Duration) (*domain.Node, error) {
					node, err := base.Heartbeat(ctx, ws, nodeID, ttl)
					if err != nil {
						return nil, err
					}
					node.Placement.Generation++
					return node, nil
				},
			}}
		},
	})
	resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, 29*time.Minute, nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d (%v), want 401", resp.StatusCode, decoded)
	}
	assertError(t, decoded, "generation_fenced", false)
	if _, ok := decoded["occupantToken"]; ok {
		t.Fatalf("occupantToken present after refreshed generation mismatch: %v", decoded)
	}
}

func TestLeadAPIHeartbeatDoesNotRenewFarFromExpiry(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if _, ok := decoded["occupantToken"]; ok {
		t.Fatalf("occupantToken present for far-from-expiry token: %v", decoded)
	}
}

func TestLeadAPIPlacementLookupErrors(t *testing.T) {
	tests := []struct {
		name       string
		opts       harnessOptions
		wantStatus int
		wantCode   string
		retryable  bool
	}{
		{"missing placement", harnessOptions{}, http.StatusUnauthorized, "placement_absent", false},
		{"store unavailable", unavailableStoreOptions(), http.StatusServiceUnavailable, "unavailable", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.opts)
			resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d (%v), want %d", resp.StatusCode, decoded, tt.wantStatus)
			}
			assertError(t, decoded, tt.wantCode, tt.retryable)
		})
	}
}

func unavailableStoreOptions() harnessOptions {
	return harnessOptions{
		createNode:    true,
		createSession: true,
		configure: func(h *testHarness, m *Module) {
			m.store = overrideStore{Store: h.store, nodes: overrideNodeStore{
				NodeStore: h.store.Nodes(),
				get: func(context.Context, string, string) (*domain.Node, error) {
					return nil, errors.New("fleet-db unavailable")
				},
			}}
		},
	}
}

func TestLeadAPIPlacementStateGate(t *testing.T) {
	tests := []struct {
		state     domain.PlacementState
		wantOK    bool
		wantCode  string
		retryable bool
	}{
		{domain.PlacementStateProvisioning, true, "", false},
		{domain.PlacementStateActive, true, "", false},
		{domain.PlacementStateReleasing, false, "placement_released", false},
		{domain.PlacementStateReleased, false, "placement_released", false},
		{domain.PlacementStateLost, false, "placement_released", false},
		{"", false, "placement_released", false},
		{"paused", false, "placement_released", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			h := newHarness(t, harnessOptions{createNode: true, createSession: true})
			setPlacementState(t, h, tt.state)
			resp, decoded := h.postHeartbeat(t, "WS", h.mintToken(t, time.Hour, nil))
			assertPlacementStateResponse(t, resp, decoded, tt.wantOK, tt.wantCode, tt.retryable)
		})
	}
}

func assertPlacementStateResponse(t *testing.T, resp *http.Response, decoded map[string]any, wantOK bool, wantCode string, retryable bool) {
	t.Helper()
	if wantOK {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
		}
		return
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d (%v), want 401", resp.StatusCode, decoded)
	}
	assertError(t, decoded, wantCode, retryable)
}

func TestLeadAPIBearerSchemeIsCaseInsensitive(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	token := h.mintToken(t, time.Hour, nil)
	resp, decoded := h.postLeadOpWithAuth(t, "WS", "heartbeat", "bearer "+token, []byte("{}"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
}

func TestLeadAPIOversizedBodyReturns413(t *testing.T) {
	h := newHarness(t, harnessOptions{createNode: true, createSession: true})
	body := bytes.Repeat([]byte{'x'}, maxLeadOpBodyBytes+1)
	resp, decoded := h.postLeadOp(t, "WS", "heartbeat", h.mintToken(t, time.Hour, nil), body)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d (%v), want 413", resp.StatusCode, decoded)
	}
	assertError(t, decoded, "invalid", false)
	if strings.Contains(errorMessage(t, decoded), "http: request body too large") {
		t.Fatalf("oversized body error leaked raw MaxBytesReader text: %v", decoded)
	}
}

func getNode(t *testing.T, h *testHarness) *domain.Node {
	t.Helper()
	return getNodeByID(t, h, h.workspace, h.nodeID)
}

func getNodeByID(t *testing.T, h *testHarness, ws, nodeID string) *domain.Node {
	t.Helper()
	node, err := h.store.Nodes().Get(context.Background(), ws, nodeID)
	if err != nil {
		t.Fatalf("get node %s/%s: %v", ws, nodeID, err)
	}
	return node
}

func getSession(t *testing.T, h *testHarness) *domain.AgentSession {
	t.Helper()
	return getSessionByID(t, h, h.workspace, h.sessionID)
}

func getSessionByID(t *testing.T, h *testHarness, ws, sessionID string) *domain.AgentSession {
	t.Helper()
	session, err := h.store.AgentSessions().Get(context.Background(), ws, sessionID)
	if err != nil {
		t.Fatalf("get session %s/%s: %v", ws, sessionID, err)
	}
	return session
}

func assertHeartbeatAdvanced(t *testing.T, before, after time.Time, label string) {
	t.Helper()
	if !after.After(before) {
		t.Fatalf("%s heartbeat = %s, want after %s", label, after, before)
	}
}

func assertHeartbeatUnchanged(t *testing.T, before, after time.Time, label string) {
	t.Helper()
	if !after.Equal(before) {
		t.Fatalf("%s heartbeat = %s, want unchanged %s", label, after, before)
	}
}

func setNodeOwnerActor(t *testing.T, h *testHarness, owner string) {
	t.Helper()
	if _, err := h.store.Nodes().Update(context.Background(), h.workspace, h.nodeID, store.NodeUpdate{OwnerActor: &owner}); err != nil {
		t.Fatalf("set node owner actor: %v", err)
	}
}

func setPlacementState(t *testing.T, h *testHarness, state domain.PlacementState) {
	t.Helper()
	placement := *getNode(t, h).Placement
	placement.State = state
	placementPtr := &placement
	if _, err := h.store.Nodes().Update(context.Background(), h.workspace, h.nodeID, store.NodeUpdate{Placement: &placementPtr}); err != nil {
		t.Fatalf("set placement state: %v", err)
	}
}

func markSessionFinished(t *testing.T, h *testHarness, sessionID string) {
	t.Helper()
	status := domain.AgentSessionCompleted
	finishedAt := time.Now().UTC()
	finishedAtPtr := &finishedAt
	patch := store.AgentSessionUpdate{Status: &status, FinishedAt: &finishedAtPtr}
	if _, err := h.store.AgentSessions().Update(context.Background(), h.workspace, sessionID, patch); err != nil {
		t.Fatalf("mark session finished: %v", err)
	}
}

type overrideStore struct {
	store.Store
	nodes store.NodeStore
}

func (s overrideStore) Nodes() store.NodeStore {
	return s.nodes
}

type overrideNodeStore struct {
	store.NodeStore
	get       func(context.Context, string, string) (*domain.Node, error)
	heartbeat func(context.Context, string, string, time.Duration) (*domain.Node, error)
}

func (s overrideNodeStore) Get(ctx context.Context, ws, nodeID string) (*domain.Node, error) {
	if s.get != nil {
		return s.get(ctx, ws, nodeID)
	}
	return s.NodeStore.Get(ctx, ws, nodeID)
}

func (s overrideNodeStore) Heartbeat(ctx context.Context, ws, nodeID string, ttl time.Duration) (*domain.Node, error) {
	if s.heartbeat != nil {
		return s.heartbeat(ctx, ws, nodeID, ttl)
	}
	return s.NodeStore.Heartbeat(ctx, ws, nodeID, ttl)
}
