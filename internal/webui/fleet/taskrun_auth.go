package fleet

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TaskRun write-path auth + fencing middleware — PRD Phase C
// (docs/product/loom-typescript-sdk-spec.md). Guards the session write
// endpoints a runner calls via @loom/sdk. The issue read/comment endpoints stay
// shared with the human web UI and are not gated by this.

// taskRunClaimsContextKey is the unexported context key for TaskRunClaims.
type taskRunClaimsContextKey struct{}

// FencingLookup returns the current lease's fencing token for a session, so the
// middleware can reject stale writers. found=false means there is no resolvable
// active lease — a mutating request is then rejected (we cannot prove ownership).
type FencingLookup func(ctx context.Context, workspace, sessionID string) (current int64, found bool, err error)

// taskRunAuthError is the JSON envelope for auth/fencing rejections.
type taskRunAuthError struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// NewTaskRunAuthMiddleware validates a scoped TaskRun capability token on
// session write endpoints. It:
//   - 401s a missing/malformed/invalid/expired token (or when auth is unconfigured);
//   - 403s a token whose binding does not match the path's workspace+session (a
//     token minted for one TaskRun cannot act on another's session);
//   - 409s a mutating request whose fencing token is stale relative to the
//     current lease (a zombie/duplicate runner cannot corrupt state);
//
// then attaches the claims to the request context for handlers.
//
// fencing may be nil to skip lease-fencing (e.g. a read-only mount); when set,
// a mutating request with no resolvable active lease is rejected with 409.
//
// tokenOptional=true runs in "enforce-if-present" mode: a request with no bearer
// token is passed straight through (e.g. to the existing dev-mode X-Actor auth),
// while a request that DOES carry a token is fully validated + fenced. This lets
// the middleware be mounted before runner token-minting is the default without
// locking out the current callers; set it false to require a token.
func NewTaskRunAuthMiddleware(signingKey []byte, fencing FencingLookup, tokenOptional bool) func(http.Handler) http.Handler {
	if len(signingKey) == 0 && !tokenOptional {
		slog.Warn("taskrun JWT signing key is empty; all TaskRun write requests will be rejected")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Enforce-if-present: no token → defer to the underlying auth.
			if tokenOptional && !hasBearerToken(r) {
				next.ServeHTTP(w, r)
				return
			}
			claims, status, msg := authenticateTaskRun(r, signingKey)
			if status != 0 {
				writeTaskRunAuthError(w, status, msg)
				return
			}

			// Binding: the token must name this workspace+session.
			workspace := middleware.WorkspaceFromContext(r.Context())
			sessionID := r.PathValue("sessionId")
			if !claims.AuthorizesSession(workspace, sessionID) {
				writeTaskRunAuthError(w, http.StatusForbidden, "token is not scoped to this session")
				return
			}

			// Fencing: a stale writer loses to the current lease holder.
			if fencing != nil && isMutating(r.Method) {
				current, found, err := fencing(r.Context(), workspace, sessionID)
				switch {
				case err != nil:
					writeTaskRunAuthError(w, http.StatusServiceUnavailable, "could not verify lease fencing")
					return
				case !found:
					writeTaskRunAuthError(w, http.StatusConflict, "no active lease for this session")
					return
				case claims.FencedOut(current):
					writeTaskRunAuthError(w, http.StatusConflict, "stale fencing token; lease held by a newer runner")
					return
				}
			}

			ctx := context.WithValue(r.Context(), taskRunClaimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// hasBearerToken reports whether the request carries a non-empty bearer token.
func hasBearerToken(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	return strings.HasPrefix(auth, prefix) && strings.TrimSpace(auth[len(prefix):]) != ""
}

// authenticateTaskRun extracts and validates the bearer token. Returns
// (claims, 0, "") on success, or (nil, status, msg) describing the rejection.
func authenticateTaskRun(r *http.Request, signingKey []byte) (*TaskRunClaims, int, string) {
	if len(signingKey) == 0 {
		return nil, http.StatusUnauthorized, "taskrun authentication not configured"
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if auth == "" || !strings.HasPrefix(auth, prefix) {
		return nil, http.StatusUnauthorized, "missing or malformed authorization header"
	}
	tokenStr := strings.TrimSpace(auth[len(prefix):])
	if tokenStr == "" {
		return nil, http.StatusUnauthorized, "missing bearer token"
	}
	claims, err := ValidateTaskRunToken(tokenStr, signingKey)
	if err != nil {
		return nil, http.StatusUnauthorized, "invalid or expired token"
	}
	return claims, 0, ""
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// TaskRunClaimsFromContext extracts validated TaskRun claims from the context.
func TaskRunClaimsFromContext(ctx context.Context) (*TaskRunClaims, bool) {
	claims, ok := ctx.Value(taskRunClaimsContextKey{}).(*TaskRunClaims)
	return claims, ok
}

func writeTaskRunAuthError(w http.ResponseWriter, status int, msg string) {
	handler.WriteJSON(w, status, taskRunAuthError{Success: false, Error: msg})
}
