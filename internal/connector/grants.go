package connector

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// DenyReason classifies why Evaluate denied a call. Recorded verbatim in the
// connector-call audit journal's sanitized summary.
type DenyReason string

const (
	// DenyReasonNoGrants: the binding has no active grants at all on this
	// connector — the deny-by-default baseline (Permissions[] is never
	// auto-migrated; egress fails closed until explicit grants exist).
	DenyReasonNoGrants DenyReason = "no_grants"
	// DenyReasonActionNotGranted: active grants exist but none covers the
	// requested action (actions match exactly; no action wildcards).
	DenyReasonActionNotGranted DenyReason = "action_not_granted"
	// DenyReasonResourceNotGranted: a grant covers the action but no
	// resource pattern matches the requested resource (e.g. cross-repo).
	DenyReasonResourceNotGranted DenyReason = "resource_not_granted"
	// DenyReasonGrantRevoked: the only grant(s) that would have matched
	// have been revoked. Revoked grants never authorize egress.
	DenyReasonGrantRevoked DenyReason = "grant_revoked"
)

// GrantDenied is the structured deny returned by Evaluate. It is an error:
// errors.Is matches domain.ErrGrantDenied always, and additionally
// domain.ErrGrantRevoked when the deny is due to a revoked grant.
type GrantDenied struct {
	BindingID string
	Action    string
	Resource  string
	Reason    DenyReason
}

// Error implements error with a redaction-safe summary (identifiers only,
// never credentials or payloads).
func (d *GrantDenied) Error() string {
	return fmt.Sprintf("connector: grant denied for binding %q action %q resource %q: %s",
		d.BindingID, d.Action, d.Resource, d.Reason)
}

// Unwrap exposes the matching domain sentinels for errors.Is.
func (d *GrantDenied) Unwrap() []error {
	if d.Reason == DenyReasonGrantRevoked {
		return []error{domain.ErrGrantDenied, domain.ErrGrantRevoked}
	}
	return []error{domain.ErrGrantDenied}
}

// Decision is the deterministic outcome of evaluating one (binding, action,
// resource) tuple against the binding's grants.
type Decision struct {
	// Allowed reports whether an active grant authorizes the call.
	Allowed bool
	// GrantID identifies the first active grant that matched, when Allowed.
	GrantID string
	// Denied carries the structured deny when !Allowed; nil otherwise.
	Denied *GrantDenied
}

// Err returns nil when the decision allows the call and the structured
// GrantDenied error otherwise.
func (d Decision) Err() error {
	if d.Allowed {
		return nil
	}
	return d.Denied
}

// Evaluate decides whether bindingID may perform action on resource given
// its grants (normally store.ConnectorGrantStore.ListByBinding output, but
// revoked or foreign-binding rows are excluded again here as defense in
// depth). Matching is deterministic and pure:
//
//   - actions match exactly (no wildcards),
//   - resources match via MatchResource segment globs,
//   - revoked grants never authorize,
//   - no match means deny — grants are deny-by-default.
//
// Deny reasons are ordered by specificity: a revoked grant that would have
// matched wins over the generic reasons so operators can see exactly why a
// previously working call now fails.
func Evaluate(bindingID string, grants []*domain.ConnectorGrant, action, resource string) Decision {
	deny := func(reason DenyReason) Decision {
		return Decision{Denied: &GrantDenied{
			BindingID: bindingID,
			Action:    action,
			Resource:  resource,
			Reason:    reason,
		}}
	}

	activeCount := 0
	actionMatched := false
	revokedWouldMatch := false
	for _, g := range grants {
		if g == nil || g.BindingID != bindingID {
			continue
		}
		matches := g.Action == action && MatchResource(g.ResourcePattern, resource)
		if g.Revoked() {
			if matches {
				revokedWouldMatch = true
			}
			continue
		}
		activeCount++
		if g.Action != action {
			continue
		}
		actionMatched = true
		if matches {
			return Decision{Allowed: true, GrantID: g.GrantID}
		}
	}

	switch {
	case revokedWouldMatch:
		return deny(DenyReasonGrantRevoked)
	case activeCount == 0:
		return deny(DenyReasonNoGrants)
	case !actionMatched:
		return deny(DenyReasonActionNotGranted)
	default:
		return deny(DenyReasonResourceNotGranted)
	}
}

