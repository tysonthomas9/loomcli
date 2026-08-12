package authority_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	testWorkspace = "workspace-a"
	testSubject   = "operator-alice"
	testAction    = authority.Action("workflowcatalog.approve-version")
)

func TestIssuerDerivesEveryTypedAuthority(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)
	issuer := newTestIssuer(t, &now)

	tests := []struct {
		name  string
		class authority.Class
		issue func(authority.VerifiedPrincipal) (authority.Authority, error)
		check func(authority.Authority)
	}{
		{
			name:  "operator",
			class: authority.ClassOperator,
			issue: func(principal authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueOperator(principal, testWorkspace, testAction)
			},
			check: func(value authority.Authority) {
				got, ok := value.(authority.OperatorAuthority)
				if !ok {
					t.Fatalf("authority type = %T, want OperatorAuthority", value)
				}
				assertScope(t, got.Subject(), got.Workspace(), got.Action(), got.ExpiresAt(), testSubject, expiresAt)
			},
		},
		{
			name:  "execution",
			class: authority.ClassExecution,
			issue: func(principal authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueExecution(principal, testWorkspace, testAction)
			},
			check: func(value authority.Authority) {
				got, ok := value.(authority.ExecutionAuthority)
				if !ok {
					t.Fatalf("authority type = %T, want ExecutionAuthority", value)
				}
				assertScope(t, got.Subject(), got.Workspace(), got.Action(), got.ExpiresAt(), testSubject, expiresAt)
			},
		},
		{
			name:  "session",
			class: authority.ClassSession,
			issue: func(principal authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueSession(principal, testWorkspace, testAction)
			},
			check: func(value authority.Authority) {
				got, ok := value.(authority.SessionAuthority)
				if !ok {
					t.Fatalf("authority type = %T, want SessionAuthority", value)
				}
				assertScope(t, got.Subject(), got.Workspace(), got.Action(), got.ExpiresAt(), testSubject, expiresAt)
			},
		},
		{
			name:  "webhook",
			class: authority.ClassWebhook,
			issue: func(principal authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueWebhook(principal, testWorkspace, testAction)
			},
			check: func(value authority.Authority) {
				got, ok := value.(authority.WebhookAuthority)
				if !ok {
					t.Fatalf("authority type = %T, want WebhookAuthority", value)
				}
				assertScope(t, got.Subject(), got.Workspace(), got.Action(), got.ExpiresAt(), testSubject, expiresAt)
			},
		},
		{
			name:  "system",
			class: authority.ClassSystem,
			issue: func(principal authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueSystem(principal, testWorkspace, testAction, "refresh built-in catalog")
			},
			check: func(value authority.Authority) {
				got, ok := value.(authority.SystemAuthority)
				if !ok {
					t.Fatalf("authority type = %T, want SystemAuthority", value)
				}
				assertScope(t, got.Subject(), got.Workspace(), got.Action(), got.ExpiresAt(), testSubject, expiresAt)
				if got.Reason() != "refresh built-in catalog" {
					t.Fatalf("Reason = %q", got.Reason())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
				Subject: testSubject, Class: tt.class, Workspace: testWorkspace,
				Actions: []authority.Action{testAction}, ExpiresAt: expiresAt,
			})
			if principal.Subject() != testSubject || principal.Class() != tt.class || principal.Workspace() != testWorkspace || !principal.ExpiresAt().Equal(expiresAt) {
				t.Fatalf("principal scope = subject %q class %q workspace %q expiry %v", principal.Subject(), principal.Class(), principal.Workspace(), principal.ExpiresAt())
			}
			value, err := tt.issue(principal)
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			tt.check(value)
		})
	}
}

