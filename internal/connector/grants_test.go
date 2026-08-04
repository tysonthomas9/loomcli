package connector

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func grant(grantID, bindingID, action, pattern string) *domain.ConnectorGrant {
	return &domain.ConnectorGrant{
		WorkspaceKey:    "ws-1",
		GrantID:         grantID,
		ConnectorID:     "github-prod",
		BindingID:       bindingID,
		Action:          action,
		ResourcePattern: pattern,
		CreatedAt:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

func revokedGrant(grantID, bindingID, action, pattern string) *domain.ConnectorGrant {
	g := grant(grantID, bindingID, action, pattern)
	at := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	g.RevokedAt = &at
	return g
}

// Grant fixtures mirroring the security.html S4 examples: a review agent
// holding read+review on one repo, and a merge agent holding merge:write on
// an explicit repo list.
var (
	reviewAgentGrants = []*domain.ConnectorGrant{
		grant("g-pr-read", "b-review", "github.pull_request.read", "repo:octocat/hello"),
		grant("g-review-write", "b-review", "github.review.write", "repo:octocat/hello"),
	}
	mergeAgentGrants = []*domain.ConnectorGrant{
		grant("g-merge-hello", "b-merge", "github.merge", "repo:octocat/hello"),
		grant("g-merge-world", "b-merge", "github.merge", "repo:octocat/world"),
	}
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name        string
		bindingID   string
		grants      []*domain.ConnectorGrant
		action      string
		resource    string
		wantAllowed bool
		wantGrantID string
		wantReason  DenyReason
	}{
		{
			name:        "review agent may read pull requests on its repo",
			bindingID:   "b-review",
			grants:      reviewAgentGrants,
			action:      "github.pull_request.read",
			resource:    "repo:octocat/hello",
			wantAllowed: true,
			wantGrantID: "g-pr-read",
		},
		{
			name:        "review agent may write reviews on its repo",
			bindingID:   "b-review",
			grants:      reviewAgentGrants,
			action:      "github.review.write",
			resource:    "repo:octocat/hello",
			wantAllowed: true,
			wantGrantID: "g-review-write",
		},
		{
			name:       "review agent may NOT merge (action not granted)",
			bindingID:  "b-review",
			grants:     reviewAgentGrants,
			action:     "github.merge",
			resource:   "repo:octocat/hello",
			wantReason: DenyReasonActionNotGranted,
		},
		{
			name:        "merge agent may merge a listed repo",
			bindingID:   "b-merge",
			grants:      mergeAgentGrants,
			action:      "github.merge",
			resource:    "repo:octocat/world",
			wantAllowed: true,
			wantGrantID: "g-merge-world",
		},
		{
			name:       "merge agent cross-repo deny",
			bindingID:  "b-merge",
			grants:     mergeAgentGrants,
			action:     "github.merge",
			resource:   "repo:evil/other",
			wantReason: DenyReasonResourceNotGranted,
		},
		{
			name:       "deny-by-default with empty grants",
			bindingID:  "b-review",
			grants:     nil,
			action:     "github.pull_request.read",
			resource:   "repo:octocat/hello",
			wantReason: DenyReasonNoGrants,
		},
		{
			name:      "revoked grant deny",
			bindingID: "b-merge",
			grants: []*domain.ConnectorGrant{
				revokedGrant("g-merge-hello", "b-merge", "github.merge", "repo:octocat/hello"),
			},
			action:     "github.merge",
			resource:   "repo:octocat/hello",
			wantReason: DenyReasonGrantRevoked,
		},
		{
			name:      "revoked reason wins over generic resource mismatch",
			bindingID: "b-merge",
			grants: []*domain.ConnectorGrant{
				grant("g-merge-world", "b-merge", "github.merge", "repo:octocat/world"),
				revokedGrant("g-merge-hello", "b-merge", "github.merge", "repo:octocat/hello"),
			},
			action:     "github.merge",
			resource:   "repo:octocat/hello",
			wantReason: DenyReasonGrantRevoked,
		},
		{
			name:      "active grant still wins when an unrelated revoked grant exists",
			bindingID: "b-merge",
			grants: []*domain.ConnectorGrant{
				revokedGrant("g-old", "b-merge", "github.merge", "repo:octocat/hello"),
				grant("g-new", "b-merge", "github.merge", "repo:octocat/hello"),
			},
			action:      "github.merge",
			resource:    "repo:octocat/hello",
			wantAllowed: true,
			wantGrantID: "g-new",
		},
		{
			name:       "grants for a different binding never authorize",
			bindingID:  "b-review",
			grants:     mergeAgentGrants,
			action:     "github.merge",
			resource:   "repo:octocat/hello",
			wantReason: DenyReasonNoGrants,
		},
		{
			name:      "glob grant authorizes any repo under the owner",
			bindingID: "b-review",
			grants: []*domain.ConnectorGrant{
				grant("g-owner-read", "b-review", "github.pull_request.read", "repo:octocat/*"),
			},
			action:      "github.pull_request.read",
			resource:    "repo:octocat/hello",
			wantAllowed: true,
			wantGrantID: "g-owner-read",
		},
		{
			name:      "glob grant does not cross owners",
			bindingID: "b-review",
			grants: []*domain.ConnectorGrant{
				grant("g-owner-read", "b-review", "github.pull_request.read", "repo:octocat/*"),
			},
			action:     "github.pull_request.read",
			resource:   "repo:evil/hello",
			wantReason: DenyReasonResourceNotGranted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.bindingID, tt.grants, tt.action, tt.resource)
			if got.Allowed != tt.wantAllowed {
				t.Fatalf("Allowed = %v, want %v (decision %+v)", got.Allowed, tt.wantAllowed, got)
			}
			if tt.wantAllowed {
				if got.GrantID != tt.wantGrantID {
					t.Fatalf("GrantID = %q, want %q", got.GrantID, tt.wantGrantID)
				}
				if got.Denied != nil {
					t.Fatalf("Denied = %+v, want nil on allow", got.Denied)
				}
				if err := got.Err(); err != nil {
					t.Fatalf("Err() = %v, want nil on allow", err)
				}
				return
			}
			if got.Denied == nil {
				t.Fatalf("Denied = nil on deny")
			}
			if got.Denied.Reason != tt.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Denied.Reason, tt.wantReason)
			}
			if got.Denied.BindingID != tt.bindingID || got.Denied.Action != tt.action || got.Denied.Resource != tt.resource {
				t.Fatalf("Denied tuple = %+v, want {%s %s %s}", got.Denied, tt.bindingID, tt.action, tt.resource)
			}
			err := got.Err()
			if !errors.Is(err, domain.ErrGrantDenied) {
				t.Fatalf("Err() = %v, want wrap of domain.ErrGrantDenied", err)
			}
			wantRevoked := tt.wantReason == DenyReasonGrantRevoked
			if errors.Is(err, domain.ErrGrantRevoked) != wantRevoked {
				t.Fatalf("errors.Is(err, ErrGrantRevoked) = %v, want %v", !wantRevoked, wantRevoked)
			}
		})
	}
}

