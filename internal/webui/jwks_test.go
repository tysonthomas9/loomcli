package webui

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testRSAKey is a pre-generated 2048-bit RSA key pair for testing.
var testRSAKey *rsa.PrivateKey

// testRSAKey2 is a second pre-generated key pair for multi-key tests.
var testRSAKey2 *rsa.PrivateKey

func init() {
	var err error
	testRSAKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key: " + err.Error())
	}
	testRSAKey2, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key 2: " + err.Error())
	}
}

// rsaKeyToJWK converts an RSA public key to a jwkKey for test JWKS responses.
func rsaKeyToJWK(pub *rsa.PublicKey, kid string) jwkKey {
	return jwkKey{
		Kty: "RSA",
		Kid: kid,
		Alg: "RS256",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// makeJWKSJSON builds a JWKS JSON string from the given keys.
func makeJWKSJSON(keys ...jwkKey) string {
	resp := jwksResponse{Keys: keys}
	data, _ := json.Marshal(resp)
	return string(data)
}

// newTestJWKSServer creates an httptest.Server that returns the given JWKS body.
// The fetchCount is atomically incremented on each request.
func newTestJWKSServer(body string, fetchCount *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fetchCount != nil {
			fetchCount.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

// newTestJWKSCache creates a JWKSCache pointing at the given URL with a short
// refresh interval suitable for testing. Background refresh is disabled by
// using a very long interval — tests trigger refreshes explicitly.
func newTestJWKSCache(t *testing.T, url string) *JWKSCache {
	t.Helper()
	c := &JWKSCache{
		endpoint: url,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger:   slog.Default(),
		keys:     make(map[string]*rsa.PublicKey),
		negCache: make(map[string]time.Time),
		done:     make(chan struct{}),
	}
	return c
}

func TestJWKSCache_FetchAndLookup_RSA(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	keys, err := cache.GetKey("key-1")
	if err != nil {
		t.Fatalf("GetKey failed: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].N.Cmp(testRSAKey.PublicKey.N) != 0 {
		t.Error("returned key modulus does not match original")
	}
	if keys[0].E != testRSAKey.PublicKey.E {
		t.Error("returned key exponent does not match original")
	}
}

func TestJWKSCache_KeyMiss_TriggersRefresh(t *testing.T) {
	jk1 := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	jk2 := rsaKeyToJWK(&testRSAKey2.PublicKey, "key-2")

	// Start server with only key-1.
	var fetchCount atomic.Int64
	var mu sync.Mutex
	currentBody := makeJWKSJSON(jk1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		mu.Lock()
		body := currentBody
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// Update server to include key-2.
	mu.Lock()
	currentBody = makeJWKSJSON(jk1, jk2)
	mu.Unlock()

	// Request key-2 — should trigger on-demand refresh.
	keys, err := cache.GetKey("key-2")
	if err != nil {
		t.Fatalf("GetKey(key-2) failed: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].N.Cmp(testRSAKey2.PublicKey.N) != 0 {
		t.Error("returned key-2 modulus does not match")
	}
	// At least 2 fetches: initial + on-demand.
	if fetchCount.Load() < 2 {
		t.Errorf("expected at least 2 fetches, got %d", fetchCount.Load())
	}
}

func TestJWKSCache_KeyMiss_Cooldown(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	var fetchCount atomic.Int64
	srv := newTestJWKSServer(makeJWKSJSON(jk), &fetchCount)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// First miss triggers refresh.
	_, err := cache.GetKey("unknown-kid")
	if err == nil {
		t.Fatal("expected error for unknown kid")
	}

	countAfterFirst := fetchCount.Load()

	// Second miss within cooldown should NOT trigger refresh.
	_, err = cache.GetKey("another-unknown-kid")
	if err == nil {
		t.Fatal("expected error for another unknown kid")
	}
	if fetchCount.Load() != countAfterFirst {
		t.Errorf("expected no additional fetch during cooldown, got %d total", fetchCount.Load())
	}
}

func TestJWKSCache_NegativeCache(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	var fetchCount atomic.Int64
	srv := newTestJWKSServer(makeJWKSJSON(jk), &fetchCount)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// First lookup of unknown kid triggers refresh and adds to negative cache.
	_, err := cache.GetKey("bad-kid")
	if err == nil {
		t.Fatal("expected error for unknown kid")
	}

	countAfterRefresh := fetchCount.Load()

	// Reset cooldown so we can test negative cache independently.
	cache.mu.Lock()
	cache.lastOnDemand = time.Time{}
	cache.mu.Unlock()

	// Second lookup of SAME kid should hit negative cache — no refresh.
	_, err = cache.GetKey("bad-kid")
	if err == nil {
		t.Fatal("expected error for negative-cached kid")
	}
	if !strings.Contains(err.Error(), "negative cached") {
		t.Errorf("expected negative cache error, got: %v", err)
	}
	if fetchCount.Load() != countAfterRefresh {
		t.Errorf("negative cache did not prevent refresh: fetches went from %d to %d",
			countAfterRefresh, fetchCount.Load())
	}
}

func TestJWKSCache_KidStormProtection(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	var fetchCount atomic.Int64
	srv := newTestJWKSServer(makeJWKSJSON(jk), &fetchCount)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}
	initialFetches := fetchCount.Load()

	// Fire 100 concurrent requests with unique kids.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.GetKey(fmt.Sprintf("storm-kid-%d", i))
		}(i)
	}
	wg.Wait()

	// Singleflight + cooldown should result in exactly 1 additional fetch.
	additionalFetches := fetchCount.Load() - initialFetches
	if additionalFetches != 1 {
		t.Errorf("expected exactly 1 additional fetch during kid storm (singleflight), got %d", additionalFetches)
	}
}

func TestJWKSCache_EmptyJWKS_RetainsCache(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// Verify key-1 is cached.
	keys, err := cache.GetKey("key-1")
	if err != nil {
		t.Fatalf("GetKey failed before empty response: %v", err)
	}
	if len(keys) != 1 {
		t.Fatal("expected 1 key before empty response")
	}

	// Switch server to return empty keys.
	srv.Close()
	emptySrv := newTestJWKSServer(`{"keys":[]}`, nil)
	defer emptySrv.Close()
	cache.endpoint = emptySrv.URL

	// Fetch should succeed but not evict cache.
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("fetch with empty response failed: %v", err)
	}

	// Key should still be available.
	keys, err = cache.GetKey("key-1")
	if err != nil {
		t.Fatalf("GetKey failed after empty response: %v", err)
	}
	if len(keys) != 1 {
		t.Error("cache was evicted by empty JWKS response")
	}
}

func TestJWKSCache_MalformedJSON_RetainsCache(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// Switch server to return garbage.
	srv.Close()
	garbageSrv := newTestJWKSServer("not-json{{{", nil)
	defer garbageSrv.Close()
	cache.endpoint = garbageSrv.URL

	// Fetch should fail.
	if err := cache.fetch(t.Context()); err == nil {
		t.Fatal("expected error for malformed JSON")
	}

	// Key should still be available.
	keys, err := cache.GetKey("key-1")
	if err != nil {
		t.Fatalf("GetKey failed after malformed response: %v", err)
	}
	if len(keys) != 1 {
		t.Error("cache was evicted by malformed response")
	}
}

func TestJWKSCache_OversizedBody_Rejected(t *testing.T) {
	// Create a response larger than 64KB.
	bigBody := `{"keys":[` + strings.Repeat(`{"kty":"RSA","kid":"x","n":"`, 1) + strings.Repeat("A", 70000) + `"}]}` //nolint:goconst
	srv := newTestJWKSServer(bigBody, nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	err := cache.fetch(t.Context())
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
}

func TestJWKSCache_WeakRSAKey_Rejected(t *testing.T) {
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // Intentionally weak key for testing rejection
	if err != nil {
		t.Fatalf("failed to generate 1024-bit key: %v", err)
	}

	jk := rsaKeyToJWK(&weakKey.PublicKey, "weak-key")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("fetch should succeed (skips weak key): %v", err)
	}

	// Fetch should succeed but not cache the weak key. Cache should remain empty
	// since there were no valid keys → the fetch logs a warning and retains empty cache.
	cache.mu.RLock()
	keyCount := len(cache.keys)
	cache.mu.RUnlock()
	if keyCount != 0 {
		t.Errorf("expected 0 cached keys (weak key rejected), got %d", keyCount)
	}
}

func TestJWKSCache_MaxKeysEnforced(t *testing.T) {
	var keys []jwkKey
	for i := 0; i < 15; i++ {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("failed to generate key %d: %v", i, err)
		}
		keys = append(keys, rsaKeyToJWK(&key.PublicKey, fmt.Sprintf("key-%d", i)))
	}

	srv := newTestJWKSServer(makeJWKSJSON(keys...), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	cache.mu.RLock()
	cachedCount := len(cache.keys)
	cache.mu.RUnlock()
	if cachedCount > jwksMaxKeys {
		t.Errorf("cached %d keys, expected max %d", cachedCount, jwksMaxKeys)
	}
}

func TestJWKSCache_ConcurrentAccess(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// Hammer GetKey from multiple goroutines.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				cache.GetKey("key-1")
			}
		}()
	}
	wg.Wait()
}

