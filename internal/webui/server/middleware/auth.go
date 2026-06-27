package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const extAuthMaxTokenSize = 8192

// UserIdentity represents the authenticated user extracted from a JWT.
type UserIdentity struct {
	UserID string // JWT "sub" claim — unique user ID from Better Auth
	Email  string // JWT "email" claim (optional, may be empty)
	Name   string // JWT "name" claim (optional, may be empty)
	Role   string // JWT "role" claim (optional, defaults to read-only when empty)
}

// String returns a redacted representation safe for logging.
// Only UserID is included; Email and Name are omitted to prevent PII leakage.
func (u UserIdentity) String() string {
	return fmt.Sprintf("UserIdentity{UserID: %q}", u.UserID)
}

// userIdentityContextKey is the unexported context key for storing UserIdentity.
type userIdentityContextKey struct{}

// AuthConfig holds configuration for the external auth middleware.
type AuthConfig struct {
	JWKSCache *JWKSCache   // JWKS cache for public key lookup; nil = passthrough (open mode)
	Issuer    string       // Expected JWT issuer (validated against "iss" claim)
	Audience  string       // Expected JWT audience (validated against "aud" claim)
	Logger    *slog.Logger // Structured logger (nil falls back to slog.Default())
}

// extAuthClaims extends jwt.RegisteredClaims with Better Auth custom fields.
type extAuthClaims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

// extAuthValidator holds the pre-computed configuration for JWT validation.
type extAuthValidator struct {
	cache      *JWKSCache
	logger     *slog.Logger
	parserOpts []jwt.ParserOption
}

// validateToken extracts, parses, and validates a JWT from the request.
// Returns the validated claims or an HTTP error (status, message).
func (v *extAuthValidator) validateToken(tokenStr string) (*extAuthClaims, int, string) {
	if len(tokenStr) > extAuthMaxTokenSize {
		return nil, http.StatusBadRequest, "malformed token"
	}

	kid := extractJWTKid(tokenStr)
	keys, err := v.cache.GetKey(kid)
	if err != nil {
		v.logger.Debug("JWKS key lookup failed", "error", err)
		return nil, http.StatusUnauthorized, "invalid or expired token"
	}

	claims, lastErr := v.tryKeys(tokenStr, keys)
	if claims == nil {
		if lastErr != nil {
			v.logger.Debug("JWT validation failed", "error", classifyJWTError(lastErr))
		}
		if lastErr != nil && isTokenMalformed(lastErr) {
			return nil, http.StatusBadRequest, "malformed token"
		}
		return nil, http.StatusUnauthorized, "invalid or expired token"
	}

	if claims.Subject == "" {
		return nil, http.StatusUnauthorized, "invalid token claims"
	}
	return claims, 0, ""
}

// tryKeys attempts to parse and verify a JWT against each candidate key.
func (v *extAuthValidator) tryKeys(tokenStr string, keys []*rsa.PublicKey) (*extAuthClaims, error) {
	var lastErr error
	for _, key := range keys {
		c := &extAuthClaims{}
		_, parseErr := jwt.ParseWithClaims(tokenStr, c, func(token *jwt.Token) (interface{}, error) {
			// Strict algorithm check: RS256 ONLY via pointer identity.
			// Prevents alg:none, HMAC confusion, and custom SigningMethod spoofing.
			if token.Method != jwt.SigningMethodRS256 {
				return nil, fmt.Errorf("unsupported signing algorithm: %v", token.Method.Alg())
			}
			return key, nil
		}, v.parserOpts...)
		if parseErr == nil {
			return c, nil
		}
		lastErr = parseErr
	}
	return nil, lastErr
}

// Auth creates a middleware that validates Bearer JWTs from an
// external auth service using cached JWKS public keys. When cfg.JWKSCache is nil,
// returns a passthrough middleware (open mode).
func Auth(cfg AuthConfig) Middleware {
	if cfg.JWKSCache == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	v := &extAuthValidator{
		cache:  cfg.JWKSCache,
		logger: logger,
		parserOpts: []jwt.ParserOption{
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithLeeway(5 * time.Second),
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithAudience(cfg.Audience),
		},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || isPublicRoute(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			tokenStr := extractBearerToken(r)
			if tokenStr == "" {
				writeJSONError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			claims, status, msg := v.validateToken(tokenStr)
			if claims == nil {
				writeJSONError(w, status, msg)
				return
			}

			identity := UserIdentity{
				UserID: claims.Subject,
				Email:  claims.Email,
				Name:   claims.Name,
				Role:   claims.Role,
			}
			ctx := context.WithValue(r.Context(), userIdentityContextKey{}, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithUserIdentity returns a new context with the given UserIdentity set.
// This is primarily used by tests that need to inject an identity without
// going through the full auth middleware.
func WithUserIdentity(ctx context.Context, identity UserIdentity) context.Context {
	return context.WithValue(ctx, userIdentityContextKey{}, identity)
}

// UserIdentityFromContext extracts UserIdentity from the request context.
// Returns zero-value UserIdentity and false if not present.
func UserIdentityFromContext(ctx context.Context) (UserIdentity, bool) {
	identity, ok := ctx.Value(userIdentityContextKey{}).(UserIdentity)
	return identity, ok
}

// extractBearerToken extracts a Bearer token from the Authorization header only.
// Case-insensitive "Bearer" prefix check per RFC 6750.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) < 8 { // len("Bearer ") == 7, plus at least 1 char token
		return ""
	}
	if strings.EqualFold(auth[:7], "bearer ") {
		return auth[7:]
	}
	return ""
}

// extractJWTKid extracts the kid (Key ID) from a raw JWT's header segment
// without full parsing or signature verification.
func extractJWTKid(tokenStr string) string {
	dot := strings.IndexByte(tokenStr, '.')
	if dot < 0 {
		return ""
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(tokenStr[:dot])
	if err != nil {
		return ""
	}
	var header struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return ""
	}
	return header.Kid
}

// classifyJWTError returns a safe error classification string (never includes token content).
func classifyJWTError(err error) string {
	if isTokenMalformed(err) {
		return "malformed"
	}
	return "validation_failed"
}

// isTokenMalformed checks if a JWT parse error indicates a malformed token.
func isTokenMalformed(err error) bool {
	return errors.Is(err, jwt.ErrTokenMalformed)
}