func TestDeriveVerifiedPrincipalRejectsInvalidClaims(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	valid := authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: now.Add(time.Hour),
	}

	tests := []struct {
		name string
		edit func(*authority.PrincipalClaims)
		want error
	}{
		{name: "empty subject", edit: func(v *authority.PrincipalClaims) { v.Subject = " " }, want: authority.ErrInvalidPrincipal},
		{name: "unknown class", edit: func(v *authority.PrincipalClaims) { v.Class = "root" }, want: authority.ErrInvalidPrincipal},
		{name: "empty workspace", edit: func(v *authority.PrincipalClaims) { v.Workspace = " " }, want: authority.ErrInvalidScope},
		{name: "no actions", edit: func(v *authority.PrincipalClaims) { v.Actions = nil }, want: authority.ErrInvalidPrincipal},
		{name: "empty action", edit: func(v *authority.PrincipalClaims) { v.Actions = []authority.Action{" "} }, want: authority.ErrInvalidPrincipal},
		{name: "wildcard action", edit: func(v *authority.PrincipalClaims) { v.Actions = []authority.Action{"*"} }, want: authority.ErrInvalidPrincipal},
		{name: "zero expiry", edit: func(v *authority.PrincipalClaims) { v.ExpiresAt = time.Time{} }, want: authority.ErrInvalidPrincipal},
		{name: "expiry is now", edit: func(v *authority.PrincipalClaims) { v.ExpiresAt = now }, want: authority.ErrInvalidPrincipal},
		{name: "past expiry", edit: func(v *authority.PrincipalClaims) { v.ExpiresAt = now.Add(-time.Second) }, want: authority.ErrInvalidPrincipal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := valid
			claims.Actions = append([]authority.Action(nil), valid.Actions...)
			tt.edit(&claims)
			_, err := issuer.DeriveVerifiedPrincipal(claims)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestIssueValidatesIssuerClassWorkspaceActionAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	otherIssuer := newTestIssuer(t, &now)
	expiresAt := now.Add(time.Hour)
	operator := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: expiresAt,
	})
	execution := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: "run-1", Class: authority.ClassExecution, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: expiresAt,
	})
	foreign := derivePrincipal(t, otherIssuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: expiresAt,
	})

	tests := []struct {
		name      string
		principal authority.VerifiedPrincipal
		workspace string
		action    authority.Action
		want      error
	}{
		{name: "zero principal", workspace: testWorkspace, action: testAction, want: authority.ErrInvalidPrincipal},
		{name: "foreign issuer", principal: foreign, workspace: testWorkspace, action: testAction, want: authority.ErrInvalidPrincipal},
		{name: "wrong class", principal: execution, workspace: testWorkspace, action: testAction, want: authority.ErrPrincipalClass},
		{name: "empty workspace", principal: operator, workspace: " ", action: testAction, want: authority.ErrInvalidScope},
		{name: "wrong workspace", principal: operator, workspace: "workspace-b", action: testAction, want: authority.ErrWorkspaceMismatch},
		{name: "empty action", principal: operator, workspace: testWorkspace, action: " ", want: authority.ErrInvalidScope},
		{name: "wildcard action", principal: operator, workspace: testWorkspace, action: "*", want: authority.ErrInvalidScope},
		{name: "ungranted action", principal: operator, workspace: testWorkspace, action: "workflowcatalog.activate-version", want: authority.ErrActionNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := issuer.IssueOperator(tt.principal, tt.workspace, tt.action)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}

	now = expiresAt
	if _, err := issuer.IssueOperator(operator, testWorkspace, testAction); !errors.Is(err, authority.ErrPrincipalExpired) {
		t.Fatalf("expired issue error = %v, want ErrPrincipalExpired", err)
	}
}

func TestIssueSystemRequiresAuditReason(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: "catalog-reconciler", Class: authority.ClassSystem, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: now.Add(time.Hour),
	})
	if _, err := issuer.IssueSystem(principal, testWorkspace, testAction, " "); !errors.Is(err, authority.ErrInvalidScope) {
		t.Fatalf("empty reason error = %v, want ErrInvalidScope", err)
	}
}

func TestPrincipalClaimsAreDefensivelyCopied(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	actions := []authority.Action{testAction}
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: actions, ExpiresAt: now.Add(time.Hour),
	})
	actions[0] = "workflowcatalog.activate-version"
	if _, err := issuer.IssueOperator(principal, testWorkspace, testAction); err != nil {
		t.Fatalf("original action disappeared after caller mutation: %v", err)
	}
	if _, err := issuer.IssueOperator(principal, testWorkspace, actions[0]); !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("mutated action error = %v, want ErrActionNotAllowed", err)
	}
}

func TestIssuerZeroAndNilClockFailClosed(t *testing.T) {
	if _, err := authority.NewIssuerWithClock(nil); !errors.Is(err, authority.ErrInvalidIssuer) {
		t.Fatalf("nil clock error = %v, want ErrInvalidIssuer", err)
	}
	var issuer authority.Issuer
	if _, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{}); !errors.Is(err, authority.ErrInvalidIssuer) {
		t.Fatalf("zero issuer error = %v, want ErrInvalidIssuer", err)
	}
}

func newTestIssuer(t *testing.T, now *time.Time) *authority.Issuer {
	t.Helper()
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return *now })
	if err != nil {
		t.Fatalf("NewIssuerWithClock: %v", err)
	}
	return issuer
}

func derivePrincipal(t *testing.T, issuer *authority.Issuer, claims authority.PrincipalClaims) authority.VerifiedPrincipal {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(claims)
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	return principal
}

func assertScope(t *testing.T, subject, workspace string, action authority.Action, expiresAt time.Time, wantSubject string, wantExpiry time.Time) {
	t.Helper()
	if subject != wantSubject || workspace != testWorkspace || action != testAction || !expiresAt.Equal(wantExpiry) {
		t.Fatalf("scope = subject %q workspace %q action %q expiry %v", subject, workspace, action, expiresAt)
	}
}
