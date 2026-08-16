package driverapi

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// mintToken signs a run token for the harness run with optional claim
// mutation. The default claims mirror the run's current owner credentials,
// exactly what TK3's in-process minting at claim will produce.
func (h *testHarness) mintToken(t *testing.T, ttl time.Duration, mutate func(*driverpkg.RunTokenClaims)) string {
	t.Helper()
	claims := driverpkg.RunTokenClaims{
		WorkspaceKey: "WS",
		RunID:        h.runID,
		NodeID:       h.nodeID,
		LeaseID:      h.leaseID,
		FencingToken: h.fence,
	}
	if mutate != nil {
		mutate(&claims)
	}
	token, err := driverpkg.MintRunToken(claims, h.runTokenKey, ttl)
	if err != nil {
		t.Fatalf("MintRunToken: %v", err)
	}
	return token
}

func (h *testHarness) tokenHeadersForRun(t *testing.T, run *execution.DriverRunRecord) map[string]string {
	t.Helper()
	claims := driverpkg.RunTokenClaims{
		WorkspaceKey: run.WorkspaceKey,
		RunID:        run.RunID,
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		FencingToken: run.FencingToken,
	}
	token, err := driverpkg.MintRunToken(claims, h.runTokenKey, time.Hour)
	if err != nil {
		t.Fatalf("MintRunToken: %v", err)
	}
	return bearer(token)
}

