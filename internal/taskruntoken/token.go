// Package taskruntoken defines the scoped per-TaskRun capability token (PRD
// Phase C, docs/product/loom-typescript-sdk-spec.md, "Auth & trust model").
//
// When loom leases a TaskRun to a runner it mints a short-lived, least-privilege
// token bound to exactly one {workspace, task, session} and the lease's current
// fencing token. The runner presents it (via @loom/sdk) to `loom serve` to read
// its task and report results — it never holds fleetdb credentials.
//
// The {workspace, task, session} binding is the primary least-privilege control:
// a token minted for one TaskRun cannot read another task, report against
// another session, or claim new work. The scope list is a secondary capability
// filter.
//
// This package is intentionally dependency-light (only the JWT lib): both the
// daemon supervisor (which mints at lease time) and loom serve (which validates)
// depend down onto it, so neither pulls the other's stack in. The shared HMAC
// signing key is provisioned elsewhere (fleet.SigningKeyManager / env).
package taskruntoken

import (
	"fmt"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TaskRun token scopes. Coarse capability filter layered on top of the
// workspace/task/session binding.
const (
	ScopeTaskRead     = "task:read"     // read this task (getTask)
	ScopeTaskComment  = "task:comment"  // comment on this task
	ScopeSessionWrite = "session:write" // status / artifact / usage / log / heartbeat / complete for this session
)

// DefaultScopes is the least-privilege set a runner needs: read its task and
// report against its own session. It deliberately excludes any claim/admin
// scope, so a leaked TaskRun token cannot acquire new work.
var DefaultScopes = []string{ScopeTaskRead, ScopeTaskComment, ScopeSessionWrite}

// DefaultTTL is the lifetime of a minted TaskRun token. It is kept short so a
// leaked token dies quickly after the runner stops; long runs are covered by
// refresh-on-heartbeat (the heartbeat endpoint re-issues a fresh-TTL token
// bound to the same TaskRun, and the SDK rotates onto it). Both the supervisor
// (initial mint) and loom serve (refresh mint) source the value from here so
// it stays in one place. The runner heartbeats well inside this window.
const DefaultTTL = 30 * time.Minute

// Claims binds a capability token to exactly one TaskRun.
type Claims struct {
	Workspace    string   `json:"workspace"`
	TaskID       string   `json:"task_id"`
	SessionID    string   `json:"session_id"`
	FencingToken int64    `json:"fencing_token"`
	Scopes       []string `json:"scopes,omitempty"`
	jwt.RegisteredClaims
}

// Generate mints a signed, scoped TaskRun capability token valid for expiry
// (see DefaultTTL). The token's TTL is deliberately short: a long run keeps it
// alive by refreshing on heartbeat (the heartbeat endpoint re-mints with the
// same {workspace, task, session, fencing} binding and a fresh TTL), so a
// leaked token dies soon after the runner stops heartbeating. Within the TTL,
// fencing is the backstop: a stale writer is rejected because a newer lease
// holder has a higher fencing token.
func Generate(c Claims, signingKey []byte, expiry time.Duration) (string, error) {
	if len(signingKey) == 0 {
		return "", fmt.Errorf("taskrun token: empty signing key")
	}
	if c.Workspace == "" || c.SessionID == "" {
		return "", fmt.Errorf("taskrun token: workspace and session_id are required")
	}
	now := time.Now()
	if len(c.Scopes) == 0 {
		c.Scopes = slices.Clone(DefaultScopes)
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

// Validate parses and verifies a TaskRun token (HMAC signature + expiry). It does
// NOT check scope, binding, or fencing — callers do that via the claim helpers
// against the request path and the current lease.
func Validate(tokenString string, signingKey []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return signingKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("taskrun token: parse: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("taskrun token: invalid claims")
	}
	return claims, nil
}

// HasScope reports whether the token carries the given capability.
func (c *Claims) HasScope(scope string) bool {
	return slices.Contains(c.Scopes, scope)
}

// AuthorizesSession reports whether the token may act on the given
// workspace+session. A token minted for one TaskRun cannot touch another session
// (the caller maps a mismatch to HTTP 403).
func (c *Claims) AuthorizesSession(workspace, sessionID string) bool {
	return c.Workspace != "" && c.Workspace == workspace &&
		c.SessionID != "" && c.SessionID == sessionID
}

// AuthorizesTask reports whether the token may read/comment on the given
// workspace+task (a mismatch is the scope-test 403 in PRD Phase C validation).
func (c *Claims) AuthorizesTask(workspace, taskID string) bool {
	return c.Workspace != "" && c.Workspace == workspace &&
		c.TaskID != "" && c.TaskID == taskID
}

// FencedOut reports whether this token's fencing token is stale relative to the
// current lease holder's. A stale writer (lower fencing token) must be rejected
// with HTTP 409 so a zombie/duplicate runner cannot corrupt state; the current
// holder (equal token) is allowed. A higher token should never occur (the lease
// store issues monotonically) but is treated as not-stale defensively.
func (c *Claims) FencedOut(currentFencingToken int64) bool {
	return c.FencingToken < currentFencingToken
}
