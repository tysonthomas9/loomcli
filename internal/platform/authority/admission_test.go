package authority_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestOperatorOnlyAdmissionRejectsEveryOtherClass(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	admission, err := issuer.NewAdmission(
		authority.OperatorOnly("workflowcatalog.approve-version"),
		authority.OperatorOnly("workflowcatalog.unapprove-version"),
		authority.OperatorOnly("workflowcatalog.activate-version"),
	)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	authorities := []struct {
		name  string
		class authority.Class
		issue func(authority.VerifiedPrincipal) (authority.Authority, error)
		want  authority.DenialReason
	}{
		{
			name: "operator", class: authority.ClassOperator,
			issue: func(p authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueOperator(p, testWorkspace, testAction)
			},
		},
		{
			name: "execution", class: authority.ClassExecution, want: authority.DenialWrongClass,
			issue: func(p authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueExecution(p, testWorkspace, testAction)
			},
		},
		{
			name: "session", class: authority.ClassSession, want: authority.DenialWrongClass,
			issue: func(p authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueSession(p, testWorkspace, testAction)
			},
		},
		{
			name: "webhook", class: authority.ClassWebhook, want: authority.DenialWrongClass,
			issue: func(p authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueWebhook(p, testWorkspace, testAction)
			},
		},
		{
			name: "system", class: authority.ClassSystem, want: authority.DenialWrongClass,
			issue: func(p authority.VerifiedPrincipal) (authority.Authority, error) {
				return issuer.IssueSystem(p, testWorkspace, testAction, "internal reconciliation")
			},
		},
	}

	for _, tt := range authorities {
		t.Run(tt.name, func(t *testing.T) {
			principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
				Subject: tt.name, Class: tt.class, Workspace: testWorkspace,
				Actions: []authority.Action{testAction}, ExpiresAt: now.Add(time.Hour),
			})
			value, err := tt.issue(principal)
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			err = admission.Admit(testAction, testWorkspace, value)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("operator rejected: %v", err)
				}
				operator := value.(authority.OperatorAuthority)
				if err := admission.RequireOperator(testAction, testWorkspace, operator); err != nil {
					t.Fatalf("RequireOperator: %v", err)
				}
				return
			}
			assertDenial(t, err, tt.want)
		})
	}
}

func TestAuthorityTypesAreNotConvertible(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(authority.OperatorAuthority{}),
		reflect.TypeOf(authority.ExecutionAuthority{}),
		reflect.TypeOf(authority.SessionAuthority{}),
		reflect.TypeOf(authority.WebhookAuthority{}),
		reflect.TypeOf(authority.SystemAuthority{}),
	}
	for i := range types {
		for j := range types {
			if i != j && types[i].ConvertibleTo(types[j]) {
				t.Errorf("%v is convertible to %v", types[i], types[j])
			}
		}
	}
}

func TestAdmissionIsDefaultDenyAndScopeBound(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	const otherAction = authority.Action("workflowcatalog.activate-version")
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction, otherAction}, ExpiresAt: now.Add(time.Hour),
	})
	operator, err := issuer.IssueOperator(principal, testWorkspace, testAction)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	admission, err := issuer.NewAdmission(authority.OperatorOnly(testAction), authority.OperatorOnly(otherAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	tests := []struct {
		name      string
		action    authority.Action
		workspace string
		value     authority.Authority
		want      authority.DenialReason
	}{
		{name: "unknown operation", action: "workflowcatalog.delete-version", workspace: testWorkspace, value: operator, want: authority.DenialUnknownOperation},
		{name: "empty operation", action: " ", workspace: testWorkspace, value: operator, want: authority.DenialUnknownOperation},
		{name: "wildcard operation", action: "*", workspace: testWorkspace, value: operator, want: authority.DenialUnknownOperation},
		{name: "empty workspace", action: testAction, workspace: " ", value: operator, want: authority.DenialWrongWorkspace},
		{name: "wrong workspace", action: testAction, workspace: "workspace-b", value: operator, want: authority.DenialWrongWorkspace},
		{name: "wrong action", action: otherAction, workspace: testWorkspace, value: operator, want: authority.DenialWrongAction},
		{name: "zero authority", action: testAction, workspace: testWorkspace, value: authority.OperatorAuthority{}, want: authority.DenialInvalidAuthority},
		{name: "authority pointer", action: testAction, workspace: testWorkspace, value: &operator, want: authority.DenialInvalidAuthority},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDenial(t, admission.Admit(tt.action, tt.workspace, tt.value), tt.want)
		})
	}
}

func TestAdmissionRejectsExpiredAndForeignIssuerAuthority(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)
	issuer := newTestIssuer(t, &now)
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: expiresAt,
	})
	operator, err := issuer.IssueOperator(principal, testWorkspace, testAction)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	admission, err := issuer.NewAdmission(authority.OperatorOnly(testAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	otherIssuer := newTestIssuer(t, &now)
	foreignPrincipal := derivePrincipal(t, otherIssuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: expiresAt,
	})
	foreign, err := otherIssuer.IssueOperator(foreignPrincipal, testWorkspace, testAction)
	if err != nil {
		t.Fatalf("foreign IssueOperator: %v", err)
	}
	assertDenial(t, admission.Admit(testAction, testWorkspace, foreign), authority.DenialInvalidAuthority)

	now = expiresAt
	assertDenial(t, admission.Admit(testAction, testWorkspace, operator), authority.DenialExpired)
}

func TestOperationRuleValidationAndDefensiveCopy(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	tests := []struct {
		name  string
		rules []authority.OperationRule
	}{
		{name: "empty action", rules: []authority.OperationRule{authority.OperatorOnly(" ")}},
		{name: "wildcard action", rules: []authority.OperationRule{authority.OperatorOnly("*")}},
		{name: "no class", rules: []authority.OperationRule{authority.Allow(testAction)}},
		{name: "unknown class", rules: []authority.OperationRule{authority.Allow(testAction, "root")}},
		{name: "duplicate normalized action", rules: []authority.OperationRule{authority.OperatorOnly(testAction), authority.OperatorOnly(" workflowcatalog.approve-version ")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := issuer.NewAdmission(tt.rules...); !errors.Is(err, authority.ErrInvalidOperationRule) {
				t.Fatalf("error = %v, want ErrInvalidOperationRule", err)
			}
		})
	}

	rule := authority.OperatorOnly(testAction)
	admission, err := issuer.NewAdmission(rule)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	rule.Allowed[0] = authority.ClassExecution
	principal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: now.Add(time.Hour),
	})
	operator, err := issuer.IssueOperator(principal, testWorkspace, testAction)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	if err := admission.RequireOperator(testAction, testWorkspace, operator); err != nil {
		t.Fatalf("rule mutation changed admission: %v", err)
	}
}

func TestEmptyRegistryDeniesEveryOperation(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	admission, err := issuer.NewAdmission()
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	assertDenial(t, admission.Admit(testAction, testWorkspace, authority.OperatorAuthority{}), authority.DenialUnknownOperation)
}

func assertDenial(t *testing.T, err error, reason authority.DenialReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("admission succeeded, want %s", reason)
	}
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("error = %v, want ErrAdmissionDenied", err)
	}
	var denial *authority.AdmissionError
	if !errors.As(err, &denial) {
		t.Fatalf("error type = %T, want *AdmissionError", err)
	}
	if denial.Reason != reason {
		t.Fatalf("denial reason = %q, want %q (error %v)", denial.Reason, reason, err)
	}
}
