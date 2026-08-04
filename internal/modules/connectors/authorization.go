package connectors

import (
	"fmt"
	"strings"
)

// GrantDenyReason classifies an owner-policy refusal without exposing
// credentials or provider payloads.
type GrantDenyReason string

const (
	GrantDenyNoGrants           GrantDenyReason = "no_grants"
	GrantDenyActionNotGranted   GrantDenyReason = "action_not_granted"
	GrantDenyResourceNotGranted GrantDenyReason = "resource_not_granted"
	GrantDenyRevoked            GrantDenyReason = "grant_revoked"
)

// GrantDenied is the structured, redaction-safe denial returned by
// EvaluateGrantAuthorization.
type GrantDenied struct {
	BindingID string
	Action    string
	Resource  string
	Reason    GrantDenyReason
}

func (denied *GrantDenied) Error() string {
	return fmt.Sprintf(
		"connectors: grant denied for binding %q action %q resource %q: %s",
		denied.BindingID,
		denied.Action,
		denied.Resource,
		denied.Reason,
	)
}

func (denied *GrantDenied) Unwrap() []error {
	if denied.Reason == GrantDenyRevoked {
		return []error{ErrGrantDenied, ErrGrantRevoked}
	}
	return []error{ErrGrantDenied}
}

// GrantAuthorization is the deterministic result of evaluating one binding,
// action, and resource tuple against a Connectors-owned grant projection.
type GrantAuthorization struct {
	Allowed bool
	GrantID string
	Denied  *GrantDenied
}

func (authorization GrantAuthorization) Err() error {
	if authorization.Allowed {
		return nil
	}
	return authorization.Denied
}

// EvaluateGrantAuthorization applies deny-by-default connector policy. Exact
// actions are required, resource globs are segment-scoped, foreign bindings
// never authorize, and revoked grants never regain authority.
func EvaluateGrantAuthorization(
	bindingID string,
	grants []*ConnectorGrant,
	action string,
	resource string,
) GrantAuthorization {
	deny := func(reason GrantDenyReason) GrantAuthorization {
		return GrantAuthorization{Denied: &GrantDenied{
			BindingID: bindingID,
			Action:    action,
			Resource:  resource,
			Reason:    reason,
		}}
	}

	activeCount := 0
	actionMatched := false
	revokedWouldMatch := false
	for _, grant := range grants {
		if grant == nil || grant.BindingID != bindingID {
			continue
		}
		matches := grant.Action == action && MatchGrantResource(grant.ResourcePattern, resource)
		if grant.RevokedAt != nil {
			if matches {
				revokedWouldMatch = true
			}
			continue
		}
		activeCount++
		if grant.Action != action {
			continue
		}
		actionMatched = true
		if matches {
			return GrantAuthorization{Allowed: true, GrantID: grant.GrantID}
		}
	}

	switch {
	case revokedWouldMatch:
		return deny(GrantDenyRevoked)
	case activeCount == 0:
		return deny(GrantDenyNoGrants)
	case !actionMatched:
		return deny(GrantDenyActionNotGranted)
	default:
		return deny(GrantDenyResourceNotGranted)
	}
}

// MatchGrantResource applies the deliberately small connector-grant grammar:
// '*' matches one slash-delimited segment and a trailing '**' matches one or
// more remaining segments. All other text is exact and case-sensitive.
func MatchGrantResource(pattern string, resource string) bool {
	if pattern == "" || resource == "" {
		return false
	}
	patternSegments := strings.Split(pattern, "/")
	resourceSegments := strings.Split(resource, "/")
	for index, patternSegment := range patternSegments {
		if patternSegment == "**" && index == len(patternSegments)-1 {
			return len(resourceSegments) > index
		}
		if index >= len(resourceSegments) {
			return false
		}
		if patternSegment != "*" && patternSegment != resourceSegments[index] {
			return false
		}
	}
	return len(resourceSegments) == len(patternSegments)
}

// irreversiblePreconditions is owner policy. Provider adapters implement the
// transport-specific freshness checks but cannot choose which actions require
// them.
var irreversiblePreconditions = map[string][]string{
	"github.merge":              {"expectedHeadSha"},
	"github.branch.delete":      {"expectedHeadSha"},
	"issues.set_priority":       {"expectedIssueRevision"},
	"slack.chat.delete":         {"expectedMessageTs"},
	"datadog.monitor.delete":    {"expectedMonitorRevision"},
	"datadog.monitor.mute":      {"expectedMonitorRevision"},
	"github.pull_request.close": {"expectedHeadSha"},
}

func IsIrreversibleAction(action string) bool {
	_, ok := irreversiblePreconditions[action]
	return ok
}

func RequiredActionPreconditions(action string) []string {
	fields, ok := irreversiblePreconditions[action]
	if !ok {
		return nil
	}
	return append([]string(nil), fields...)
}

// MissingActionPreconditions returns the owner-policy freshness fields absent
// from one dispatch command. An unknown registry field remains missing, so a
// policy addition cannot silently bypass the fail-closed gate.
func MissingActionPreconditions(action string, values DispatchPreconditions) []string {
	var missing []string
	for _, field := range RequiredActionPreconditions(action) {
		value := ""
		switch field {
		case "expectedHeadSha":
			value = values.ExpectedHeadSha
		case "expectedIssueRevision", "expectedMessageTs", "expectedMonitorRevision":
			value = values.ExpectedRevision
		}
		if value == "" {
			missing = append(missing, field)
		}
	}
	return missing
}