func TestJWKSCache_Stop_HaltsBackground(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	var fetchCount atomic.Int64
	srv := newTestJWKSServer(makeJWKSJSON(jk), &fetchCount)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	go cache.refreshLoop(50 * time.Millisecond)

	// Let it tick a few times.
	time.Sleep(200 * time.Millisecond)
	cache.Stop()

	// Allow any in-flight fetch to finish before snapshotting.
	time.Sleep(100 * time.Millisecond)
	countAfterStop := fetchCount.Load()

	// Wait and verify no more fetches.
	time.Sleep(300 * time.Millisecond)
	if fetchCount.Load() != countAfterStop {
		t.Errorf("background refresh continued after Stop(): %d → %d",
			countAfterStop, fetchCount.Load())
	}
}

func TestJWKSCache_KidAbsent_TriesAllKeys(t *testing.T) {
	jk1 := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	jk2 := rsaKeyToJWK(&testRSAKey2.PublicKey, "key-2")
	srv := newTestJWKSServer(makeJWKSJSON(jk1, jk2), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	// Empty kid should return all keys.
	keys, err := cache.GetKey("")
	if err != nil {
		t.Fatalf("GetKey('') failed: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys for empty kid, got %d", len(keys))
	}
}

func TestJWKSCache_HTTPError_RetainsCache(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// Switch to a server that returns 500.
	srv.Close()
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	cache.endpoint = errSrv.URL

	// Fetch should fail.
	if err := cache.fetch(t.Context()); err == nil {
		t.Fatal("expected error for HTTP 500")
	}

	// Cache should be preserved.
	keys, err := cache.GetKey("key-1")
	if err != nil {
		t.Fatalf("GetKey failed after HTTP error: %v", err)
	}
	if len(keys) != 1 {
		t.Error("cache was evicted by HTTP error")
	}
}

func TestJWKSCache_Base64RawURL_Decoding(t *testing.T) {
	// Verify that RawURLEncoding (no padding) is used correctly by round-tripping.
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "base64-test")

	// The encoded values should NOT contain padding '=' characters.
	if strings.Contains(jk.N, "=") {
		t.Error("encoded modulus contains padding (should use RawURLEncoding)")
	}
	if strings.Contains(jk.E, "=") {
		t.Error("encoded exponent contains padding (should use RawURLEncoding)")
	}

	// Parse and verify the round-trip produces the correct key.
	parsed, err := parseJWK(jk)
	if err != nil {
		t.Fatalf("parseJWK failed: %v", err)
	}
	if parsed.N.Cmp(testRSAKey.PublicKey.N) != 0 {
		t.Error("round-tripped modulus does not match")
	}
	if parsed.E != testRSAKey.PublicKey.E {
		t.Error("round-tripped exponent does not match")
	}
}

