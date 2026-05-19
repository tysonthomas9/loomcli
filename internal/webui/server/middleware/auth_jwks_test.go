package middleware

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWKSCacheFetchAndLookup(t *testing.T) {
	key := mustRSAKey(t)
	body := `{"keys":[` + jwkJSON("kid-1", &key.PublicKey) + `]}`
	cache := NewJWKSCacheNoFetch("https://auth.example/jwks", fakeHTTPClient(body, http.StatusOK, nil), slog.Default())
	if err := cache.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	keys, err := cache.GetKey("kid-1")
	if err != nil {
		t.Fatalf("GetKey(kid-1) error = %v", err)
	}
	if len(keys) != 1 || keys[0].N.Cmp(key.N) != 0 {
		t.Fatalf("GetKey(kid-1) = %#v, want generated key", keys)
	}
	all, err := cache.GetKey("")
	if err != nil {
		t.Fatalf("GetKey(empty) error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("GetKey(empty) len = %d, want 1", len(all))
	}
}

func TestJWKSCacheOnDemandRefreshCooldownAndNegativeCache(t *testing.T) {
	key1 := mustRSAKey(t)
	key2 := mustRSAKey(t)
	responses := []string{
		`{"keys":[` + jwkJSON("kid-1", &key1.PublicKey) + `]}`,
		`{"keys":[` + jwkJSON("kid-2", &key2.PublicKey) + `]}`,
		`{"keys":[` + jwkJSON("kid-2", &key2.PublicKey) + `]}`,
	}
	calls := 0
	cache := NewJWKSCacheNoFetch("https://auth.example/jwks", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := responses[min(calls, len(responses)-1)]
		calls++
		return response(body, http.StatusOK), nil
	})}, slog.Default())
	if err := cache.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if _, err := cache.GetKey("kid-2"); err != nil {
		t.Fatalf("GetKey(kid-2) after on-demand refresh error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("transport calls = %d, want 2", calls)
	}
	if _, err := cache.GetKey("missing"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("GetKey during cooldown error = %v, want cooldown", err)
	}
	cache.mu.Lock()
	cache.lastOnDemand = time.Now().Add(-jwksOnDemandCooldown)
	cache.negCache["missing"] = time.Now().Add(time.Hour)
	cache.mu.Unlock()
	if _, err := cache.GetKey("missing"); err == nil || !strings.Contains(err.Error(), "negative cached") {
		t.Fatalf("GetKey negative cache error = %v", err)
	}
}

func TestJWKSCacheFetchErrorsAndRetainedKeys(t *testing.T) {
	key := mustRSAKey(t)
	tests := []struct {
		name   string
		body   string
		status int
		err    error
	}{
		{name: "client error", err: errors.New("dial failed")},
		{name: "status", status: http.StatusTeapot, body: `{}`},
		{name: "too large", status: http.StatusOK, body: strings.Repeat("x", jwksMaxResponseBytes+1)},
		{name: "bad json", status: http.StatusOK, body: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewJWKSCacheNoFetch("https://auth.example/jwks", fakeHTTPClient(tt.body, tt.status, tt.err), slog.Default())
			if err := cache.Fetch(context.Background()); err == nil {
				t.Fatal("Fetch() error = nil")
			}
		})
	}

	cache := NewJWKSCacheNoFetch("https://auth.example/jwks", fakeHTTPClient(`{"keys":[]}`, http.StatusOK, nil), slog.Default())
	cache.keys["kid-1"] = &key.PublicKey
	cache.allKeys = []*rsa.PublicKey{&key.PublicKey}
	if err := cache.Fetch(context.Background()); err != nil {
		t.Fatalf("empty Fetch() error = %v", err)
	}
	keys, err := cache.GetKey("kid-1")
	if err != nil || len(keys) != 1 {
		t.Fatalf("retained key lookup = %v/%v", keys, err)
	}

	invalidOnly := NewJWKSCacheNoFetch("https://auth.example/jwks", fakeHTTPClient(`{"keys":[{"kty":"EC","kid":"bad"}]}`, http.StatusOK, nil), slog.Default())
	if err := invalidOnly.Fetch(context.Background()); err != nil {
		t.Fatalf("invalid-only Fetch() should retain empty cache without error, got %v", err)
	}
	if _, err := invalidOnly.GetKey(""); err == nil {
		t.Fatal("GetKey(empty) on empty cache error = nil")
	}
}

func TestJWKSCacheRejectsExpiredStaleCache(t *testing.T) {
	key := mustRSAKey(t)
	cache := NewJWKSCacheNoFetch("https://auth.example/jwks", fakeHTTPClient("", 0, errors.New("offline")), slog.Default())
	cache.keys["kid-1"] = &key.PublicKey
	cache.allKeys = []*rsa.PublicKey{&key.PublicKey}
	cache.lastFetch = time.Now().Add(-(jwksMaxStaleness + time.Hour))
	if _, err := cache.GetKey("kid-1"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("GetKey stale error = %v, want expired", err)
	}
}

