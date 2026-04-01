package webui

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// --- Test 9: Auth service down ---

func TestResilience_JWKSUnreachable_Returns401(t *testing.T) {
	// Create a server then immediately close it — simulates unreachable JWKS
	closedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedSrv.Close()

	srv := newFullChainTestServer(t, closedSrv.URL, "https://auth.example.com", "loom")
	t.Cleanup(srv.Close)

	claims := validExtAuthClaims("https://auth.example.com", "loom")
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Cache is empty (initial fetch failed), should return 401 not 500
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 401 (not 500 or panic); body = %s", resp.StatusCode, body)
	}
}

func TestResilience_JWKS500_Returns401(t *testing.T) {
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(errSrv.Close)

	srv := newFullChainTestServer(t, errSrv.URL, "https://auth.example.com", "loom")
	t.Cleanup(srv.Close)

	claims := validExtAuthClaims("https://auth.example.com", "loom")
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestResilience_JWKS500_CacheRetained(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	var mu sync.Mutex
	returnError := false

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		shouldError := returnError
		mu.Unlock()
		if shouldError {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, makeJWKSJSON(jk))
	}))
	t.Cleanup(jwksSrv.Close)

	// Build cache manually so we can manipulate it
	cache := newTestJWKSCache(t, jwksSrv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	extAuthMW := NewExtAuthMiddleware(ExtAuthConfig{
		JWKSCache: cache,
		Issuer:    "https://auth.example.com",
		Audience:  "loom",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(extAuthMW(mux))
	t.Cleanup(srv.Close)

	// First request with valid JWT should succeed
	claims := validExtAuthClaims("https://auth.example.com", "loom")
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial request status = %d, want 200", resp.StatusCode)
	}

	// Now make JWKS return 500
	mu.Lock()
	returnError = true
	mu.Unlock()

	// Force a fetch attempt (will fail with 500)
	_ = cache.fetch(t.Context())

	// Request with the same key should still work (cache retained)
	token2 := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req2.Header.Set("Authorization", "Bearer "+token2)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; cache should be retained after JWKS 500", resp2.StatusCode)
	}
}

