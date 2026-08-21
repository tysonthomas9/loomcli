package middleware

import (
	"context"
	"fmt"
	"strings"
)

// ActorKind classifies the request principal behind an issue-backend call.
type ActorKind string

const (
	// ActorKindWebUI is the browser UI principal. It keeps serve's
	// process-configured fleet-db actor (BackendActor() == "") and the
	// legacy "web-ui" attribution.
	ActorKindWebUI ActorKind = "web-ui"
	// ActorKindOccupant is a sandboxed lead authenticated by a
	// placement-scoped occupant token. Its id is the verified token
	// subject ("lead-occupant:<placementID>"). Occupants never get a
	// UserIdentity on ctx.
	ActorKindOccupant ActorKind = "lead-occupant"
)

// occupantActorPrefix mirrors leadtoken.OccupantActor's subject form.
// middleware deliberately does not import leadtoken; a test in the
// middleware package's external test file asserts the prefix matches
// leadtoken.OccupantActor output.
const occupantActorPrefix = "lead-occupant:"

// Actor is the resolved per-request principal. Fields are private: the
// only constructors are WebUIActor and OccupantActorFor, so a malformed
// principal cannot be built outside this package. Backend actor (X-Actor)
// and write attribution both derive from the same (kind, id) pair.
type Actor struct {
	kind ActorKind
	id   string
}

// WebUIActor is the principal for the browser mount.
func WebUIActor() Actor { return Actor{kind: ActorKindWebUI} }

// OccupantActorFor returns the occupant principal for a canonical verified
// token subject ("lead-occupant:<placementID>"). It is the ONLY way to
// construct an occupant Actor. Noncanonical or whitespace-only subjects
// are rejected; callers must fail closed on error.
func OccupantActorFor(subject string) (Actor, error) {
	suffix, ok := strings.CutPrefix(subject, occupantActorPrefix)
	if !ok || suffix == "" || suffix != strings.TrimSpace(suffix) {
		return Actor{}, fmt.Errorf("noncanonical occupant subject %q", subject)
	}
	return Actor{kind: ActorKindOccupant, id: subject}, nil
}

// Kind reports the principal class. The zero Actor reports "" (unknown).
func (a Actor) Kind() ActorKind { return a.kind }

// Validate reports whether the Actor is internally coherent. The zero
// Actor, unknown kinds, a web-ui actor carrying an id, and noncanonical
// occupant subjects are all invalid: callers must FAIL CLOSED (reject the
// request / return an unavailable backend), never fall back to web-ui
// semantics.
func (a Actor) Validate() error {
	switch a.kind {
	case ActorKindWebUI:
		if a.id != "" {
			return fmt.Errorf("web-ui actor must not carry an id (got %q)", a.id)
		}
		return nil
	case ActorKindOccupant:
		suffix, ok := strings.CutPrefix(a.id, occupantActorPrefix)
		if !ok || suffix == "" || suffix != strings.TrimSpace(suffix) {
			return fmt.Errorf("noncanonical occupant subject %q", a.id)
		}
		return nil
	default:
		return fmt.Errorf("unknown actor kind %q", a.kind)
	}
}

// BackendActor returns the fleet-db X-Actor override, or "" to keep the
// process-configured actor.
func (a Actor) BackendActor() string { return a.id }

// Attribution returns the string written to CreatedBy / comment Author.
// Derived from the principal so it can never diverge from BackendActor.
func (a Actor) Attribution() string {
	if a.kind == ActorKindOccupant {
		return a.id
	}
	return "web-ui"
}

// OverridesClientAttribution reports whether serve must ignore
// client-supplied CreatedBy / Assignee / Owner / comment Author and
// substitute Attribution. True for principals that do not get to name
// themselves.
func (a Actor) OverridesClientAttribution() bool { return a.kind == ActorKindOccupant }

type actorContextKey struct{}

// WithActor returns a new context carrying the request principal. It never
// writes the UserIdentity key: occupant principals must not be observable
// as authenticated users. Phase A stamps occupant actors AFTER the leadapi
// auth chain has verified the token; nothing in Phase 0 produces an
// occupant actor.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a)
}

// ActorFromContext extracts the request principal. ok=false when no mount
// injected one (all pre-existing callers), which preserves legacy behavior
// at every consumption site.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorContextKey{}).(Actor)
	return a, ok
}