func TestJWKSCache_MaxStaleness(t *testing.T) {
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "key-1")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	defer srv.Close()

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}

	// Artificially set lastFetch to >24h ago.
	cache.mu.Lock()
	cache.lastFetch = time.Now().Add(-25 * time.Hour)
	cache.mu.Unlock()

	// GetKey should detect staleness, attempt refresh. Since server is still up,
	// refresh succeeds and we get the key.
	keys, err := cache.GetKey("key-1")
	if err != nil {
		t.Fatalf("GetKey failed (server up, should refresh): %v", err)
	}
	if len(keys) != 1 {
		t.Fatal("expected 1 key after staleness refresh")
	}

	// Now make server unreachable and set lastFetch old again.
	srv.Close()
	cache.mu.Lock()
	cache.lastFetch = time.Now().Add(-25 * time.Hour)
	cache.mu.Unlock()

	// GetKey should fail due to staleness + unreachable server.
	_, err = cache.GetKey("key-1")
	if err == nil {
		t.Fatal("expected error for stale cache with unreachable server")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected staleness error, got: %v", err)
	}
}

func TestParseJWK_UnsupportedKeyType(t *testing.T) {
	jk := jwkKey{Kty: "EC", Kid: "ec-key", Alg: "ES256"}
	_, err := parseJWK(jk)
	if err == nil {
		t.Fatal("expected error for EC key type")
	}
	if !strings.Contains(err.Error(), "unsupported key type") {
		t.Errorf("expected key type error, got: %v", err)
	}
}

func TestParseJWK_UnsupportedAlgorithm(t *testing.T) {
	jk := jwkKey{Kty: "RSA", Kid: "rs384-key", Alg: "RS384", N: "AA", E: "AQAB"}
	_, err := parseJWK(jk)
	if err == nil {
		t.Fatal("expected error for RS384 algorithm")
	}
	if !strings.Contains(err.Error(), "unsupported algorithm") {
		t.Errorf("expected algorithm error, got: %v", err)
	}
}

func TestParseJWK_EmptyAlgorithm_Accepted(t *testing.T) {
	// Some JWKS providers omit alg — should be accepted for RSA keys.
	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "no-alg")
	jk.Alg = "" // Clear the algorithm field.

	parsed, err := parseJWK(jk)
	if err != nil {
		t.Fatalf("parseJWK failed for empty alg: %v", err)
	}
	if parsed.N.Cmp(testRSAKey.PublicKey.N) != 0 {
		t.Error("parsed key does not match")
	}
}

func TestJWKSCache_NewJWKSCache_InitialFetchFailure(t *testing.T) {
	// Point at a non-existent server — should not panic, just log warning.
	cache := NewJWKSCache("http://127.0.0.1:1/nonexistent", nil, slog.Default())
	defer cache.Stop()

	// Cache should exist but be empty.
	cache.mu.RLock()
	keyCount := len(cache.keys)
	cache.mu.RUnlock()
	if keyCount != 0 {
		t.Errorf("expected empty cache on failed init, got %d keys", keyCount)
	}
}
