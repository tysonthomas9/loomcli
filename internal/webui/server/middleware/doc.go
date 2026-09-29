// Package middleware holds the HTTP layer that runs before a loom handler:
// authentication, workspace scoping, CORS, and rate limiting.
//
// Identity and scope are established once and carried in the request context.
// WithUserIdentity and WithWorkspace (and WithWorkspaceRef) attach them;
// WorkspaceFromContext and VerifiedUserActorFromContext read them back. The
// "Verified" in that name is load-bearing — it returns an actor only when the
// request's identity was actually verified, so a handler attributing a write
// cannot accidentally trust an unauthenticated caller's claim about who it is.
//
// RateLimit returns both a RateLimiter and the Middleware that applies it, so
// the limiter's state can be inspected while the middleware is installed
// normally. ExtractClientIP resolves the address a limit is keyed on, and
// ExtractOrigin normalizes an origin for CORS comparison.
//
// NewJWKSHTTPClient builds the client used to fetch OIDC signing keys, taking
// an explicit dialer so key fetches obey the same egress policy as the rest of
// the server.
package middleware
