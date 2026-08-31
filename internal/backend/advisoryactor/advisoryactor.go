// Package advisoryactor carries an *advisory* actor identity on a request
// context.
//
// An advisory actor is an identity a caller would like a write attributed to,
// but which must never be allowed to make the write fail. The webui stamps the
// operator identity here before issuing a board write; the fleet backend reads
// it back and, when the issue store rejects that identity for having no ACL
// role, transparently retries the request as the configured process actor.
//
// It is a leaf package (stdlib only) so both the webui handlers and the fleet
// backend can depend on it without either depending on the other.
package advisoryactor

import "context"

type contextKey struct{}

// With returns a copy of ctx carrying actor as the advisory actor. An empty
// actor is stored as-is and reads back as unstamped, which is the fail-safe
// state: the backend then keeps the process identity and never retries.
func With(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, contextKey{}, actor)
}

// From returns the advisory actor stamped on ctx, or "" when the context
// carries none.
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	actor, _ := ctx.Value(contextKey{}).(string)
	return actor
}
