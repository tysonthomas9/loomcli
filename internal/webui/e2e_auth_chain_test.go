package webui

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// newFullChainTestServer creates an httptest.Server with the complete middleware
// chain matching server.go: rateLimit → securityHeaders → extAuth → cors → mux.
// If jwksURL is empty, ExtAuth runs in passthrough mode (open mode).
// The returned server has test routes registered:
//   - GET /api/workspaces/{ws}/issues — protected
//   - GET /api/health — public
//   - GET /health — public
//   - POST /api/fleet/claim — public (fleet uses its own auth)
func newFullChainTestServer(t *testing.T, jwksURL, issuer, audience string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Protected: returns UserIdentity as JSON if present
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", func(w http.ResponseWriter, r *http.Request) {
		identity, ok := middleware.UserIdentityFromContext(r.Context())
		if ok {
			respondJSON(w, http.StatusOK, map[string]string{
				"user_id": identity.UserID,
				"email":   identity.Email,
				"name":    identity.Name,
			})
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok_no_identity"})
	})

	// Public routes
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Fleet route (public, uses its own auth)
	mux.HandleFunc("POST /api/fleet/claim", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "fleet_ok"})
	})

	// Build middleware chain
	var extAuthMW func(http.Handler) http.Handler
	if jwksURL != "" {
		cache := middleware.NewJWKSCacheNoFetch(jwksURL, nil, nil)
		if err := cache.Fetch(t.Context()); err != nil {
			// Allow initial fetch to fail (tested in resilience tests)
			t.Logf("initial JWKS fetch failed (may be intentional): %v", err)
		}
		extAuthMW = middleware.Auth(middleware.AuthConfig{
			JWKSCache: cache,
			Issuer:    issuer,
			Audience:  audience,
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	} else {
		extAuthMW = middleware.Auth(middleware.AuthConfig{JWKSCache: nil})
	}

	corsMiddleware := middleware.CORS(middleware.CORSConfig{})
	securityMiddleware := middleware.SecurityHeaders(middleware.SecurityConfig{})
	_, rateLimitMiddleware := middleware.RateLimit(middleware.DefaultRateLimitConfig())

	handler := rateLimitMiddleware(securityMiddleware(extAuthMW(corsMiddleware(mux))))
	return httptest.NewServer(handler)
}

// --- Test 5: JWT claims ---

func TestFullChain_ValidJWT_AllClaimsPresent(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(jwksSrv.Close)

	issuer := "https://auth.example.com"
	audience := "loom"
	srv := newFullChainTestServer(t, jwksSrv.URL, issuer, audience)
	t.Cleanup(srv.Close)

	claims := extAuthClaims{
		Email: "alice@example.com",
		Name:  "Alice",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			ID:        "unique-jti-abc",
		},
	}
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, body)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["user_id"] != "user-123" {
		t.Errorf("user_id = %q, want %q", body["user_id"], "user-123")
	}
	if body["email"] != "alice@example.com" {
		t.Errorf("email = %q, want %q", body["email"], "alice@example.com")
	}
	if body["name"] != "Alice" {
		t.Errorf("name = %q, want %q", body["name"], "Alice")
	}
}

