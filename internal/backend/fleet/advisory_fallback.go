package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/advisoryactor"
)

// advisoryDenialTTL is how long a "this actor has no role" verdict is cached.
// Long enough that a denied operator identity costs one extra round-trip every
// ten minutes rather than one per write; short enough that granting the role in
// the issue store takes effect without restarting the process.
const advisoryDenialTTL = 10 * time.Minute

// noRoleDenialMessage is the exact message fleet-db's authz layer returns when
// the request's actor holds no ACL role in the workspace. It is deliberately
// matched verbatim: "insufficient permissions" (the actor HAS a role that
// lacks the permission) must not fall back, because retrying that as the
// process actor would be privilege escalation rather than a fallback.
const noRoleDenialMessage = "workspace access denied"

// isNoRoleDenial reports whether resp is fleet-db's "actor has no role in this
// workspace" rejection — the one denial that is safe to retry as the process
// actor, because it means the request was refused before it ran and no state
// changed.
func isNoRoleDenial(statusCode int, resp *apiResponse) bool {
	if statusCode != http.StatusForbidden || resp == nil {
		return false
	}
	return resp.Code == "forbidden" && resp.Error == noRoleDenialMessage
}

// clock returns the backend's time source, tolerating a FleetBackend that was
// built without New (zero-value literals in tests).
func (b *FleetBackend) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// advisoryActorDenied reports whether actor was denied for lack of a role
// within the last advisoryDenialTTL, in which case the doomed first attempt is
// skipped and the request goes straight out as the process actor.
func (b *FleetBackend) advisoryActorDenied(actor string) bool {
	b.mu.RLock()
	at, ok := b.deniedActors[actor]
	b.mu.RUnlock()
	return ok && b.clock().Sub(at) < advisoryDenialTTL
}

// recordAdvisoryDenial caches the denial for actor and emits the remediation
// warning once per actor per TTL window. The lock is never held across HTTP.
func (b *FleetBackend) recordAdvisoryDenial(actor, processActor string) {
	now := b.clock()

	b.mu.Lock()
	if b.deniedActors == nil {
		b.deniedActors = make(map[string]time.Time)
	}
	for known, at := range b.deniedActors {
		if now.Sub(at) >= advisoryDenialTTL {
			delete(b.deniedActors, known)
		}
	}
	_, warned := b.deniedActors[actor]
	b.deniedActors[actor] = now
	workspace := b.workspace
	b.mu.Unlock()

	if warned {
		return
	}
	slog.Warn("operator actor has no role in the issue backend; retrying as the process actor",
		"actor", actor,
		"workspace", workspace,
		"process_actor", processActor,
		"remediation", advisoryDenialRemediation(actor),
		"retry_after", advisoryDenialTTL.String())
}

// advisoryDenialRemediation is the operator-facing instruction attached to
// both the warning log and the 403 error message.
func advisoryDenialRemediation(actor string) string {
	return fmt.Sprintf("grant the actor a role in fleet-db "+
		"(redis: SET fleet-db:acl:global-roles:%s maintainer), or set LOOM_OPERATOR_ACTOR "+
		"to an actor that has one; writes meanwhile succeed but are attributed to the "+
		"process actor and the role is re-probed within %s", actor, advisoryDenialTTL)
}

// CheckActorAccess probes whether actor is authorized to read this workspace,
// using the authz-gated issue count endpoint (a read, so the probe has no side
// effects). Returns nil when the actor is authorized.
//
// The probe deliberately bypasses the advisory fallback: it stamps no advisory
// actor on the context, so a 403 is reported rather than papered over. This is
// what `loom doctor` uses to tell an operator that attribution is silently
// falling back before they discover it by clicking.
func (b *FleetBackend) CheckActorAccess(ctx context.Context, actor string) error {
	const op = "CheckActorAccess"
	if actor == "" {
		return backend.ErrValidation(op, "actor must not be empty")
	}
	probeCtx := advisoryactor.With(ctx, "")
	apiResp, statusCode, err := b.doRequestAsActor(probeCtx, http.MethodGet, "/issues/count", nil, actor)
	if err != nil {
		return classifyTransportError(op, err)
	}
	return b.classifyAs(probeCtx, op, statusCode, *apiResp, actor, actor)
}

// Workspace returns the workspace id this backend is scoped to. Exposed so
// diagnostics can name the workspace an actor was rejected from.
func (b *FleetBackend) Workspace() string { return b.workspace }
