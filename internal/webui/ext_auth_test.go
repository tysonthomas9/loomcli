package webui

import (
	"context"
	"crypto"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signExtAuthJWT creates a signed JWT with the given claims, key, kid, and signing method.
func signExtAuthJWT(t *testing.T, claims jwt.Claims, key interface{}, kid string, method jwt.SigningMethod) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign JWT: %v", err)
	}
	return signed
}

// validExtAuthClaims returns standard valid claims for ext auth testing.
func validExtAuthClaims(issuer, audience string) extAuthClaims {
	now := time.Now()
	return extAuthClaims{
		Email: "alice@example.com",
		Name:  "Alice",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-abc123",
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now.Add(-1 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
}

// extAuthTestSetup creates a JWKS server, cache, and middleware for testing.
type extAuthTestSetup struct {
	Server     *httptest.Server
	Cache      *JWKSCache
	Middleware func(http.Handler) http.Handler
	Issuer     string
	Audience   string
}

func newExtAuthTestSetup(t *testing.T) *extAuthTestSetup {
	t.Helper()
	issuer := "https://auth.example.com"
	audience := "loom"

	jk := rsaKeyToJWK(&testRSAKey.PublicKey, "test-kid-1")
	srv := newTestJWKSServer(makeJWKSJSON(jk), nil)
	t.Cleanup(srv.Close)

	cache := newTestJWKSCache(t, srv.URL)
	if err := cache.fetch(t.Context()); err != nil {
		t.Fatalf("initial JWKS fetch failed: %v", err)
	}

	mw := NewExtAuthMiddleware(ExtAuthConfig{
		JWKSCache: cache,
		Issuer:    issuer,
		Audience:  audience,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	return &extAuthTestSetup{
		Server:     srv,
		Cache:      cache,
		Middleware: mw,
		Issuer:     issuer,
		Audience:   audience,
	}
}

// okHandler is a test handler that writes 200 OK.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func TestExtAuth_Passthrough_WhenNilCache(t *testing.T) {
	mw := NewExtAuthMiddleware(ExtAuthConfig{JWKSCache: nil})
	handler := mw(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestExtAuth_ValidJWT_InjectsIdentity(t *testing.T) {
	setup := newExtAuthTestSetup(t)

	var captured UserIdentity
	var capturedOK bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, capturedOK = UserIdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := setup.Middleware(inner)

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !capturedOK {
		t.Fatal("expected UserIdentity in context")
	}
	if captured.UserID != "user-abc123" {
		t.Errorf("UserID = %q, want %q", captured.UserID, "user-abc123")
	}
	if captured.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", captured.Email, "alice@example.com")
	}
	if captured.Name != "Alice" {
		t.Errorf("Name = %q, want %q", captured.Name, "Alice")
	}
}

func TestExtAuth_ExpiredJWT_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-30 * time.Second))
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_WrongKey_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	// Sign with testRSAKey2 but JWKS only has testRSAKey
	token := signExtAuthJWT(t, claims, testRSAKey2, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_WrongIssuer_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims("https://wrong-issuer.com", setup.Audience)
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_WrongAudience_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, "wrong-audience")
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_MissingSub_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	claims.Subject = "" // missing sub
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	assertResponseContains(t, w, "invalid token claims")
}

func TestExtAuth_MissingExp_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	now := time.Now()
	claims := extAuthClaims{
		Email: "alice@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  "user-abc123",
			Issuer:   setup.Issuer,
			Audience: jwt.ClaimStrings{setup.Audience},
			IssuedAt: jwt.NewNumericDate(now),
			// No ExpiresAt — should fail WithExpirationRequired
		},
	}
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_AlgNone_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	// Create an unsigned token with alg:none
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	token.Header["kid"] = "test-kid-1"
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to sign none token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_HS256Confusion_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	// HMAC confusion attack: sign with the RSA public key bytes as HMAC secret
	pubKeyBytes := testRSAKey.PublicKey.N.Bytes()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = "test-kid-1"
	signed, err := token.SignedString(pubKeyBytes)
	if err != nil {
		t.Fatalf("failed to sign HS256 token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// fakeRS256SigningMethod is a custom SigningMethod that returns "RS256" from Alg()
// but is a distinct type from *jwt.SigningMethodRSA. This tests pointer identity check.
type fakeRS256SigningMethod struct{}

func (f *fakeRS256SigningMethod) Verify(signingString string, sig []byte, key interface{}) error {
	return nil // always "valid"
}

func (f *fakeRS256SigningMethod) Sign(signingString string, key interface{}) ([]byte, error) {
	// Sign with the real RS256 key for a valid-looking signature
	return jwt.SigningMethodRS256.Sign(signingString, key)
}

func (f *fakeRS256SigningMethod) Alg() string {
	return "RS256" // same string as real RS256
}

// NOTE: This test mutates the global jwt signing method registry. Do NOT use
// t.Parallel() on this test or any test that parses RS256 tokens in this package.
func TestExtAuth_CustomSigningMethodSpoofing_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	fake := &fakeRS256SigningMethod{}

	// Sign the token using the fake method (which delegates to real RS256 for signing).
	token := jwt.NewWithClaims(fake, claims)
	token.Header["kid"] = "test-kid-1"
	signed, err := token.SignedString(testRSAKey)
	if err != nil {
		t.Fatalf("failed to sign with fake method: %v", err)
	}

	// Temporarily register the fake method so jwt-go's parser resolves it
	// instead of the real jwt.SigningMethodRS256. This simulates an attack where
	// the signing method is spoofed. The pointer identity check in the keyfunc
	// should detect that token.Method is not jwt.SigningMethodRS256.
	jwt.RegisterSigningMethod("RS256", func() jwt.SigningMethod { return fake })
	defer jwt.RegisterSigningMethod("RS256", func() jwt.SigningMethod { return jwt.SigningMethodRS256 })

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d; pointer identity check should reject custom SigningMethod", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_OversizedToken_Returns400(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	// Create a token >8192 bytes
	bigToken := strings.Repeat("a", extAuthMaxTokenSize+1)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+bigToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	assertResponseContains(t, w, "malformed token")
}

func TestExtAuth_MissingToken_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	assertResponseContains(t, w, "authentication required")
}