func TestFullChain_ValidJWT_OptionalClaimsEmpty(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(jwksSrv.Close)

	issuer := "https://auth.example.com"
	audience := "loom"
	srv := newFullChainTestServer(t, jwksSrv.URL, issuer, audience)
	t.Cleanup(srv.Close)

	claims := extAuthClaims{
		// No Email or Name
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-minimal",
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["user_id"] != "user-minimal" {
		t.Errorf("user_id = %q, want %q", body["user_id"], "user-minimal")
	}
	if body["email"] != "" {
		t.Errorf("email = %q, want empty", body["email"])
	}
	if body["name"] != "" {
		t.Errorf("name = %q, want empty", body["name"])
	}
}

// --- Test 6: Cross-system JWT confusion ---

func TestFullChain_CrossSystem_FleetJWTToProtectedRoute(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(jwksSrv.Close)

	srv := newFullChainTestServer(t, jwksSrv.URL, "https://auth.example.com", "loom")
	t.Cleanup(srv.Close)

	// Generate HS256 fleet JWT
	fleetKey := []byte("fleet-signing-key-for-test-1234!")
	fleetToken, err := GenerateWorkerToken("worker-1", nil, fleetKey, time.Hour)
	if err != nil {
		t.Fatalf("failed to generate fleet token: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+fleetToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; HS256 fleet JWT must not pass RS256 ExtAuth", resp.StatusCode)
	}
}

func TestFullChain_CrossSystem_UserJWTToFleetRoute(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(jwksSrv.Close)

	// Build a server with fleet auth on the fleet route
	mux := http.NewServeMux()

	fleetKey := []byte("fleet-signing-key-for-test-1234!")
	fleetMW := NewFleetAuthMiddleware(fleetKey)
	mux.Handle("POST /api/fleet/claim", fleetMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "fleet_ok"})
	})))

	// Apply the same middleware chain
	cache := middleware.NewJWKSCacheNoFetch(jwksSrv.URL, nil, nil)
	if err := cache.Fetch(t.Context()); err != nil {
		t.Fatalf("initial JWKS fetch failed: %v", err)
	}
	extAuthMW := middleware.Auth(middleware.AuthConfig{
		JWKSCache: cache,
		Issuer:    "https://auth.example.com",
		Audience:  "loom",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	handler := extAuthMW(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Sign RS256 user JWT
	claims := validExtAuthClaims("https://auth.example.com", "loom")
	userToken := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Fleet route is public (isPublicRoute=true), so ExtAuth skips it.
	// Fleet auth middleware rejects RS256 user JWT because ValidateWorkerToken
	// expects HS256.
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 401; RS256 user JWT must not pass HS256 fleet auth; body = %s", resp.StatusCode, body)
	}
}

// --- Test 7: JWT in query param rejected ---

func TestFullChain_JWTInQueryParam_Rejected(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(jwksSrv.Close)

	srv := newFullChainTestServer(t, jwksSrv.URL, "https://auth.example.com", "loom")
	t.Cleanup(srv.Close)

	// Sign a valid JWT
	claims := validExtAuthClaims("https://auth.example.com", "loom")
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	// Send JWT via query param instead of Authorization header
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues?token="+token, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// extractToken rejects JWT-like tokens (>200 chars, 2 dots) in query params.
	// ExtAuth sees no token → 401.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; JWT in query param must be rejected", resp.StatusCode)
	}
}

// --- Test 8: Open mode ---

func TestFullChain_OpenMode_NoAuth(t *testing.T) {
	// nil JWKS cache = open mode (passthrough)
	srv := newFullChainTestServer(t, "", "", "")
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; open mode should allow all requests", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok_no_identity" {
		t.Errorf("expected no identity in open mode, got %v", body)
	}
}

func TestFullChain_OpenMode_ExtraHeadersIgnored(t *testing.T) {
	srv := newFullChainTestServer(t, "", "", "")
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer random-garbage-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; open mode should ignore auth headers", resp.StatusCode)
	}
}

func TestFullChain_PublicRoutes_NoAuthRequired(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(jwksSrv.Close)

	srv := newFullChainTestServer(t, jwksSrv.URL, "https://auth.example.com", "loom")
	t.Cleanup(srv.Close)

	// Public routes should work without auth even when ExtAuth is enabled
	publicPaths := []string{"/api/health", "/health"}
	for _, path := range publicPaths {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want 200 for public route %s; body = %s", resp.StatusCode, path, body)
			}
		})
	}
}

func TestFullChain_ProtectedRoute_NoAuth_Returns401(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(jwksSrv.Close)

	srv := newFullChainTestServer(t, jwksSrv.URL, "https://auth.example.com", "loom")
	t.Cleanup(srv.Close)

	// Protected route without auth should return 401
	resp, err := http.Get(srv.URL + "/api/workspaces/test-ws/issues")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for protected route without auth", resp.StatusCode)
	}
}
