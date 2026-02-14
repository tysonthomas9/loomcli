package webui

import (
	"context"
	"log"
	"net/http"
	"strings"
)

// workerClaimsContextKey is the unexported context key for storing WorkerClaims.
type workerClaimsContextKey struct{}

// NewFleetAuthMiddleware creates a middleware that validates JWT bearer tokens
// on fleet endpoints. It extracts the Authorization header, validates the token
// using HMAC-SHA256, and attaches WorkerClaims to the request context.
//
// If signingKey is nil or empty, all requests are rejected with 401.
func NewFleetAuthMiddleware(signingKey []byte) func(http.Handler) http.Handler {
	if len(signingKey) == 0 {
		log.Printf("Warning: fleet JWT signing key is empty, all fleet auth requests will be rejected")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(signingKey) == 0 {
				writeFleetAuthError(w, "fleet authentication not configured")
				return
			}

			// Extract Authorization header
			auth := r.Header.Get("Authorization")
			if auth == "" {
				writeFleetAuthError(w, "missing authorization header")
				return
			}

			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) {
				writeFleetAuthError(w, "invalid authorization header format")
				return
			}

			tokenStr := auth[len(prefix):]
			if tokenStr == "" {
				writeFleetAuthError(w, "missing bearer token")
				return
			}

			// Validate the JWT
			claims, err := ValidateWorkerToken(tokenStr, signingKey)
			if err != nil {
				writeFleetAuthError(w, "invalid or expired token")
				return
			}

			// Attach claims to context and pass to next handler
			ctx := context.WithValue(r.Context(), workerClaimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WorkerClaimsFromContext extracts WorkerClaims from the request context.
// Returns nil and false if no claims are present.
func WorkerClaimsFromContext(ctx context.Context) (*WorkerClaims, bool) {
	claims, ok := ctx.Value(workerClaimsContextKey{}).(*WorkerClaims)
	return claims, ok
}

// writeFleetAuthError writes a 401 JSON error response using the FleetClaimResponse envelope.
func writeFleetAuthError(w http.ResponseWriter, msg string) {
	respondJSON(w, http.StatusUnauthorized, FleetClaimResponse{Success: false, Error: msg})
}
