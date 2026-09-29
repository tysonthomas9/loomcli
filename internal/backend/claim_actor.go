package backend

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ClaimAs acquires an issue's operational lock on behalf of actor.
//
// # The rule
//
//	actor == ""                        -> plain ClaimIssue. Legitimate: nothing
//	                                      configured an identity, so there is
//	                                      none to lose. Unchanged behavior, and
//	                                      what keeps backends (and test fakes)
//	                                      that only implement IssueBackend valid.
//	actor != "", backend is ActorClaimer -> ClaimIssueAsActor. The identity
//	                                      reaches fleet-db, which arbitrates
//	                                      locks BY actor.
//	actor != "", backend is not         -> refuse. KindNotImplemented naming the
//	                                      concrete backend type and the actor
//	                                      that would have been dropped.
//
// # Why refuse rather than warn and proceed
//
// A capability check that falls through is not a degraded claim, it is a claim
// under the wrong name. fleet-db grants the lock to whoever asked, so a claim
// that loses its actor is recorded against the client's own identity: every
// sibling worker then looks like one claimant, concurrent claims all appear to
// win, and any sibling can release a lock it does not hold. By the time a
// warning is read, two agents are already running the same task and one has
// released the other's lock. The corruption is not undone by a log line.
//
// Refusing costs availability in one narrow case — a caller that has an actor
// talking to a backend that cannot express one — but that case has no correct
// outcome to begin with, and it fails visibly: every claim call site already
// propagates a claim error (the driver returns it, the supervisor records a
// preflight error, the web UI surfaces the service error), so the fleet stops
// with a message naming the backend instead of silently corrupting locks.
// Identity primitives fail closed; fleet-db does the same thing, answering 401
// when a request arrives with no resolvable actor.
//
// This is the one place the rule is written. Every claim path routes through
// here rather than repeating a private `interface{ ClaimIssueAsActor(...) }`
// assertion — the duplicated assertions are precisely how the serve-mediated
// path lost the capability without anything failing to compile.
func ClaimAs(ctx context.Context, be IssueBackend, id string, lockTTL time.Duration, actor string) error {
	if be == nil {
		return ErrValidation("ClaimIssue", "issue backend must not be nil")
	}
	// Trim once, here, so a header that arrives as "  " cannot select the
	// actor-scoped path with an empty identity, and so " w " and "w" are the
	// same claimant to per-actor arbitration.
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return be.ClaimIssue(ctx, id, lockTTL)
	}
	claimer, ok := be.(ActorClaimer)
	if !ok {
		return ErrNotImplemented("ClaimIssue", fmt.Sprintf(
			"backend %T cannot scope a claim to an actor; refusing to claim %q as %q "+
				"because the lock would be recorded against this client's own identity "+
				"and siblings would each appear to win",
			be, id, actor))
	}
	return claimer.ClaimIssueAsActor(ctx, id, lockTTL, actor)
}
