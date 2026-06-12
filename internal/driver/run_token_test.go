package driver

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func testRunTokenKey(t *testing.T, fill byte) []byte {
	t.Helper()
	key := make([]byte, runTokenKeyLen)
	for i := range key {
		key[i] = fill
	}
	return key
}

func testRunTokenClaims() RunTokenClaims {
	return RunTokenClaims{
		WorkspaceKey: "ws-1",
		RunID:        "run-42",
		NodeID:       "node-a",
		LeaseID:      "lease-7",
		FencingToken: 9,
	}
}

func TestMintParseRunTokenRoundTrip(t *testing.T) {
	key := testRunTokenKey(t, 0x11)
	minted, err := MintRunToken(testRunTokenClaims(), key, time.Minute)
	if err != nil {
		t.Fatalf("MintRunToken: %v", err)
	}

	claims, err := ParseRunToken(minted, key)
	if err != nil {
		t.Fatalf("ParseRunToken: %v", err)
	}
	if claims.WorkspaceKey != "ws-1" {
		t.Errorf("WorkspaceKey = %q, want %q", claims.WorkspaceKey, "ws-1")
	}
	if claims.RunID != "run-42" {
		t.Errorf("RunID = %q, want %q", claims.RunID, "run-42")
	}
	if claims.NodeID != "node-a" {
		t.Errorf("NodeID = %q, want %q", claims.NodeID, "node-a")
	}
	if claims.LeaseID != "lease-7" {
		t.Errorf("LeaseID = %q, want %q", claims.LeaseID, "lease-7")
	}
	if claims.FencingToken != 9 {
		t.Errorf("FencingToken = %d, want 9", claims.FencingToken)
	}
	if want := DriverRunActor("run-42"); claims.Subject != want {
		t.Errorf("Subject = %q, want %q", claims.Subject, want)
	}
	if len(claims.Caps) != 0 {
		t.Errorf("Caps = %v, want empty (reserved claim)", claims.Caps)
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatalf("IssuedAt/ExpiresAt = %v/%v, want both set", claims.IssuedAt, claims.ExpiresAt)
	}
	gotTTL := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	if gotTTL != time.Minute {
		t.Errorf("ExpiresAt-IssuedAt = %s, want %s", gotTTL, time.Minute)
	}
}

func TestMintRunTokenRejectsInvalidInput(t *testing.T) {
	key := testRunTokenKey(t, 0x11)
	tests := []struct {
		name   string
		claims RunTokenClaims
		key    []byte
		ttl    time.Duration
	}{
		{"empty run id", RunTokenClaims{WorkspaceKey: "ws-1"}, key, time.Minute},
		{"blank run id", RunTokenClaims{RunID: "   "}, key, time.Minute},
		{"empty key", testRunTokenClaims(), nil, time.Minute},
		{"zero ttl", testRunTokenClaims(), key, 0},
		{"negative ttl", testRunTokenClaims(), key, -time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MintRunToken(tt.claims, tt.key, tt.ttl)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("MintRunToken error = %v, want domain.ErrInvalid", err)
			}
		})
	}
}

// signTestRunToken builds tokens outside MintRunToken so rejection paths
// (expired, missing exp, alg confusion, subject mismatch) can be exercised.
func signTestRunToken(t *testing.T, method jwt.SigningMethod, signingKey any, claims RunTokenClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(method, claims).SignedString(signingKey)
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return signed
}

