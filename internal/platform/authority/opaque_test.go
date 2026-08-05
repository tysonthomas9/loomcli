package authority_test

import (
	"encoding"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestAuthorityAndPrincipalRejectWireSerialization(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	expiresAt := now.Add(time.Hour)

	operatorPrincipal := derivePrincipal(t, issuer, authority.PrincipalClaims{
		Subject: testSubject, Class: authority.ClassOperator, Workspace: testWorkspace,
		Actions: []authority.Action{testAction}, ExpiresAt: expiresAt,
	})
	operator, err := issuer.IssueOperator(operatorPrincipal, testWorkspace, testAction)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}

	values := []struct {
		name   string
		value  any
		target func() any
	}{
		{name: "principal", value: operatorPrincipal, target: func() any { return &authority.VerifiedPrincipal{} }},
		{name: "operator", value: operator, target: func() any { return &authority.OperatorAuthority{} }},
		{name: "execution", value: authority.ExecutionAuthority{}, target: func() any { return &authority.ExecutionAuthority{} }},
		{name: "session", value: authority.SessionAuthority{}, target: func() any { return &authority.SessionAuthority{} }},
		{name: "webhook", value: authority.WebhookAuthority{}, target: func() any { return &authority.WebhookAuthority{} }},
		{name: "system", value: authority.SystemAuthority{}, target: func() any { return &authority.SystemAuthority{} }},
	}

	for _, tt := range values {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := json.Marshal(tt.value); !errors.Is(err, authority.ErrOpaqueAuthority) {
				t.Fatalf("json.Marshal error = %v, want ErrOpaqueAuthority", err)
			}
			target := tt.target()
			if err := json.Unmarshal([]byte(`{}`), target); !errors.Is(err, authority.ErrOpaqueAuthority) {
				t.Fatalf("json.Unmarshal error = %v, want ErrOpaqueAuthority", err)
			}
			marshaler, ok := tt.value.(encoding.TextMarshaler)
			if !ok {
				t.Fatalf("%T does not implement encoding.TextMarshaler", tt.value)
			}
			if _, err := marshaler.MarshalText(); !errors.Is(err, authority.ErrOpaqueAuthority) {
				t.Fatalf("MarshalText error = %v, want ErrOpaqueAuthority", err)
			}
			unmarshaler, ok := target.(encoding.TextUnmarshaler)
			if !ok {
				t.Fatalf("%T does not implement encoding.TextUnmarshaler", target)
			}
			if err := unmarshaler.UnmarshalText([]byte("forged")); !errors.Is(err, authority.ErrOpaqueAuthority) {
				t.Fatalf("UnmarshalText error = %v, want ErrOpaqueAuthority", err)
			}
		})
	}
}

func TestRequestDTOCannotDeserializeAuthority(t *testing.T) {
	type requestDTO struct {
		Workspace string                      `json:"workspace"`
		Authority authority.OperatorAuthority `json:"authority"`
	}
	var request requestDTO
	if err := json.Unmarshal([]byte(`{"workspace":"workspace-a","authority":{}}`), &request); !errors.Is(err, authority.ErrOpaqueAuthority) {
		t.Fatalf("json.Unmarshal error = %v, want ErrOpaqueAuthority", err)
	}
}

func TestOmittedAuthorityRemainsInvalid(t *testing.T) {
	type requestDTO struct {
		Workspace string                      `json:"workspace"`
		Authority authority.OperatorAuthority `json:"authority,omitempty"`
	}
	var request requestDTO
	if err := json.Unmarshal([]byte(`{"workspace":"workspace-a"}`), &request); err != nil {
		t.Fatalf("json.Unmarshal omitted authority: %v", err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer := newTestIssuer(t, &now)
	admission, err := issuer.NewAdmission(authority.OperatorOnly(testAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	assertDenial(t, admission.RequireOperator(testAction, request.Workspace, request.Authority), authority.DenialInvalidAuthority)
}
