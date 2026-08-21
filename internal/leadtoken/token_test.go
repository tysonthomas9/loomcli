package leadtoken

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

func testOccupantKey(fill byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	return key
}

func testOccupantClaims() OccupantClaims {
	return OccupantClaims{
		WorkspaceKey: "ws-1",
		PlacementID:  "node-lead-1",
		Generation:   7,
		Caps:         []string{"lead:session"},
	}
}

func TestMintParseOccupantTokenRoundTrip(t *testing.T) {
	key := testOccupantKey(0x11)
	minted, err := MintOccupantToken(testOccupantClaims(), key, time.Minute)
	if err != nil {
		t.Fatalf("MintOccupantToken: %v", err)
	}
	claims, err := ParseOccupantToken(minted, key)
	if err != nil {
		t.Fatalf("ParseOccupantToken: %v", err)
	}
	assertRoundTripClaims(t, claims)
}

func assertRoundTripClaims(t *testing.T, claims *OccupantClaims) {
	t.Helper()
	if claims.WorkspaceKey != "ws-1" || claims.PlacementID != "node-lead-1" {
		t.Fatalf("claims workspace/placement = %q/%q", claims.WorkspaceKey, claims.PlacementID)
	}
	if claims.Generation != 7 {
		t.Fatalf("Generation = %d, want 7", claims.Generation)
	}
	if claims.Subject != OccupantActor("node-lead-1") {
		t.Fatalf("Subject = %q", claims.Subject)
	}
	if !HasCap(claims, "lead:session") || HasCap(claims, "lead:admin") {
		t.Fatalf("HasCap results for caps %v are wrong", claims.Caps)
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatalf("IssuedAt/ExpiresAt = %v/%v, want both set", claims.IssuedAt, claims.ExpiresAt)
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != time.Minute {
		t.Fatalf("ttl = %s, want %s", got, time.Minute)
	}
}

func TestIsOccupantTokenExpired(t *testing.T) {
	key := testOccupantKey(0x11)
	claims := liveClaims(testOccupantClaims(), time.Now())
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	token := signTestOccupantToken(t, jwt.SigningMethodHS256, key, claims)

	_, err := ParseOccupantToken(token, key)
	if err == nil || !IsOccupantTokenExpired(err) {
		t.Fatalf("ParseOccupantToken err = %v, want expired", err)
	}
}

func TestParseOccupantTokenRejectsBadSignaturesAndAlgorithms(t *testing.T) {
	key := testOccupantKey(0x11)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	token := signTestOccupantToken(t, jwt.SigningMethodHS256, key, liveClaims(testOccupantClaims(), time.Now()))
	tests := map[string]string{
		"wrong key":       signTestOccupantToken(t, jwt.SigningMethodHS256, testOccupantKey(0x22), liveClaims(testOccupantClaims(), time.Now())),
		"alg none":        signTestOccupantToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, liveClaims(testOccupantClaims(), time.Now())),
		"alg rs256":       signTestOccupantToken(t, jwt.SigningMethodRS256, rsaKey, liveClaims(testOccupantClaims(), time.Now())),
		"tampered claims": tamperJWTClaim(t, token, "workspaceKey", "other-ws"),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			assertParseRejected(t, raw, key)
		})
	}
}

func TestParseOccupantTokenRejectsMalformedClaims(t *testing.T) {
	key := testOccupantKey(0x11)
	now := time.Now()
	tests := map[string]OccupantClaims{
		"missing expiry":       withoutExpiry(liveClaims(testOccupantClaims(), now)),
		"subject mismatch":     withSubject(liveClaims(testOccupantClaims(), now), OccupantActor("other")),
		"empty workspace":      withWorkspace(liveClaims(testOccupantClaims(), now), ""),
		"empty placement id":   withPlacement(liveClaims(testOccupantClaims(), now), ""),
		"nonpositive gen zero": withGeneration(liveClaims(testOccupantClaims(), now), 0),
		"nonpositive gen neg":  withGeneration(liveClaims(testOccupantClaims(), now), -1),
	}
	for name, claims := range tests {
		t.Run(name, func(t *testing.T) {
			assertParseRejected(t, signTestOccupantToken(t, jwt.SigningMethodHS256, key, claims), key)
		})
	}
}

