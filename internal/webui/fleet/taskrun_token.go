package fleet

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TaskRun-scoped capability tokens — PRD Phase C
// (docs/product/loom-typescript-sdk-spec.md, "Auth & trust model").
//
// When loom leases a TaskRun to a runner it mints a short-lived, least-privilege
// token bound to exactly one {workspace, task, session} and the lease's current
// fencing token. The runner presents it (via @loom/sdk) to `loom serve` to read
// its task and report results — it never holds fleetdb credentials.
//
// The {workspace, task, session} binding is the primary least-privilege control:
// a token minted for one TaskRun cannot read another task, report against
// another session, or claim new work. The scope list is a secondary capability
// filter. Reuses the same HMAC-SHA256 signing the fleet worker tokens use
// (jwt.go); the signing key is provisioned once at `loom serve` startup, so this
// adds no new key-management surface (resolving the PRD's "JWT vs macaroon" open
// question by following the codebase's existing precedent).

// TaskRun token scopes. Coarse capability filter layered on top of the
// workspace/task/session binding.
const (
	ScopeTaskRead     = "task:read"     // read this task (getTask)
	ScopeTaskComment  = "task:comment"  // comment on this task
	ScopeSessionWrite = "session:write" // status / artifact / usage / log / heartbeat / complete for this session
)

// DefaultTaskRunScopes is the least-privilege set a runner needs: read its task
// and report against its own session. It deliberately excludes any claim/admin
// scope, so a leaked TaskRun token cannot acquire new work.
var DefaultTaskRunScopes = []string{ScopeTaskRead, ScopeTaskComment, ScopeSessionWrite}

// TaskRunClaims binds a capability token to exactly one TaskRun.
type TaskRunClaims struct {
	Workspace    string   `json:"workspace"`
	TaskID       string   `json:"task_id"`
	SessionID    string   `json:"session_id"`
	FencingToken int64    `json:"fencing_token"`
	Scopes       []string `json:"scopes,omitempty"`
	jwt.RegisteredClaims
}

// GenerateTaskRunToken mints a signed, scoped TaskRun capability token. The TTL
// should track the lease TTL; the runner refreshes via heartbeat while the lease
// holds, so a lost lease means the token is not refreshed and calls fail closed.
func GenerateTaskRunToken(c TaskRunClaims, signingKey []byte, expiry time.Duration) (string, error) {
	if len(signingKey) == 0 {
		return "", fmt.Errorf("taskrun token: empty signing key")
	}
	if c.Workspace == "" || c.SessionID == "" {
		return "", fmt.Errorf("taskrun token: workspace and session_id are required")
	}
	now := time.Now()
	if len(c.Scopes) == 0 {
		c.Scopes = slices.Clone(DefaultTaskRunScopes)
	}
	c.RegisteredClaims = jwt.RegisteredClaims{
		Subject:   c.SessionID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := token.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("taskrun token: sign: %w", err)
	}
	return signed, nil
}

// ValidateTaskRunToken parses and verifies a TaskRun token (HMAC signature +
// expiry). It does NOT check scope, binding, or fencing — callers do that via
// the claim helpers against the request path and the current lease.
func ValidateTaskRunToken(tokenString string, signingKey []byte) (*TaskRunClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TaskRunClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return signingKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("taskrun token: parse: %w", err)
	}
	claims, ok := token.Claims.(*TaskRunClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("taskrun token: invalid claims")
	}
	return claims, nil
}

// HasScope reports whether the token carries the given capability.
func (c *TaskRunClaims) HasScope(scope string) bool {
	return slices.Contains(c.Scopes, scope)
}

// AuthorizesSession reports whether the token may act on the given
// workspace+session. A token minted for one TaskRun cannot touch another
// session (the caller maps a mismatch to HTTP 403).
func (c *TaskRunClaims) AuthorizesSession(workspace, sessionID string) bool {
	return c.Workspace != "" && c.Workspace == workspace &&
		c.SessionID != "" && c.SessionID == sessionID
}

// AuthorizesTask reports whether the token may read/comment on the given
// workspace+task (a mismatch is the scope-test 403 in PRD Phase C validation).
func (c *TaskRunClaims) AuthorizesTask(workspace, taskID string) bool {
	return c.Workspace != "" && c.Workspace == workspace &&
		c.TaskID != "" && c.TaskID == taskID
}

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
	return GenerateTaskRunToken(claims, key, expiry)
}

// ValidateTaskRunTokenFromStore validates a TaskRun token against the current
// shared signing key, falling back to the previous version during a rotation
// grace period (mirrors how worker tokens tolerate rotation).
func (m *SigningKeyManager) ValidateTaskRunTokenFromStore(ctx context.Context, tokenString string) (*TaskRunClaims, error) {
	current, previous, err := m.GetCurrentAndPreviousKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("taskrun token: load signing keys: %w", err)
	}
	claims, err := ValidateTaskRunToken(tokenString, current)
	if err == nil {
		return claims, nil
	}
	if len(previous) > 0 {
		if prevClaims, prevErr := ValidateTaskRunToken(tokenString, previous); prevErr == nil {
			return prevClaims, nil
		}
	}
	return nil, err
}

// FencedOut reports whether this token's fencing token is stale relative to the
// current lease holder's. A stale writer (lower fencing token) must be rejected
// with HTTP 409 so a zombie/duplicate runner cannot corrupt state; the current
// holder (equal token) is allowed. A higher token should never occur (the lease
// store issues monotonically) but is treated as not-stale defensively.
func (c *TaskRunClaims) FencedOut(currentFencingToken int64) bool {
	return c.FencingToken < currentFencingToken
}
