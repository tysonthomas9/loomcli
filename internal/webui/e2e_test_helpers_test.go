package webui

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

// testJWKKey represents a single JWK entry for test JWKS responses.
type testJWKKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// rsaKeyToJWK converts an RSA public key to a testJWKKey for test JWKS responses.
func rsaKeyToJWK(pub *rsa.PublicKey, kid string) testJWKKey {
	return testJWKKey{
		Kty: "RSA",
		Kid: kid,
		Alg: "RS256",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// makeJWKSJSON builds a JWKS JSON string from the given keys.
func makeJWKSJSON(keys ...testJWKKey) string {
	resp := struct {
		Keys []testJWKKey `json:"keys"`
	}{Keys: keys}
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

// extAuthClaims mirrors the middleware package's extAuthClaims type for e2e tests.
type extAuthClaims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	jwt.RegisteredClaims
}

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