func TestExtAuth_OptionsPassthrough(t *testing.T) {
	setup := newExtAuthTestSetup(t)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := setup.Middleware(inner)

	req := httptest.NewRequest(http.MethodOptions, "/api/issues", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("inner handler was not called for OPTIONS request")
	}
}

func TestExtAuth_KidAbsentFallback(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	// Sign without kid header
	token := signExtAuthJWT(t, claims, testRSAKey, "", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; kid-absent should try all cached keys", w.Code, http.StatusOK)
	}
}

func TestExtAuth_ClockSkewWithinLeeway_Accepted(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	// Expired 3 seconds ago — within 5s leeway
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-3 * time.Second))
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; token within 5s leeway should be accepted", w.Code, http.StatusOK)
	}
}

func TestExtAuth_ClockSkewBeyondLeeway_Rejected(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	// Expired 7 seconds ago — beyond 5s leeway
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-7 * time.Second))
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d; token beyond 5s leeway should be rejected", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_PublicRoutePassthrough(t *testing.T) {
	setup := newExtAuthTestSetup(t)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := setup.Middleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("inner handler was not called for public route")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestExtAuth_CrossSystem_FleetJWTAgainstExtAuth_Returns401(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	// Generate a valid fleet HS256 JWT
	fleetKey := []byte("fleet-signing-key-for-test-1234!")
	fleetToken, err := GenerateWorkerToken("worker-1", nil, fleetKey, time.Hour)
	if err != nil {
		t.Fatalf("failed to generate fleet token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+fleetToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d; fleet HS256 JWT should be rejected by ExtAuth", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_CrossSystem_UserJWTAgainstFleetAuth_Returns401(t *testing.T) {
	// Generate a valid RS256 user JWT
	claims := validExtAuthClaims("https://auth.example.com", "loom")
	userToken := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	// Send to FleetAuthMiddleware
	fleetKey := []byte("fleet-signing-key-for-test-1234!")
	fleetMW := NewFleetAuthMiddleware(fleetKey)
	handler := fleetMW(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d; RS256 user JWT should be rejected by fleet auth", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_UserIdentityString_RedactsPII(t *testing.T) {
	identity := UserIdentity{
		UserID: "user-123",
		Email:  "alice@example.com",
		Name:   "Alice",
	}
	s := identity.String()

	if s != `UserIdentity{UserID: "user-123"}` {
		t.Errorf("String() = %q, want %q", s, `UserIdentity{UserID: "user-123"}`)
	}
	if strings.Contains(s, "alice") || strings.Contains(s, "Alice") || strings.Contains(s, "example.com") {
		t.Errorf("String() leaks PII: %q", s)
	}
}

func TestUserIdentityFromContext_NoClaims(t *testing.T) {
	ctx := context.Background()
	identity, ok := UserIdentityFromContext(ctx)
	if ok {
		t.Error("expected ok = false for empty context")
	}
	if identity.UserID != "" {
		t.Errorf("expected zero-value UserIdentity, got UserID = %q", identity.UserID)
	}
}

func TestUserIdentityFromContext_WithClaims(t *testing.T) {
	expected := UserIdentity{UserID: "user-test", Email: "test@example.com", Name: "Test"}
	ctx := context.WithValue(context.Background(), userIdentityContextKey{}, expected)

	identity, ok := UserIdentityFromContext(ctx)
	if !ok {
		t.Fatal("expected ok = true")
	}
	if identity.UserID != "user-test" {
		t.Errorf("UserID = %q, want %q", identity.UserID, "user-test")
	}
	if identity.Email != "test@example.com" {
		t.Errorf("Email = %q, want %q", identity.Email, "test@example.com")
	}
}

func TestExtractToken_JWTGuard_RejectsJWTInQueryParam(t *testing.T) {
	// Generate a real JWT (>200 chars, contains exactly 2 dots)
	claims := validExtAuthClaims("https://auth.example.com", "loom")
	jwtToken := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	if len(jwtToken) <= 200 || strings.Count(jwtToken, ".") != 2 {
		t.Fatalf("test JWT doesn't match JWT pattern: len=%d, dots=%d", len(jwtToken), strings.Count(jwtToken, "."))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events?token="+jwtToken, nil)
	result := extractToken(req)
	if result != "" {
		t.Errorf("extractToken should reject JWT-like query param token, got %d chars", len(result))
	}
}

func TestExtractToken_JWTGuard_AllowsShortOpaqueToken(t *testing.T) {
	opaqueToken := "abc123def456"
	req := httptest.NewRequest(http.MethodGet, "/api/events?token="+opaqueToken, nil)
	result := extractToken(req)
	if result != opaqueToken {
		t.Errorf("extractToken should allow short opaque token, got %q", result)
	}
}

func TestExtractToken_JWTGuard_AllowsLongOpaqueTokenWithoutDots(t *testing.T) {
	// A long token without exactly 2 dots should pass
	opaqueToken := strings.Repeat("a", 300)
	req := httptest.NewRequest(http.MethodGet, "/api/events?token="+opaqueToken, nil)
	result := extractToken(req)
	if result != opaqueToken {
		t.Errorf("extractToken should allow long opaque token without dots, got len=%d", len(result))
	}
}

func TestExtractBearerToken_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"standard Bearer", "Bearer mytoken", "mytoken"},
		{"lowercase bearer", "bearer mytoken", "mytoken"},
		{"mixed case BEARER", "BEARER mytoken", "mytoken"},
		{"mixed case BeArEr", "BeArEr mytoken", "mytoken"},
		{"empty", "", ""},
		{"wrong prefix", "Basic dXNlcjpwYXNz", ""},
		{"too short", "Bear", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			got := extractBearerToken(req)
			if got != tt.want {
				t.Errorf("extractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestExtractJWTKid(t *testing.T) {
	claims := validExtAuthClaims("iss", "aud")

	// Token with kid
	token := signExtAuthJWT(t, claims, testRSAKey, "my-kid", jwt.SigningMethodRS256)
	kid := extractJWTKid(token)
	if kid != "my-kid" {
		t.Errorf("extractJWTKid with kid = %q, want %q", kid, "my-kid")
	}

	// Token without kid
	token2 := signExtAuthJWT(t, claims, testRSAKey, "", jwt.SigningMethodRS256)
	kid2 := extractJWTKid(token2)
	if kid2 != "" {
		t.Errorf("extractJWTKid without kid = %q, want empty", kid2)
	}

	// Garbage input
	kid3 := extractJWTKid("not-a-jwt")
	if kid3 != "" {
		t.Errorf("extractJWTKid(garbage) = %q, want empty", kid3)
	}
}

// assertResponseContains checks that the response body contains the expected substring.
func assertResponseContains(t *testing.T, w *httptest.ResponseRecorder, substr string) {
	t.Helper()
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	errMsg, ok := resp["error"]
	if !ok {
		t.Fatal("response missing 'error' field")
	}
	if !strings.Contains(errMsg, substr) {
		t.Errorf("error message %q does not contain %q", errMsg, substr)
	}
}

// Verify the fakeRS256SigningMethod satisfies the interface at compile time.
var _ jwt.SigningMethod = (*fakeRS256SigningMethod)(nil)

// Verify the fakeRS256SigningMethod satisfies the crypto.SignerOpts interface
// (needed by some signing operations).
var _ crypto.Hash = crypto.SHA256
var _ = rand.Reader // ensure crypto/rand is used

// fakeRS256ForRegistration registers the fake method so jwt-go can look it up by alg string.
func init() {
	// We need to be careful NOT to register this globally as it would interfere
	// with real RS256. Instead, the fake method is used directly in token creation.
}

func TestExtAuth_BearerOnlyNotQueryParam(t *testing.T) {
	setup := newExtAuthTestSetup(t)
	handler := setup.Middleware(okHandler())

	claims := validExtAuthClaims(setup.Issuer, setup.Audience)
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	// Send token via query param instead of Authorization header
	req := httptest.NewRequest(http.MethodGet, "/api/issues?token="+token, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d; ext auth should NOT accept query param tokens", w.Code, http.StatusUnauthorized)
	}
}

func TestExtAuth_OptionalEmailAndName(t *testing.T) {
	setup := newExtAuthTestSetup(t)

	var captured UserIdentity
	var capturedOK bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, capturedOK = UserIdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := setup.Middleware(inner)

	now := time.Now()
	claims := extAuthClaims{
		// No Email or Name
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-minimal",
			Issuer:    setup.Issuer,
			Audience:  jwt.ClaimStrings{setup.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	token := signExtAuthJWT(t, claims, testRSAKey, "test-kid-1", jwt.SigningMethodRS256)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !capturedOK {
		t.Fatal("expected UserIdentity in context")
	}
	if captured.UserID != "user-minimal" {
		t.Errorf("UserID = %q, want %q", captured.UserID, "user-minimal")
	}
	if captured.Email != "" {
		t.Errorf("Email = %q, want empty", captured.Email)
	}
	if captured.Name != "" {
		t.Errorf("Name = %q, want empty", captured.Name)
	}
}

// Verify fakeRS256SigningMethod Alg() returns RS256 for the test to be meaningful.
func TestFakeRS256_AlgReturnsRS256(t *testing.T) {
	fake := &fakeRS256SigningMethod{}
	if fake.Alg() != "RS256" {
		t.Errorf("fakeRS256.Alg() = %q, want %q", fake.Alg(), "RS256")
	}
	// Verify it's NOT the same pointer as the real RS256
	if fmt.Sprintf("%p", fake) == fmt.Sprintf("%p", jwt.SigningMethodRS256) {
		t.Error("fakeRS256 should be a different pointer than jwt.SigningMethodRS256")
	}
}
