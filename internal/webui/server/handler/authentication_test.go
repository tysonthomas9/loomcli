package handler

import (
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestClassifyAuthenticationAuthorityErrorHTTPProjection(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		ok     bool
	}{
		{name: "unauthenticated", err: authority.ErrUnauthenticated, status: http.StatusUnauthorized, code: "unauthenticated", ok: true},
		{name: "invalid admitted authority preserves generic forbidden contract", err: &authority.AdmissionError{Reason: authority.DenialInvalidAuthority}, status: http.StatusForbidden, code: "forbidden", ok: true},
		{name: "expired admitted authority preserves generic forbidden contract", err: &authority.AdmissionError{Reason: authority.DenialExpired}, status: http.StatusForbidden, code: "forbidden", ok: true},
		{name: "forbidden", err: authority.ErrActionNotAllowed, status: http.StatusForbidden, code: "forbidden", ok: true},
		{name: "capability specific fallthrough", err: workflowcatalog.ErrVersionOwnership, ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification, ok := ClassifyAuthenticationAuthorityError(test.err)
			if ok != test.ok {
				t.Fatalf("ClassifyAuthenticationAuthorityError(%v) ok = %v, want %v", test.err, ok, test.ok)
			}
			if classification.Status != test.status || classification.Code != test.code {
				t.Fatalf("ClassifyAuthenticationAuthorityError(%v) = %+v, want status=%d code=%q", test.err, classification, test.status, test.code)
			}
		})
	}
}