func TestParseRunTokenRejections(t *testing.T) {
	key := testRunTokenKey(t, 0x11)
	otherKey := testRunTokenKey(t, 0x22)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	now := time.Now()
	live := func(c RunTokenClaims) RunTokenClaims {
		c.Subject = DriverRunActor(c.RunID)
		c.IssuedAt = jwt.NewNumericDate(now)
		c.ExpiresAt = jwt.NewNumericDate(now.Add(time.Minute))
		return c
	}

	expired := live(testRunTokenClaims())
	expired.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute))

	noExpiry := live(testRunTokenClaims())
	noExpiry.ExpiresAt = nil

	badSubject := live(testRunTokenClaims())
	badSubject.Subject = DriverRunActor("someone-else")

	emptyRunID := live(testRunTokenClaims())
	emptyRunID.RunID = ""

	tests := []struct {
		name  string
		token string
	}{
		{"expired", signTestRunToken(t, jwt.SigningMethodHS256, key, expired)},
		{"missing expiry", signTestRunToken(t, jwt.SigningMethodHS256, key, noExpiry)},
		{"wrong key", signTestRunToken(t, jwt.SigningMethodHS256, otherKey, live(testRunTokenClaims()))},
		{"alg none", signTestRunToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, live(testRunTokenClaims()))},
		{"alg rs256", signTestRunToken(t, jwt.SigningMethodRS256, rsaKey, live(testRunTokenClaims()))},
		{"subject mismatch", signTestRunToken(t, jwt.SigningMethodHS256, key, badSubject)},
		{"empty run id claim", signTestRunToken(t, jwt.SigningMethodHS256, key, emptyRunID)},
		{"garbage", "not.a.jwt"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, parseErr := ParseRunToken(tt.token, key)
			if claims != nil || parseErr == nil {
				t.Fatalf("ParseRunToken = (%+v, %v), want (nil, error)", claims, parseErr)
			}
			if !errors.Is(parseErr, ErrRunTokenInvalid) {
				t.Errorf("error = %v, want ErrRunTokenInvalid", parseErr)
			}
			if !errors.Is(parseErr, domain.ErrNotOwner) {
				t.Errorf("error = %v, want to wrap domain.ErrNotOwner", parseErr)
			}
		})
	}
}

func TestParseRunTokenRequiresKey(t *testing.T) {
	_, err := ParseRunToken("whatever", nil)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("ParseRunToken with empty key error = %v, want domain.ErrInvalid", err)
	}
}

func TestResolveRunTokenSigningKeyFromEnv(t *testing.T) {
	want := testRunTokenKey(t, 0x33)
	tests := []struct {
		name    string
		value   string
		want    []byte
		wantErr bool
	}{
		{"valid hex", hex.EncodeToString(want), want, false},
		{"bad hex", "zz" + strings.Repeat("00", runTokenKeyLen-1), nil, true},
		{"too short", strings.Repeat("ab", runTokenKeyLen-1), nil, true},
		{"too long", strings.Repeat("ab", runTokenKeyLen+1), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(RunTokenSigningKeyEnv, tt.value)
			key, err := ResolveRunTokenSigningKey()
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Errorf("error = %v, want domain.ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRunTokenSigningKey: %v", err)
			}
			if string(key) != string(tt.want) {
				t.Errorf("key = %x, want %x", key, tt.want)
			}
		})
	}
}

func TestResolveRunTokenSigningKeyEphemeral(t *testing.T) {
	t.Setenv(RunTokenSigningKeyEnv, "")

	first, err := ResolveRunTokenSigningKey()
	if err != nil {
		t.Fatalf("ResolveRunTokenSigningKey: %v", err)
	}
	if len(first) != runTokenKeyLen {
		t.Fatalf("ephemeral key length = %d, want %d", len(first), runTokenKeyLen)
	}
	second, err := ResolveRunTokenSigningKey()
	if err != nil {
		t.Fatalf("ResolveRunTokenSigningKey (second): %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("ephemeral key not stable across calls within process")
	}

	minted, err := MintRunToken(testRunTokenClaims(), first, time.Minute)
	if err != nil {
		t.Fatalf("MintRunToken with ephemeral key: %v", err)
	}
	claims, err := ParseRunToken(minted, second)
	if err != nil {
		t.Fatalf("ParseRunToken with ephemeral key: %v", err)
	}
	if claims.RunID != "run-42" {
		t.Errorf("RunID = %q, want %q", claims.RunID, "run-42")
	}
}

func TestRunTokenTTL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"unset uses default", "", DefaultRunTokenTTL, false},
		{"whitespace uses default", "   ", DefaultRunTokenTTL, false},
		{"explicit override", "2h30m", 2*time.Hour + 30*time.Minute, false},
		{"garbage", "soon", 0, true},
		{"zero", "0s", 0, true},
		{"negative", "-5m", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(RunTokenTTLEnv, tt.value)
			ttl, err := RunTokenTTL()
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Errorf("error = %v, want domain.ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunTokenTTL: %v", err)
			}
			if ttl != tt.want {
				t.Errorf("ttl = %s, want %s", ttl, tt.want)
			}
		})
	}
}