func TestMintOccupantTokenRejectsInvalidInput(t *testing.T) {
	key := testOccupantKey(0x11)
	tests := map[string]struct {
		claims OccupantClaims
		key    []byte
		ttl    time.Duration
	}{
		"empty placement": {withPlacement(testOccupantClaims(), ""), key, time.Minute},
		"empty workspace": {withWorkspace(testOccupantClaims(), ""), key, time.Minute},
		"zero generation": {withGeneration(testOccupantClaims(), 0), key, time.Minute},
		"empty key":       {testOccupantClaims(), nil, time.Minute},
		"zero ttl":        {testOccupantClaims(), key, 0},
		"negative ttl":    {testOccupantClaims(), key, -time.Second},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := MintOccupantToken(tt.claims, tt.key, tt.ttl)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("MintOccupantToken err = %v, want domain.ErrInvalid", err)
			}
		})
	}
}

func TestParseOccupantTokenRequiresKey(t *testing.T) {
	_, err := ParseOccupantToken("whatever", nil)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ParseOccupantToken err = %v, want domain.ErrInvalid", err)
	}
}

func TestOccupantAndDriverRunTokensAreNotInterchangeable(t *testing.T) {
	key := testOccupantKey(0x11)
	occupantToken, err := MintOccupantToken(testOccupantClaims(), key, time.Minute)
	if err != nil {
		t.Fatalf("MintOccupantToken: %v", err)
	}
	runToken, err := driverpkg.MintRunToken(driverpkg.RunTokenClaims{
		WorkspaceKey: "ws-1",
		RunID:        "run-42",
		NodeID:       "node-a",
		LeaseID:      "lease-7",
		FencingToken: 9,
	}, key, time.Minute)
	if err != nil {
		t.Fatalf("MintRunToken: %v", err)
	}
	if claims, parseErr := driverpkg.ParseRunToken(occupantToken, key); claims != nil || !errors.Is(parseErr, driverpkg.ErrRunTokenInvalid) {
		t.Fatalf("ParseRunToken(occupant) = (%+v, %v), want ErrRunTokenInvalid", claims, parseErr)
	}
	if claims, parseErr := ParseOccupantToken(runToken, key); claims != nil || !errors.Is(parseErr, ErrOccupantTokenInvalid) {
		t.Fatalf("ParseOccupantToken(run) = (%+v, %v), want ErrOccupantTokenInvalid", claims, parseErr)
	}
}

func assertParseRejected(t *testing.T, token string, key []byte) {
	t.Helper()
	claims, err := ParseOccupantToken(token, key)
	if claims != nil || err == nil {
		t.Fatalf("ParseOccupantToken = (%+v, %v), want (nil, error)", claims, err)
	}
	if !errors.Is(err, ErrOccupantTokenInvalid) || !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("err = %v, want ErrOccupantTokenInvalid wrapping ErrNotOwner", err)
	}
}

func signTestOccupantToken(t *testing.T, method jwt.SigningMethod, key any, claims OccupantClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return signed
}

func liveClaims(claims OccupantClaims, now time.Time) OccupantClaims {
	claims.Subject = OccupantActor(claims.PlacementID)
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(time.Minute))
	return claims
}

func withoutExpiry(claims OccupantClaims) OccupantClaims {
	claims.ExpiresAt = nil
	return claims
}

func withSubject(claims OccupantClaims, subject string) OccupantClaims {
	claims.Subject = subject
	return claims
}

func withWorkspace(claims OccupantClaims, workspace string) OccupantClaims {
	claims.WorkspaceKey = workspace
	return claims
}

func withPlacement(claims OccupantClaims, placement string) OccupantClaims {
	claims.PlacementID = placement
	return claims
}

func withGeneration(claims OccupantClaims, generation int64) OccupantClaims {
	claims.Generation = generation
	return claims
}

func tamperJWTClaim(t *testing.T, token, key string, value any) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d, want 3", len(parts))
	}
	var payload map[string]any
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	payload[key] = value
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(encoded)
	return strings.Join(parts, ".")
}
