package driver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	// RunTokenSigningKeyEnv names the env var holding the hex-encoded
	// 32-byte HS256 signing key for run tokens. When unset, an ephemeral
	// per-process key is generated: tokens then die with the serve process,
	// which also kills the runs they were minted for (single-instance
	// T0-T2). Multi-replica deployments must set this var (or move to the
	// fleet SigningKeyManager Redis pattern).
	RunTokenSigningKeyEnv = "LOOM_RUN_TOKEN_SIGNING_KEY" //nolint:gosec // env var name, not a credential

	// RunTokenTTLEnv names the env var overriding the run token TTL. The
	// TTL equals the maximum run duration: expiry doubles as the hard
	// run-duration cap. Revocation happens via fenced run verification
	// regardless of expiry, so the TTL only bounds how long a stolen token
	// stays parseable.
	RunTokenTTLEnv = "LOOM_RUN_TOKEN_TTL" //nolint:gosec // env var name, not a credential

	// DefaultRunTokenTTL is the default maximum run duration.
	DefaultRunTokenTTL = 24 * time.Hour

	// runTokenKeyLen is the required signing key length in bytes.
	runTokenKeyLen = 32
)

// ErrRunTokenInvalid indicates a run token failed validation (bad signature,
// wrong algorithm, expired, malformed, or inconsistent claims). It wraps
// domain.ErrNotOwner: presenting a token that does not prove run identity is
// an ownership failure.
var ErrRunTokenInvalid = fmt.Errorf("driver: run token invalid: %w", domain.ErrNotOwner)

// RunTokenClaims bind a bearer token to one DriverRun for one lease window.
// A stolen token is therefore bounded to a single run and rejected once the
// lease moves on (fenced verification) or the TTL — the maximum run duration
// — elapses. Tokens are stateless and never persisted; idempotent re-claims
// mint fresh ones.
type RunTokenClaims struct {
	WorkspaceKey string `json:"workspaceKey"`
	RunID        string `json:"runId"`
	NodeID       string `json:"nodeId"`
	LeaseID      string `json:"leaseId"`
	FencingToken int64  `json:"fencingToken"`

	// Caps is reserved for future capability scoping. Empty means the full
	// current driver-op surface; capability enforcement stays with
	// connector grants for now.
	Caps []string `json:"caps,omitempty"`

	jwt.RegisteredClaims
}

// MintRunToken signs claims as an HS256 JWT with Subject set to
// DriverRunActor(claims.RunID) so the store actor identity travels in the
// token. IssuedAt and ExpiresAt are always stamped; ttl must be positive.
func MintRunToken(claims RunTokenClaims, key []byte, ttl time.Duration) (string, error) {
	if strings.TrimSpace(claims.RunID) == "" {
		return "", fmt.Errorf("mint run token: run id required: %w", domain.ErrInvalid)
	}
	if len(key) == 0 {
		return "", fmt.Errorf("mint run token: signing key required: %w", domain.ErrInvalid)
	}
	if ttl <= 0 {
		return "", fmt.Errorf("mint run token: ttl must be positive, got %s: %w", ttl, domain.ErrInvalid)
	}
	now := time.Now()
	claims.Subject = DriverRunActor(claims.RunID)
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("mint run token: sign: %w", err)
	}
	return signed, nil
}

// ParseRunToken validates an HS256 run token and returns its claims. The
// algorithm is pinned to HS256 (alg-confusion rejected), expiry is required,
// and the Subject must match DriverRunActor(RunID). Callers must still pass
// the claims through fenced run verification — parsing only proves the token
// was minted by this serve, not that the lease is still live.
func ParseRunToken(token string, key []byte) (*RunTokenClaims, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("parse run token: signing key required: %w", domain.ErrInvalid)
	}
	parsed, err := jwt.ParseWithClaims(token, &RunTokenClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRunTokenInvalid, err)
	}
	claims, ok := parsed.Claims.(*RunTokenClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("%w: claims missing", ErrRunTokenInvalid)
	}
	if strings.TrimSpace(claims.RunID) == "" {
		return nil, fmt.Errorf("%w: run id claim empty", ErrRunTokenInvalid)
	}
	if claims.Subject != DriverRunActor(claims.RunID) {
		return nil, fmt.Errorf("%w: subject %q does not match run %q", ErrRunTokenInvalid, claims.Subject, claims.RunID)
	}
	return claims, nil
}

// IsRunTokenExpired reports whether a ParseRunToken failure means the token
// was correctly signed but past its expiry (jwt/v5 verifies the signature
// before validating claims, so an expired-token failure proves authenticity).
// Lets callers surface a distinct "token_expired" signal without importing
// the jwt package.
func IsRunTokenExpired(err error) bool {
	return errors.Is(err, jwt.ErrTokenExpired)
}

// ephemeralRunTokenKey generates the per-process fallback signing key once.
var ephemeralRunTokenKey = sync.OnceValues(func() ([]byte, error) {
	key := make([]byte, runTokenKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate ephemeral run token signing key: %w", err)
	}
	return key, nil
})

// ResolveRunTokenSigningKey returns the HS256 signing key for run tokens:
// LOOM_RUN_TOKEN_SIGNING_KEY (hex, 32 bytes) when set, otherwise a stable
// ephemeral per-process key.
func ResolveRunTokenSigningKey() ([]byte, error) {
	if encoded := strings.TrimSpace(os.Getenv(RunTokenSigningKeyEnv)); encoded != "" {
		return decodeRunTokenSigningKey(encoded)
	}
	return ephemeralRunTokenKey()
}

func decodeRunTokenSigningKey(encoded string) ([]byte, error) {
	key, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%s: decode hex: %v: %w", RunTokenSigningKeyEnv, err, domain.ErrInvalid)
	}
	if len(key) != runTokenKeyLen {
		return nil, fmt.Errorf("%s: key is %d bytes, want %d: %w", RunTokenSigningKeyEnv, len(key), runTokenKeyLen, domain.ErrInvalid)
	}
	return key, nil
}

// RunTokenTTL returns the run token TTL (= maximum run duration):
// LOOM_RUN_TOKEN_TTL when set (Go duration, must be positive), otherwise
// DefaultRunTokenTTL.
func RunTokenTTL() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(RunTokenTTLEnv))
	if raw == "" {
		return DefaultRunTokenTTL, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: parse duration %q: %v: %w", RunTokenTTLEnv, raw, err, domain.ErrInvalid)
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("%s: ttl must be positive, got %s: %w", RunTokenTTLEnv, ttl, domain.ErrInvalid)
	}
	return ttl, nil
}