func (h *testHarness) ownerLeaseToken(t *testing.T) string {
	t.Helper()
	token, err := driverpkg.DeriveDriverRunLeaseToken(h.runTokenKey, "WS", h.runID, h.nodeID, h.leaseID)
	if err != nil {
		t.Fatalf("DeriveDriverRunLeaseToken: %v", err)
	}
	return token
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// TestDriverAPIRunTokenOnly proves the §7 step 9 contract: a workflow call
// authenticates with the run token ALONE — no header quad, no shared static
// token — and the server derives the fenced identity from the claims.
func TestDriverAPIRunTokenOnly(t *testing.T) {
	h := newTestHarness(t)
	token := h.mintToken(t, time.Minute, nil)
	resp, decoded := h.do(t, opRequest{op: "list-agents", headers: bearer(token)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
}

func TestDriverAPIRejectsLegacyStaticBearerAndIdentityHeaders(t *testing.T) {
	h := newTestHarness(t)
	headers := h.ownerHeaders(t)
	headers["Authorization"] = "Bearer secret-token"

	resp, decoded := h.do(t, opRequest{op: "list-agents", headers: headers})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d (%v), want 401", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}
}

// TestDriverAPIRunTokenRejections is the rejection matrix for the token auth
// path on the generic {op} route.
func TestDriverAPIRunTokenRejections(t *testing.T) {
	tests := []struct {
		name       string
		headers    func(h *testHarness) map[string]string
		wantStatus int
		wantCode   string
	}{
		{
			name: "expired token",
			headers: func(h *testHarness) map[string]string {
				token := h.mintToken(t, time.Nanosecond, nil)
				time.Sleep(20 * time.Millisecond)
				return bearer(token)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "token_expired",
		},
		{
			name: "forged signature fails",
			headers: func(h *testHarness) map[string]string {
				forged, err := driverpkg.MintRunToken(driverpkg.RunTokenClaims{
					WorkspaceKey: "WS",
					RunID:        h.runID,
					NodeID:       h.nodeID,
					LeaseID:      h.leaseID,
					FencingToken: h.fence,
				}, bytes.Repeat([]byte{0x13}, 32), time.Minute)
				if err != nil {
					t.Fatalf("mint forged token: %v", err)
				}
				return bearer(forged)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthenticated",
		},
		{
			name: "forged signature resembling the retired static token fails",
			headers: func(h *testHarness) map[string]string {
				forged, err := driverpkg.MintRunToken(driverpkg.RunTokenClaims{RunID: h.runID},
					bytes.Repeat([]byte{0x13}, 32), time.Minute)
				if err != nil {
					t.Fatalf("mint forged token: %v", err)
				}
				return bearer(forged)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthenticated",
		},
		{
			name: "retired identity header rejected even when it matches token",
			headers: func(h *testHarness) map[string]string {
				headers := bearer(h.mintToken(t, time.Minute, nil))
				headers["X-Loom-Driver-Run-Id"] = h.runID
				return headers
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "identity_mismatch",
		},
		{
			name: "token scoped to another workspace",
			headers: func(h *testHarness) map[string]string {
				return bearer(h.mintToken(t, time.Minute, func(c *driverpkg.RunTokenClaims) {
					c.WorkspaceKey = "OTHER"
				}))
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "identity_mismatch",
		},
		{
			name: "token for unknown run",
			headers: func(h *testHarness) map[string]string {
				return bearer(h.mintToken(t, time.Minute, func(c *driverpkg.RunTokenClaims) {
					c.RunID = "run-ghost"
				}))
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t)
			resp, decoded := h.do(t, opRequest{op: "list-agents", headers: tc.headers(h)})
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d (%v), want %d", resp.StatusCode, decoded, tc.wantStatus)
			}
			if code := errorCode(t, decoded); code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

// TestDriverAPIRunTokenRevokedByFinish: revocation needs no denylist — a
// token for a terminal run fails verifyParent regardless of expiry.
func TestDriverAPIRunTokenRevokedByFinish(t *testing.T) {
	h := newTestHarness(t)
	token := h.mintToken(t, time.Hour, nil)
	if _, err := h.store.DriverRuns().Finish(context.Background(), "WS", h.runID, execution.DriverRunFinish{
		NodeID:       h.nodeID,
		LeaseID:      h.leaseID,
		FencingToken: h.fence,
		Status:       execution.DriverRunCompleted,
	}); err != nil {
		t.Fatalf("Finish driver run: %v", err)
	}
	resp, decoded := h.do(t, opRequest{op: "list-agents", headers: bearer(token)})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d (%v), want 409", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "invalid_transition" {
		t.Fatalf("error code = %q, want invalid_transition", code)
	}
}

// TestDriverAPIRunTokenRevokedByReclaim: a re-claim issues a new lease and
// fencing token, so a token carrying the old lease fails the fenced
// heartbeat inside verifyParent.
func TestDriverAPIRunTokenRevokedByReclaim(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	stale := h.mintToken(t, time.Hour, nil)
	// Full await cycle so the resume-eligibility gate grants the re-queue:
	// register -> suspend -> resolve -> resume.
	awaitKey := execution.AwaitInstanceKey(h.runID, 1)
	if _, err := h.store.Awaits().RegisterAwaitAndCheck(ctx, "WS", execution.AwaitRegistration{
		InstanceKey: awaitKey,
		RunID:       h.runID,
		Pattern:     "pr.merged:pr#1",
		Deadline:    time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("RegisterAwaitAndCheck: %v", err)
	}
	if _, err := h.store.DriverRuns().Suspend(ctx, "WS", h.runID, h.nodeID, h.leaseID, h.fence, awaitKey); err != nil {
		t.Fatalf("Suspend driver run: %v", err)
	}
	if _, err := h.store.Awaits().ResolveAwait(ctx, "WS", awaitKey, "evt-1", nil, "alice"); err != nil {
		t.Fatalf("ResolveAwait: %v", err)
	}
	if _, err := h.store.DriverRuns().ResumeAwaiting(ctx, "WS", h.runID, awaitKey, "evt-1"); err != nil {
		t.Fatalf("ResumeAwaiting driver run: %v", err)
	}
	reclaimed, err := h.store.DriverRuns().Claim(ctx, "WS", h.runID, "node-2", "lease-2")
	if err != nil {
		t.Fatalf("re-claim driver run: %v", err)
	}
	if reclaimed.FencingToken == h.fence {
		t.Fatalf("re-claim kept fencing token %d, expected a new fence", h.fence)
	}

	resp, decoded := h.do(t, opRequest{op: "list-agents", headers: bearer(stale)})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stale token status = %d (%v), want 403", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}

	// A token minted for the new lease (what the re-claim mints) works.
	fresh := h.mintToken(t, time.Hour, func(c *driverpkg.RunTokenClaims) {
		c.NodeID = reclaimed.NodeID
		c.LeaseID = reclaimed.LeaseID
		c.FencingToken = reclaimed.FencingToken
	})
	resp, decoded = h.do(t, opRequest{op: "list-agents", headers: bearer(fresh)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh token status = %d (%v), want 200", resp.StatusCode, decoded)
	}
}

// TestDriverAPIRunTokenTwoSegmentOps covers the explicitly registered routes
// that cannot ride the generic {op} pattern: they share the same token gate.
func TestDriverAPIRunTokenTwoSegmentOps(t *testing.T) {
	h := newTestHarness(t)
	token := h.mintToken(t, time.Minute, nil)

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/api/workspaces/WS/driver/events/awaits", nil)
	if err != nil {
		t.Fatalf("new awaits request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do awaits request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events/awaits with token status = %d, want 200", resp.StatusCode)
	}

	// workflows/await with a token but empty params proves the token cleared
	// auth: identity resolution succeeds and the request reaches param
	// validation (400 invalid) instead of failing with 401.
	respOp, decoded := h.do(t, opRequest{op: "workflows/await", headers: bearer(token)})
	if respOp.StatusCode != http.StatusBadRequest {
		t.Fatalf("workflows/await with token status = %d (%v), want 400", respOp.StatusCode, decoded)
	}
}

// TestWatchEpicRunTokenAuth proves the watch SSE GET shares the token gate.
func TestWatchEpicRunTokenAuth(t *testing.T) {
	h := newTestHarness(t)

	expired := h.mintToken(t, time.Nanosecond, nil)
	time.Sleep(20 * time.Millisecond)
	stream := openWatch(t, h, "", bearer(expired))
	if stream.resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token watch status = %d, want 401", stream.resp.StatusCode)
	}

	stream = openWatch(t, h, "", bearer(h.mintToken(t, time.Minute, nil)))
	if stream.resp.StatusCode != http.StatusOK {
		t.Fatalf("token watch status = %d, want 200", stream.resp.StatusCode)
	}
	frame, ok := stream.nextEvent(t)
	if !ok || frame.event != "snapshot" {
		t.Fatalf("first watch frame = (%+v, %v), want snapshot event", frame, ok)
	}
}