func TestParseJWKValidation(t *testing.T) {
	key := mustRSAKey(t)
	valid := jwkFromKey("kid-1", &key.PublicKey)
	if parsed, err := parseJWK(valid); err != nil || parsed.N.Cmp(key.N) != 0 {
		t.Fatalf("parseJWK(valid) = %#v/%v", parsed, err)
	}
	for _, tt := range []struct {
		name string
		key  jwkKey
	}{
		{name: "wrong type", key: jwkKey{Kty: "EC", Alg: "RS256"}},
		{name: "wrong algorithm", key: jwkKey{Kty: "RSA", Alg: "HS256"}},
		{name: "bad modulus", key: jwkKey{Kty: "RSA", N: "%", E: valid.E}},
		{name: "bad exponent", key: jwkKey{Kty: "RSA", N: valid.N, E: "%"}},
		{name: "small key", key: jwkKey{Kty: "RSA", N: base64.RawURLEncoding.EncodeToString(big.NewInt(3).Bytes()), E: valid.E}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseJWK(tt.key); err == nil {
				t.Fatal("parseJWK() error = nil")
			}
		})
	}
}

func TestAuthMiddlewareValidatesJWTAndPublicRoutes(t *testing.T) {
	key := mustRSAKey(t)
	cache := cacheWithKey("kid-1", &key.PublicKey)
	mw := Auth(AuthConfig{JWKSCache: cache, Issuer: "issuer", Audience: "aud"})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := UserIdentityFromContext(r.Context())
		if r.URL.Path == "/api/private" {
			if !ok || identity.UserID != "user-1" || identity.Email != "u@example.com" || identity.Name != "User One" {
				t.Fatalf("identity = %#v/%v", identity, ok)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	req.Header.Set("Authorization", "Bearer "+signedJWT(t, key, "kid-1", "issuer", "aud", jwt.SigningMethodRS256))
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid token status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("public auth route status = %d", rec.Code)
	}
}

func TestAuthMiddlewareRejectsBadRequests(t *testing.T) {
	key := mustRSAKey(t)
	cache := cacheWithKey("kid-1", &key.PublicKey)
	mw := Auth(AuthConfig{JWKSCache: cache, Issuer: "issuer", Audience: "aud"})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next should not be called")
	})

	for _, tt := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "too large", header: "Bearer " + strings.Repeat("x", extAuthMaxTokenSize+1), want: http.StatusBadRequest},
		{name: "malformed", header: "Bearer not-a-jwt", want: http.StatusBadRequest},
		{name: "bad algorithm", header: "Bearer " + signedJWT(t, key, "kid-1", "issuer", "aud", jwt.SigningMethodRS512), want: http.StatusUnauthorized},
		{name: "missing subject", header: "Bearer " + signedJWTWithSubject(t, key, "kid-1", "issuer", "aud", ""), want: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/private", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func TestAuthHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "bEaReR token")
	if got := extractBearerToken(req); got != "token" {
		t.Fatalf("extractBearerToken = %q", got)
	}
	if got := extractJWTKid("bad"); got != "" {
		t.Fatalf("extractJWTKid(bad) = %q", got)
	}
	if got := classifyJWTError(jwt.ErrTokenMalformed); got != "malformed" {
		t.Fatalf("classifyJWTError malformed = %q", got)
	}
	if got := (UserIdentity{UserID: "user-1", Email: "secret@example.com", Name: "Secret"}).String(); strings.Contains(got, "secret") {
		t.Fatalf("UserIdentity.String leaked PII: %s", got)
	}
	ctx := WithUserIdentity(context.Background(), UserIdentity{UserID: "user-2"})
	if identity, ok := UserIdentityFromContext(ctx); !ok || identity.UserID != "user-2" {
		t.Fatalf("UserIdentityFromContext = %#v/%v", identity, ok)
	}
}

func TestAuthOpenModePassesThrough(t *testing.T) {
	called := false
	Auth(AuthConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/private", nil))
	if !called {
		t.Fatal("open-mode auth did not call next")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func fakeHTTPClient(body string, status int, err error) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err != nil {
			return nil, err
		}
		return response(body, status), nil
	})}
}

func response(body string, status int) *http.Response {
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, jwksMinRSABits)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

func jwkFromKey(kid string, key *rsa.PublicKey) jwkKey {
	return jwkKey{
		Kty: "RSA",
		Kid: kid,
		Alg: "RS256",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func jwkJSON(kid string, key *rsa.PublicKey) string {
	jwk := jwkFromKey(kid, key)
	return `{"kty":"` + jwk.Kty + `","kid":"` + jwk.Kid + `","alg":"` + jwk.Alg + `","use":"` + jwk.Use + `","n":"` + jwk.N + `","e":"` + jwk.E + `"}`
}

func cacheWithKey(kid string, key *rsa.PublicKey) *JWKSCache {
	cache := NewJWKSCacheNoFetch("https://auth.example/jwks", nil, slog.Default())
	cache.keys[kid] = key
	cache.allKeys = []*rsa.PublicKey{key}
	cache.lastFetch = time.Now()
	return cache
}

func signedJWT(t *testing.T, key *rsa.PrivateKey, kid, issuer, audience string, method jwt.SigningMethod) string {
	t.Helper()
	return signedJWTWithSubjectAndMethod(t, key, kid, issuer, audience, "user-1", method)
}

func signedJWTWithSubject(t *testing.T, key *rsa.PrivateKey, kid, issuer, audience, subject string) string {
	t.Helper()
	return signedJWTWithSubjectAndMethod(t, key, kid, issuer, audience, subject, jwt.SigningMethodRS256)
}

func signedJWTWithSubjectAndMethod(t *testing.T, key *rsa.PrivateKey, kid, issuer, audience, subject string, method jwt.SigningMethod) string {
	t.Helper()
	token := jwt.NewWithClaims(method, extAuthClaims{
		Email: "u@example.com",
		Name:  "User One",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    issuer,
			Audience:  []string{audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}