func TestResilience_JWKSTimeout_NoHang(t *testing.T) {
	// Create a server that delays 10 seconds (simulates timeout)
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, makeJWKSJSON(rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")))
	}))
	t.Cleanup(slowSrv.Close)

	// newTestJWKSCache uses a 5-second HTTP client timeout
	cache := newTestJWKSCache(t, slowSrv.URL)

	extAuthMW := NewExtAuthMiddleware(ExtAuthConfig{
		JWKSCache: cache,
		Issuer:    "https://auth.example.com",
		Audience:  "loom",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(extAuthMW(mux))
	t.Cleanup(srv.Close)

	claims := validExtAuthClaims("https://auth.example.com", "loom")
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	start := time.Now()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	elapsed := time.Since(start)

	// Should complete within ~5s (HTTP client timeout), not hang for 10s
	if elapsed > 8*time.Second {
		t.Errorf("request took %v, should complete within HTTP client timeout (~5s)", elapsed)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (cache empty after timeout)", resp.StatusCode)
	}
}

// --- Test 10: Key rotation ---

func TestResilience_KeyRotation_NewKeyAccepted(t *testing.T) {
	jk1 := rsaKeyToJWK(&testRSAKey.PublicKey, "key-a")
	var mu sync.Mutex
	currentBody := makeJWKSJSON(jk1)
	var fetchCount atomic.Int64

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		mu.Lock()
		body := currentBody
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(jwksSrv.Close)

	cache := newTestJWKSCache(t, jwksSrv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	extAuthMW := NewExtAuthMiddleware(ExtAuthConfig{
		JWKSCache: cache,
		Issuer:    "https://auth.example.com",
		Audience:  "loom",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(extAuthMW(mux))
	t.Cleanup(srv.Close)

	// Request with key A — should work
	claimsA := validExtAuthClaims("https://auth.example.com", "loom")
	tokenA := signExtAuthJWT(t, claimsA, testRSAKey, "key-a", jwt.SigningMethodRS256)
	reqA, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	reqA.Header.Set("Authorization", "Bearer "+tokenA)
	respA, err := http.DefaultClient.Do(reqA)
	if err != nil {
		t.Fatalf("request A failed: %v", err)
	}
	respA.Body.Close()
	if respA.StatusCode != http.StatusOK {
		t.Fatalf("key A status = %d, want 200", respA.StatusCode)
	}

	// Rotate: add key B alongside key A
	jk2 := rsaKeyToJWK(&testRSAKey2.PublicKey, "key-b")
	mu.Lock()
	currentBody = makeJWKSJSON(jk1, jk2)
	mu.Unlock()

	// Request with key B — should trigger on-demand refresh and succeed
	claimsB := validExtAuthClaims("https://auth.example.com", "loom")
	tokenB := signExtAuthJWT(t, claimsB, testRSAKey2, "key-b", jwt.SigningMethodRS256)
	reqB, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	reqB.Header.Set("Authorization", "Bearer "+tokenB)
	respB, err := http.DefaultClient.Do(reqB)
	if err != nil {
		t.Fatalf("request B failed: %v", err)
	}
	respB.Body.Close()
	if respB.StatusCode != http.StatusOK {
		t.Errorf("key B after rotation status = %d, want 200", respB.StatusCode)
	}

	// Verify fetch count: initial + on-demand
	if fetchCount.Load() < 2 {
		t.Errorf("expected at least 2 JWKS fetches (initial + on-demand), got %d", fetchCount.Load())
	}
}

func TestResilience_KeyRotation_OldKeyStillValid(t *testing.T) {
	jk1 := rsaKeyToJWK(&testRSAKey.PublicKey, "key-a")
	jk2 := rsaKeyToJWK(&testRSAKey2.PublicKey, "key-b")
	var mu sync.Mutex
	currentBody := makeJWKSJSON(jk1)

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body := currentBody
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(jwksSrv.Close)

	cache := newTestJWKSCache(t, jwksSrv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	extAuthMW := NewExtAuthMiddleware(ExtAuthConfig{
		JWKSCache: cache,
		Issuer:    "https://auth.example.com",
		Audience:  "loom",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(extAuthMW(mux))
	t.Cleanup(srv.Close)

	// Rotate JWKS to only key B (remove key A from server)
	mu.Lock()
	currentBody = makeJWKSJSON(jk2)
	mu.Unlock()

	// Request with key A — still in local cache from initial fetch
	claims := validExtAuthClaims("https://auth.example.com", "loom")
	token := signExtAuthJWT(t, claims, testRSAKey, "key-a", jwt.SigningMethodRS256)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	// Old key should still work from cache (grace period until TTL expires)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; old key should remain valid while in cache", resp.StatusCode)
	}
}

// --- Test 11: Kid storm ---

func TestResilience_KidStorm_CooldownEnforced(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	var fetchCount atomic.Int64
	jwksSrv := newTestJWKSServer(makeJWKSJSON(jk), &fetchCount)
	t.Cleanup(jwksSrv.Close)

	cache := newTestJWKSCache(t, jwksSrv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}
	initialFetches := fetchCount.Load()

	extAuthMW := NewExtAuthMiddleware(ExtAuthConfig{
		JWKSCache: cache,
		Issuer:    "https://auth.example.com",
		Audience:  "loom",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(extAuthMW(mux))
	t.Cleanup(srv.Close)

	// Send 100 concurrent requests, each with a unique random kid
	var wg sync.WaitGroup
	responses := make([]int, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claims := validExtAuthClaims("https://auth.example.com", "loom")
			// Sign with testRSAKey but use a unique kid that won't be in JWKS
			token := signExtAuthJWT(t, claims, testRSAKey, fmt.Sprintf("storm-%d", i), jwt.SigningMethodRS256)

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/workspaces/test-ws/issues", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			responses[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()

	// All should return 401 (kids not in JWKS)
	for i, code := range responses {
		if code != 0 && code != http.StatusUnauthorized {
			t.Errorf("response[%d] = %d, want 401", i, code)
		}
	}

	// Cooldown should limit fetches
	additionalFetches := fetchCount.Load() - initialFetches
	if additionalFetches > 3 {
		t.Errorf("kid storm caused %d additional JWKS fetches (want ≤ 3, cooldown should prevent flood)", additionalFetches)
	}
}
