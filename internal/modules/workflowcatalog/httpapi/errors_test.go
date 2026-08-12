package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestClassifyErrorHasStableCatalogAndAuthorityMappings(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "unauthenticated", err: ErrUnauthenticated, status: http.StatusUnauthorized, code: errorCodeUnauthenticated},
		{name: "invalid principal", err: authority.ErrInvalidPrincipal, status: http.StatusUnauthorized, code: errorCodeUnauthenticated},
		{name: "expired principal", err: authority.ErrPrincipalExpired, status: http.StatusUnauthorized, code: errorCodeUnauthenticated},
		{name: "opaque authority", err: authority.ErrOpaqueAuthority, status: http.StatusUnauthorized, code: errorCodeUnauthenticated},
		{name: "invalid admitted authority", err: &authority.AdmissionError{Reason: authority.DenialInvalidAuthority}, status: http.StatusUnauthorized, code: errorCodeUnauthenticated},
		{name: "expired admitted authority", err: &authority.AdmissionError{Reason: authority.DenialExpired}, status: http.StatusUnauthorized, code: errorCodeUnauthenticated},
		{name: "wrong admitted class", err: &authority.AdmissionError{Reason: authority.DenialWrongClass}, status: http.StatusForbidden, code: errorCodeForbidden},
		{name: "generic admission denial", err: authority.ErrAdmissionDenied, status: http.StatusForbidden, code: errorCodeForbidden},
		{name: "workspace mismatch", err: authority.ErrWorkspaceMismatch, status: http.StatusForbidden, code: errorCodeForbidden},
		{name: "principal class", err: authority.ErrPrincipalClass, status: http.StatusForbidden, code: errorCodeForbidden},
		{name: "action not allowed", err: authority.ErrActionNotAllowed, status: http.StatusForbidden, code: errorCodeForbidden},
		{name: "catalog wrong workspace", err: workflowcatalog.ErrWrongWorkspace, status: http.StatusForbidden, code: errorCodeForbidden},
		{name: "invalid request", err: workflowcatalog.ErrInvalid, status: http.StatusBadRequest, code: errorCodeInvalidRequest},
		{name: "not found", err: workflowcatalog.ErrNotFound, status: http.StatusNotFound, code: errorCodeNotFound},
		{name: "ownership", err: workflowcatalog.ErrVersionOwnership, status: http.StatusConflict, code: errorCodeVersionOwnership},
		{name: "stale revision", err: workflowcatalog.ErrStaleRevision, status: http.StatusConflict, code: errorCodeStaleRevision},
		{name: "not validated", err: workflowcatalog.ErrVersionNotValidated, status: http.StatusPreconditionFailed, code: errorCodeVersionNotValidated},
		{name: "not approved", err: workflowcatalog.ErrVersionNotApproved, status: http.StatusPreconditionFailed, code: errorCodeVersionNotApproved},
		{name: "unavailable", err: workflowcatalog.ErrUnavailable, status: http.StatusServiceUnavailable, code: errorCodeUnavailable},
		{name: "invalid persisted state", err: workflowcatalog.ErrInvalidPersistedState, status: http.StatusBadGateway, code: errorCodeInvalidPersistedState},
		{name: "unknown", err: errors.New("boom"), status: http.StatusInternalServerError, code: errorCodeInternal},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, code, message := classifyError(test.err)
			if status != test.status || code != test.code || message == "" {
				t.Fatalf("classifyError(%v) = (%d, %q, %q), want (%d, %q, nonempty)", test.err, status, code, message, test.status, test.code)
			}
		})
	}
}
