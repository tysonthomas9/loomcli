package fleet

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/taskruntoken"
)

// The scoped TaskRun capability token contract lives in internal/taskruntoken
// (dependency-light, no webui deps) so the daemon supervisor — which mints at
// lease time — can depend on it without importing webui/fleet. fleet re-exports
// the type + mint/validate here so its middleware (taskrun_auth.go) and the
// SigningKeyManager methods below keep a stable local surface.

// TaskRunClaims aliases the neutral token claims (internal/taskruntoken.Claims).
type TaskRunClaims = taskruntoken.Claims

// Token scope re-exports.
const (
	ScopeTaskRead     = taskruntoken.ScopeTaskRead
	ScopeTaskComment  = taskruntoken.ScopeTaskComment
	ScopeSessionWrite = taskruntoken.ScopeSessionWrite
)

// DefaultTaskRunScopes re-exports the least-privilege default scope set.
var DefaultTaskRunScopes = taskruntoken.DefaultScopes

// GenerateTaskRunToken / ValidateTaskRunToken re-export the neutral mint/verify.
var (
	GenerateTaskRunToken = taskruntoken.Generate
	ValidateTaskRunToken = taskruntoken.Validate
)

// MintTaskRunToken loads the current shared signing key from Redis and mints a
// scoped TaskRun capability token. Because the key is shared (SET-NX in Redis,
// see GetOrCreateSigningKey), a token minted by one process — e.g. the
// daemon/supervisor at lease time — validates in another (loom serve), with no
// separate key-distribution mechanism. The caller (serve) must have created the
// key at startup via GetOrCreateSigningKey.
func (m *SigningKeyManager) MintTaskRunToken(ctx context.Context, claims TaskRunClaims, expiry time.Duration) (string, error) {
	key, err := m.GetCurrentSigningKey(ctx)
	if err != nil {
		return "", fmt.Errorf("taskrun token: load signing key: %w", err)
	}
	return taskruntoken.Generate(claims, key, expiry)
}

// ValidateTaskRunTokenFromStore validates a TaskRun token against the current
// shared signing key, falling back to the previous version during a rotation
// grace period (mirrors how worker tokens tolerate rotation).
func (m *SigningKeyManager) ValidateTaskRunTokenFromStore(ctx context.Context, tokenString string) (*TaskRunClaims, error) {
	current, previous, err := m.GetCurrentAndPreviousKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("taskrun token: load signing keys: %w", err)
	}
	claims, err := taskruntoken.Validate(tokenString, current)
	if err == nil {
		return claims, nil
	}
	if len(previous) > 0 {
		if prevClaims, prevErr := taskruntoken.Validate(tokenString, previous); prevErr == nil {
			return prevClaims, nil
		}
	}
	return nil, err
}
