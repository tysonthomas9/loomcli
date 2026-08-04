package fleet

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fleetAuthTestKey is the signing key used across fleet auth tests.
var fleetAuthTestKey = []byte("test-secret-key-for-jwt-signing!")

// newTestToken generates a valid JWT for testing.
func newTestToken(t *testing.T, workerID string, expiry time.Duration) string {
	t.Helper()
	token, err := GenerateWorkerToken(workerID, nil, fleetAuthTestKey, expiry)
	if err != nil {
		t.Fatalf("failed to generate test token: %v", err)
	}
	return token
}

// echoClaimsHandler is a test handler that writes claims from context into the response.
func echoClaimsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := WorkerClaimsFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("no claims in context"))
			return
		}
		w.WriteHeader(http.StatusOK)
		safeID := html.EscapeString(claims.WorkerID)
		w.Write([]byte(safeID))
	})
}

func TestFleetAuth_ValidToken_PassesThrough(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	token := newTestToken(t, "worker-1", time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "worker-1" {
		t.Errorf("body = %q, want %q", w.Body.String(), "worker-1")
	}
}

func TestFleetAuth_ClaimsInContext(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)

	var capturedClaims *WorkerClaims
	var claimsOK bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims, claimsOK = WorkerClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware(inner)

	token := newTestToken(t, "worker-ctx-test", time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !claimsOK {
		t.Fatal("expected claims in context")
	}
	if capturedClaims.WorkerID != "worker-ctx-test" {
		t.Errorf("WorkerID = %q, want %q", capturedClaims.WorkerID, "worker-ctx-test")
	}
}

func TestFleetAuth_MissingAuthorizationHeader_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetAuth_NoBearerPrefix_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetAuth_BearerWithoutToken_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestFleetAuth_ExpiredToken_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	token := newTestToken(t, "worker-1", -1*time.Second)
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetAuth_WrongSigningKey_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	// Generate token with a different key
	wrongKey := []byte("wrong-key-not-the-right-signing!!")
	token, err := GenerateWorkerToken("worker-1", nil, wrongKey, time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestFleetAuth_MalformedToken_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestFleetAuth_EmptySigningKey_RejectsAll(t *testing.T) {
	middleware := NewFleetAuthMiddleware(nil)
	handler := middleware(echoClaimsHandler())

	// Even a "valid" token should be rejected when no signing key is configured
	token := newTestToken(t, "worker-1", time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestFleetAuth_EmptyByteSliceSigningKey_RejectsAll(t *testing.T) {
	middleware := NewFleetAuthMiddleware([]byte{})
	handler := middleware(echoClaimsHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestWorkerClaimsFromContext_NoClaims(t *testing.T) {
	ctx := context.Background()
	claims, ok := WorkerClaimsFromContext(ctx)
	if ok {
		t.Error("expected ok = false for empty context")
	}
	if claims != nil {
		t.Errorf("expected nil claims, got %v", claims)
	}
}

func TestWorkerClaimsFromContext_WithClaims(t *testing.T) {
	expected := &WorkerClaims{WorkerID: "test-worker"}
	ctx := context.WithValue(context.Background(), workerClaimsContextKey{}, expected)

	claims, ok := WorkerClaimsFromContext(ctx)
	if !ok {
		t.Fatal("expected ok = true")
	}
	if claims.WorkerID != "test-worker" {
		t.Errorf("WorkerID = %q, want %q", claims.WorkerID, "test-worker")
	}
}

func TestFleetAuth_ResponseIsJSON(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestFleetAuth_DoesNotSetContentTypeOnSuccess(t *testing.T) {
	// When the middleware passes through, the inner handler controls Content-Type.
	// Verify the middleware doesn't double-set it for successful requests.
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	handler := middleware(inner)

	token := newTestToken(t, "worker-1", time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	// The inner handler sets text/plain; middleware should not override on success path
	ct := w.Header().Get("Content-Type")
	if ct != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain")
	}
}

func TestFleetDone_WithAuth_MissingHeader_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/fleet/done/worker-1", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetDone_WithAuth_ValidToken_PassesThrough(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	token := newTestToken(t, "worker-1", time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/fleet/done/worker-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "worker-1" {
		t.Errorf("body = %q, want %q", w.Body.String(), "worker-1")
	}
}

func TestFleetDone_WithAuth_ExpiredToken_Returns401(t *testing.T) {
	middleware := NewFleetAuthMiddleware(fleetAuthTestKey)
	handler := middleware(echoClaimsHandler())

	token := newTestToken(t, "worker-1", -1*time.Second)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/fleet/done/worker-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}
