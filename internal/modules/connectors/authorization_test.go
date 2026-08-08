package connectors

import (
	"errors"
	"testing"
	"time"
)

func authorizationGrant(id string, binding string, action string, resource string) *ConnectorGrant {
	return &ConnectorGrant{
		WorkspaceKey:    "WS",
		GrantID:         id,
		ConnectorID:     "github-prod",
		BindingID:       binding,
		Action:          action,
		ResourcePattern: resource,
		CreatedAt:       time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestEvaluateGrantAuthorization(t *testing.T) {
	revoked := authorizationGrant("revoked", "binding", "github.merge", "repo:octocat/hello")
	revokedAt := time.Date(2026, time.June, 2, 0, 0, 0, 0, time.UTC)
	revoked.RevokedAt = &revokedAt

	tests := []struct {
		name      string
		binding   string
		grants    []*ConnectorGrant
		action    string
		resource  string
		grantID   string
		deny      GrantDenyReason
		revokedIs bool
	}{
		{
			name:    "exact grant allows",
			binding: "binding",
			grants: []*ConnectorGrant{authorizationGrant(
				"grant", "binding", "github.merge", "repo:octocat/hello",
			)},
			action: "github.merge", resource: "repo:octocat/hello", grantID: "grant",
		},
		{
			name:    "single segment glob allows",
			binding: "binding",
			grants: []*ConnectorGrant{authorizationGrant(
				"grant", "binding", "github.pull_request.read", "repo:octocat/*",
			)},
			action: "github.pull_request.read", resource: "repo:octocat/hello", grantID: "grant",
		},
		{name: "empty denies", binding: "binding", action: "github.merge", resource: "repo:octocat/hello", deny: GrantDenyNoGrants},
		{
			name:    "foreign binding denies",
			binding: "binding",
			grants: []*ConnectorGrant{authorizationGrant(
				"grant", "other", "github.merge", "repo:octocat/hello",
			)},
			action: "github.merge", resource: "repo:octocat/hello", deny: GrantDenyNoGrants,
		},
		{
			name:    "action denies",
			binding: "binding",
			grants: []*ConnectorGrant{authorizationGrant(
				"grant", "binding", "github.review.write", "repo:octocat/hello",
			)},
			action: "github.merge", resource: "repo:octocat/hello", deny: GrantDenyActionNotGranted,
		},
		{
			name:    "resource denies",
			binding: "binding",
			grants: []*ConnectorGrant{authorizationGrant(
				"grant", "binding", "github.merge", "repo:octocat/world",
			)},
			action: "github.merge", resource: "repo:octocat/hello", deny: GrantDenyResourceNotGranted,
		},
		{
			name:    "matching revocation is specific",
			binding: "binding", grants: []*ConnectorGrant{revoked},
			action: "github.merge", resource: "repo:octocat/hello", deny: GrantDenyRevoked, revokedIs: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := EvaluateGrantAuthorization(test.binding, test.grants, test.action, test.resource)
			if test.grantID != "" {
				if !got.Allowed || got.GrantID != test.grantID || got.Err() != nil {
					t.Fatalf("authorization = %+v, want grant %q", got, test.grantID)
				}
				return
			}
			if got.Allowed || got.Denied == nil || got.Denied.Reason != test.deny {
				t.Fatalf("authorization = %+v, want denial %q", got, test.deny)
			}
			if !errors.Is(got.Err(), ErrGrantDenied) {
				t.Fatalf("error = %v, want ErrGrantDenied", got.Err())
			}
			if errors.Is(got.Err(), ErrGrantRevoked) != test.revokedIs {
				t.Fatalf("revoked match = %v, want %v", errors.Is(got.Err(), ErrGrantRevoked), test.revokedIs)
			}
		})
	}
}

func TestMatchGrantResource(t *testing.T) {
	tests := []struct {
		pattern  string
		resource string
		want     bool
	}{
		{"repo:octocat/hello", "repo:octocat/hello", true},
		{"repo:octocat/*", "repo:octocat/hello", true},
		{"repo:octocat/*", "repo:octocat/hello/issues/7", false},
		{"repo:octocat/**", "repo:octocat/hello/issues/7", true},
		{"repo:octocat/**", "repo:octocat", false},
		{"repo:octo*", "repo:octocat", false},
		{"", "repo:octocat/hello", false},
	}
	for _, test := range tests {
		if got := MatchGrantResource(test.pattern, test.resource); got != test.want {
			t.Errorf("MatchGrantResource(%q, %q) = %v, want %v", test.pattern, test.resource, got, test.want)
		}
	}
}

func TestRequiredActionPreconditionsReturnsCopy(t *testing.T) {
	if !IsIrreversibleAction("github.merge") || IsIrreversibleAction("github.pull_request.read") {
		t.Fatal("irreversible-action registry disagrees with policy")
	}
	fields := RequiredActionPreconditions("github.merge")
	fields[0] = "mutated"
	if got := RequiredActionPreconditions("github.merge"); len(got) != 1 || got[0] != "expectedHeadSha" {
		t.Fatalf("registry mutated through returned slice: %v", got)
	}
	for action := range irreversiblePreconditions {
		if normalized, err := normalizeConnectorAction(action); err != nil || normalized != action {
			t.Fatalf("registry action %q is invalid: %v", action, err)
		}
	}
}

func TestMissingActionPreconditions(t *testing.T) {
	if got := MissingActionPreconditions("github.pull_request.read", DispatchPreconditions{}); got != nil {
		t.Fatalf("reversible action missing = %v, want nil", got)
	}
	if got := MissingActionPreconditions("github.merge", DispatchPreconditions{}); len(got) != 1 || got[0] != "expectedHeadSha" {
		t.Fatalf("missing = %v, want expectedHeadSha", got)
	}
	if got := MissingActionPreconditions(
		"github.merge",
		DispatchPreconditions{ExpectedHeadSha: "sha"},
	); len(got) != 0 {
		t.Fatalf("present precondition reported missing: %v", got)
	}
	for action, fields := range irreversiblePreconditions {
		full := DispatchPreconditions{ExpectedHeadSha: "sha", ExpectedRevision: "revision"}
		if got := MissingActionPreconditions(action, full); len(got) != 0 {
			t.Fatalf("action %q fields %v have no DispatchPreconditions mapping: %v", action, fields, got)
		}
	}
}