// MatchResource reports whether resource matches pattern under the simple
// "/"-separated segment glob used by connector grants (deliberately local —
// NOT the Phase B trigger pattern package — to keep this package
// conflict-free and the grammar small):
//
//   - a pattern segment of "*" matches exactly one resource segment,
//   - a trailing pattern segment of "**" matches one or more remaining
//     segments (it does not match zero: granting "repo:octocat/**" does not
//     grant the bare "repo:octocat"),
//   - any other segment must match exactly, case-sensitively; there are no
//     in-segment wildcards ("repo:octo*" is a literal),
//   - "**" anywhere except the final segment is a literal and in practice
//     never matches.
//
// Examples: "repo:octocat/hello" matches itself; "repo:octocat/*" matches
// "repo:octocat/hello" but not "repo:octocat/hello/issues/7";
// "repo:octocat/**" matches both.
func MatchResource(pattern, resource string) bool {
	if pattern == "" || resource == "" {
		return false
	}
	patSegs := strings.Split(pattern, "/")
	resSegs := strings.Split(resource, "/")
	for i, ps := range patSegs {
		if ps == "**" && i == len(patSegs)-1 {
			return len(resSegs) > i // one or more remaining segments
		}
		if i >= len(resSegs) {
			return false
		}
		if ps != "*" && ps != resSegs[i] {
			return false
		}
	}
	return len(resSegs) == len(patSegs)
}

// irreversiblePreconditions maps irreversible actions to the camelCase wire
// fields a caller MUST supply before egress. The dispatch layer (CV8)
// consults this registry and refuses an irreversible call lacking any
// required field with decision "precondition_required".
//
// Freshness semantics differ per provider and the TOCTOU exposure is
// documented here per entry:
//
//   - github.merge: TRUE server-side precondition — expectedHeadSha is sent
//     as the GitHub merge API's sha parameter, so the provider itself
//     rejects a stale head. No TOCTOU window.
//   - github.branch.delete: best-effort pre-egress read of the branch head;
//     the head can move between read and delete (TOCTOU accepted).
//   - issues.set_priority: best-effort pre-egress read of the issue
//     revision; TOCTOU window accepted.
//   - slack.chat.delete: best-effort pre-egress read of the message ts;
//     TOCTOU window accepted.
//   - datadog.monitor.delete: best-effort pre-egress read of the monitor
//     revision; TOCTOU window accepted.
var irreversiblePreconditions = map[string][]string{
	"github.merge":              {"expectedHeadSha"},
	"github.branch.delete":      {"expectedHeadSha"},
	"issues.set_priority":       {"expectedIssueRevision"},
	"slack.chat.delete":         {"expectedMessageTs"},
	"datadog.monitor.delete":    {"expectedMonitorRevision"},
	"datadog.monitor.mute":      {"expectedMonitorRevision"},
	"github.pull_request.close": {"expectedHeadSha"},
}

// IsIrreversible reports whether action is registered as irreversible and
// therefore requires preconditions before egress.
func IsIrreversible(action string) bool {
	_, ok := irreversiblePreconditions[action]
	return ok
}

// RequiredPreconditions returns a copy of the precondition field names an
// irreversible action demands, or nil for actions not in the registry
// (reversible actions need no preconditions).
func RequiredPreconditions(action string) []string {
	fields, ok := irreversiblePreconditions[action]
	if !ok {
		return nil
	}
	out := make([]string, len(fields))
	copy(out, fields)
	return out
}
