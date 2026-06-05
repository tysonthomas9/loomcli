package fleet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// fencingAt returns a FencingLookup that always reports the given current token.
func fencingAt(current int64) FencingLookup {
	return func(context.Context, string, string) (int64, bool, error) {
		return current, true, nil
	}
}

// validateWith returns a ValidateFunc over a fixed signing key.
func validateWith(key []byte) ValidateFunc {
	return func(token string) (*TaskRunClaims, error) { return ValidateTaskRunToken(token, key) }
}

// requestFor builds a request to a session write endpoint with the workspace in
// context and the sessionId path value set, optionally bearing a token.
func requestFor(method, workspace, sessionID, token string) *http.Request {
	req := httptest.NewRequest(method, "/api/workspaces/"+workspace+"/sessions/"+sessionID+"/artifact", nil)
	req.SetPathValue("sessionId", sessionID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req.WithContext(middleware.WithWorkspace(req.Context(), workspace))
}

// run drives the middleware and reports the status + whether next ran.
func run(t *testing.T, mw func(http.Handler) http.Handler, req *http.Request) (int, bool) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := TaskRunClaimsFromContext(r.Context()); !ok {
			t.Error("claims were not attached to context for the handler")
		}
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	return rec.Code, called
}

func freshToken(t *testing.T, c TaskRunClaims) string {
	t.Helper()
	tok, err := GenerateTaskRunToken(c, testSigningKey, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return tok
}

func TestTaskRunAuth_AllowsValidMatchingRequest(t *testing.T) {
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 7})
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), fencingAt(7), false) // equal fencing → current holder
	code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", tok))
	if code != http.StatusOK || !called {
		t.Fatalf("expected 200 + next called, got code=%d called=%v", code, called)
	}
}

func TestTaskRunAuth_RejectsMissingAndMalformedToken(t *testing.T) {
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), fencingAt(1), false)

	if code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", "")); code != http.StatusUnauthorized || called {
		t.Errorf("missing token: code=%d called=%v, want 401", code, called)
	}

	bad := requestFor(http.MethodPost, "DEMO", "sess-1", "")
	bad.Header.Set("Authorization", "Token abc") // wrong scheme
	if code, called := run(t, mw, bad); code != http.StatusUnauthorized || called {
		t.Errorf("malformed header: code=%d called=%v, want 401", code, called)
	}
}

func TestTaskRunAuth_RejectsInvalidToken(t *testing.T) {
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), fencingAt(1), false)
	// A token signed with a different key must not validate.
	other, err := GenerateTaskRunToken(TaskRunClaims{Workspace: "DEMO", SessionID: "sess-1"}, []byte("another-key-32-bytes-long-xxxxxx"), time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", other)); code != http.StatusUnauthorized || called {
		t.Errorf("foreign-signed token: code=%d called=%v, want 401", code, called)
	}
}

func TestTaskRunAuth_RejectsWrongSessionBinding(t *testing.T) {
	// Token bound to sess-1 but the path targets sess-2 → 403.
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 7})
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), fencingAt(7), false)
	if code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-2", tok)); code != http.StatusForbidden || called {
		t.Errorf("cross-session: code=%d called=%v, want 403", code, called)
	}
	// Cross-workspace → 403.
	if code, called := run(t, mw, requestFor(http.MethodPost, "OTHER", "sess-1", tok)); code != http.StatusForbidden || called {
		t.Errorf("cross-workspace: code=%d called=%v, want 403", code, called)
	}
}

func TestTaskRunAuth_RejectsStaleFencing(t *testing.T) {
	// Token fencing 7, current lease at 8 → stale writer → 409.
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 7})
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), fencingAt(8), false)
	if code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", tok)); code != http.StatusConflict || called {
		t.Errorf("stale fencing: code=%d called=%v, want 409", code, called)
	}
}

func TestTaskRunAuth_RejectsWhenNoActiveLease(t *testing.T) {
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 7})
	noLease := FencingLookup(func(context.Context, string, string) (int64, bool, error) { return 0, false, nil })
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), noLease, false)
	if code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", tok)); code != http.StatusConflict || called {
		t.Errorf("no active lease: code=%d called=%v, want 409", code, called)
	}
}

func TestTaskRunAuth_FencingLookupErrorIs503(t *testing.T) {
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 7})
	boom := FencingLookup(func(context.Context, string, string) (int64, bool, error) {
		return 0, false, context.DeadlineExceeded
	})
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), boom, false)
	if code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", tok)); code != http.StatusServiceUnavailable || called {
		t.Errorf("fencing lookup error: code=%d called=%v, want 503", code, called)
	}
}

func TestTaskRunAuth_ReadsSkipFencing(t *testing.T) {
	// A GET to a session endpoint is not fencing-gated: a stale token still reads.
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 1})
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), fencingAt(99), false) // would be stale if enforced
	if code, called := run(t, mw, requestFor(http.MethodGet, "DEMO", "sess-1", tok)); code != http.StatusOK || !called {
		t.Errorf("GET should skip fencing: code=%d called=%v, want 200", code, called)
	}
}

func TestTaskRunAuth_UnconfiguredRejects(t *testing.T) {
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", SessionID: "sess-1"})
	mw := NewTaskRunAuthMiddleware(nil, fencingAt(1), false) // no signing key
	if code, called := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", tok)); code != http.StatusUnauthorized || called {
		t.Errorf("unconfigured: code=%d called=%v, want 401", code, called)
	}
}

func TestTaskRunAuth_TokenOptional(t *testing.T) {
	mw := NewTaskRunAuthMiddleware(validateWith(testSigningKey), fencingAt(8), true) // token-optional

	// No token → passthrough to next (dev-mode), claims not required.
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, requestFor(http.MethodPost, "DEMO", "sess-1", ""))
	if rec.Code != http.StatusOK || !called {
		t.Errorf("no-token passthrough: code=%d called=%v, want 200", rec.Code, called)
	}

	// Token present → still enforced: a stale token is rejected even when optional.
	tok := freshToken(t, TaskRunClaims{Workspace: "DEMO", TaskID: "DEMO-1", SessionID: "sess-1", FencingToken: 7})
	if code, ran := run(t, mw, requestFor(http.MethodPost, "DEMO", "sess-1", tok)); code != http.StatusConflict || ran {
		t.Errorf("token-present enforced: code=%d ran=%v, want 409", code, ran)
	}
}
