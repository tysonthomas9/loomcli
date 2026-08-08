package handler

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// AuthenticationAuthorityHTTPError is the shared HTTP projection of an
// authentication or authority failure. Feature adapters retain ownership of
// the response message and all capability-specific error mappings.
type AuthenticationAuthorityHTTPError struct {
	Status int
	Code   string
}

// ClassifyAuthenticationAuthorityError maps the shared authority classes to
// their stable HTTP status and code. It returns ok=false for capability errors
// so the owning feature adapter can apply its own contract.
func ClassifyAuthenticationAuthorityError(err error) (classification AuthenticationAuthorityHTTPError, ok bool) {
	class, ok := authority.ClassifyAuthenticationAuthorityError(err)
	if !ok {
		return AuthenticationAuthorityHTTPError{}, false
	}
	switch class {
	case authority.AuthenticationAuthorityErrorUnauthenticated:
		return AuthenticationAuthorityHTTPError{Status: http.StatusUnauthorized, Code: "unauthenticated"}, true
	case authority.AuthenticationAuthorityErrorInvalidAdmission,
		authority.AuthenticationAuthorityErrorForbidden:
		return AuthenticationAuthorityHTTPError{Status: http.StatusForbidden, Code: "forbidden"}, true
	default:
		return AuthenticationAuthorityHTTPError{}, false
	}
}
