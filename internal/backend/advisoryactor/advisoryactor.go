// Package advisoryactor carries an actor identity on a request context,
// together with whether that identity is *advisory*.
//
// An advisory actor is an identity a caller would like a write attributed to,
// but which must never be allowed to make the write fail. The webui stamps the
// open-mode operator identity that way before a board write; the fleet backend
// reads it back and, when the issue store rejects it for having no ACL role,
// transparently retries the request as the configured process actor.
//
// A VERIFIED actor is carried the same way but is NOT advisory, and that
// distinction is the security boundary. fleet-db answers "workspace access
// denied" both for the open-mode operator identity, which legitimately holds
// no role, and for an authenticated user who simply is not authorised here.
// Retrying the first as the process actor restores attribution the operator
// never had; retrying the second hands an outsider the process actor's
// privileges. Same message, opposite meanings — so the caller, which is the
// only party that knows whether the identity was verified, states it here
// rather than leaving the backend to infer it from a string.
//
// It is a leaf package (stdlib only) so both the webui handlers and the fleet
// backend can depend on it without either depending on the other.
package advisoryactor

import "context"

type contextKey struct{}

// stamp is the value carried on the context. It is unexported: callers go
// through With/WithVerified so the advisory bit cannot be set by accident.
type stamp struct {
	actor    string
	advisory bool
}

// With returns a copy of ctx carrying actor as an ADVISORY actor: the backend
// may retry as the process actor if the store says this identity has no role.
// An empty actor is stored as-is and reads back as unstamped, which is the
// fail-safe state — the backend then keeps the process identity and never
// retries.
func With(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, contextKey{}, stamp{actor: actor, advisory: actor != ""})
}

// WithVerified returns a copy of ctx carrying actor as a VERIFIED actor: it is
// used for the write and a denial is surfaced to the caller, never retried as
// anyone else. Use this for any identity established by authentication.
func WithVerified(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, contextKey{}, stamp{actor: actor, advisory: false})
}

// From returns the actor stamped on ctx, advisory or verified, or "" when the
// context carries none.
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(contextKey{}).(stamp)
	return s.actor
}

// IsAdvisory reports whether the actor on ctx may be substituted for the
// process actor when the store denies it for lack of a role.
//
// It is false for an unstamped context and false for a verified actor, so
// every path that does not explicitly opt in fails closed.
func IsAdvisory(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	s, _ := ctx.Value(contextKey{}).(stamp)
	return s.advisory && s.actor != ""
}