func TestMatchResource(t *testing.T) {
	tests := []struct {
		pattern  string
		resource string
		want     bool
	}{
		{"repo:octocat/hello", "repo:octocat/hello", true},
		{"repo:octocat/hello", "repo:octocat/world", false},
		{"repo:octocat/hello", "repo:Octocat/Hello", false}, // case-sensitive
		{"repo:octocat/*", "repo:octocat/hello", true},
		{"repo:octocat/*", "repo:octocat/hello/issues/7", false}, // * is one segment
		{"repo:octocat/*", "repo:octocat", false},
		{"repo:octocat/**", "repo:octocat/hello", true},
		{"repo:octocat/**", "repo:octocat/hello/issues/7", true},
		{"repo:octocat/**", "repo:octocat", false}, // ** needs >= 1 segment
		{"**", "repo:octocat/hello", true},
		{"*", "repo:octocat", true},
		{"*", "repo:octocat/hello", false},
		{"repo:octo*", "repo:octocat", false},    // no in-segment wildcards
		{"repo:**/hello", "repo:x/hello", false}, // non-final ** is a literal
		{"", "repo:octocat/hello", false},
		{"repo:octocat/hello", "", false},
		{"channel:C123", "channel:C123", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"~"+tt.resource, func(t *testing.T) {
			if got := MatchResource(tt.pattern, tt.resource); got != tt.want {
				t.Fatalf("MatchResource(%q, %q) = %v, want %v", tt.pattern, tt.resource, got, tt.want)
			}
		})
	}
}

func TestIrreversibleRegistry(t *testing.T) {
	tests := []struct {
		action     string
		wantFields []string
	}{
		{"github.merge", []string{"expectedHeadSha"}},
		{"issues.set_priority", []string{"expectedIssueRevision"}},
		{"slack.chat.delete", []string{"expectedMessageTs"}},
		{"datadog.monitor.delete", []string{"expectedMonitorRevision"}},
		{"github.pull_request.read", nil}, // reversible: not registered
		{"slack.chat.post_message", nil},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			wantIrreversible := tt.wantFields != nil
			if got := IsIrreversible(tt.action); got != wantIrreversible {
				t.Fatalf("IsIrreversible(%q) = %v, want %v", tt.action, got, wantIrreversible)
			}
			got := RequiredPreconditions(tt.action)
			if len(got) != len(tt.wantFields) {
				t.Fatalf("RequiredPreconditions(%q) = %v, want %v", tt.action, got, tt.wantFields)
			}
			for i := range got {
				if got[i] != tt.wantFields[i] {
					t.Fatalf("RequiredPreconditions(%q) = %v, want %v", tt.action, got, tt.wantFields)
				}
			}
		})
	}

	// Returned slices are copies: mutating one must not poison the registry.
	fields := RequiredPreconditions("github.merge")
	fields[0] = "mutated"
	if got := RequiredPreconditions("github.merge"); got[0] != "expectedHeadSha" {
		t.Fatalf("registry mutated through returned slice: %v", got)
	}
}
