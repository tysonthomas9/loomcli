package authority_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestClassifyAuthenticationAuthorityError(t *testing.T) {
	var nilAdmission *authority.AdmissionError
	tests := []struct {
		name  string
		err   error
		class authority.AuthenticationAuthorityErrorClass
		ok    bool
	}{
		{name: "unauthenticated", err: authority.ErrUnauthenticated, class: authority.AuthenticationAuthorityErrorUnauthenticated, ok: true},
		{name: "wrapped invalid principal", err: fmt.Errorf("verify: %w", authority.ErrInvalidPrincipal), class: authority.AuthenticationAuthorityErrorUnauthenticated, ok: true},
		{name: "expired principal", err: authority.ErrPrincipalExpired, class: authority.AuthenticationAuthorityErrorUnauthenticated, ok: true},
		{name: "opaque authority", err: authority.ErrOpaqueAuthority, class: authority.AuthenticationAuthorityErrorUnauthenticated, ok: true},
		{name: "invalid admitted authority", err: &authority.AdmissionError{Reason: authority.DenialInvalidAuthority}, class: authority.AuthenticationAuthorityErrorInvalidAdmission, ok: true},
		{name: "expired admitted authority", err: &authority.AdmissionError{Reason: authority.DenialExpired}, class: authority.AuthenticationAuthorityErrorInvalidAdmission, ok: true},
		{name: "wrong admitted class", err: &authority.AdmissionError{Reason: authority.DenialWrongClass}, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "wrong admitted action", err: &authority.AdmissionError{Reason: authority.DenialWrongAction}, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "wrong admitted workspace", err: &authority.AdmissionError{Reason: authority.DenialWrongWorkspace}, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "unknown admitted operation", err: &authority.AdmissionError{Reason: authority.DenialUnknownOperation}, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "generic admission denial", err: authority.ErrAdmissionDenied, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "typed nil admission denial", err: nilAdmission, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "workspace mismatch", err: authority.ErrWorkspaceMismatch, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "principal class", err: authority.ErrPrincipalClass, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "action not allowed", err: authority.ErrActionNotAllowed, class: authority.AuthenticationAuthorityErrorForbidden, ok: true},
		{name: "capability error falls through", err: errors.New("capability conflict"), class: authority.AuthenticationAuthorityErrorUnknown, ok: false},
		{name: "nil falls through", err: nil, class: authority.AuthenticationAuthorityErrorUnknown, ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			class, ok := authority.ClassifyAuthenticationAuthorityError(test.err)
			if class != test.class || ok != test.ok {
				t.Fatalf("ClassifyAuthenticationAuthorityError(%v) = (%v, %v), want (%v, %v)", test.err, class, ok, test.class, test.ok)
			}
		})
	}
}
