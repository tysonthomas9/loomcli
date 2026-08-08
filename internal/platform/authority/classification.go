package authority

import "errors"

// ErrUnauthenticated means a transport could not establish a verified
// principal. Keeping this sentinel with the authority primitives lets every
// inbound adapter share the same authentication classification without
// depending on another capability's HTTP adapter.
var ErrUnauthenticated = errors.New("authority: unauthenticated")

// AuthenticationAuthorityErrorClass is the capability-independent result of
// classifying an authentication or authority failure. Transports decide how
// to render the class; capability-specific errors remain with their owning
// adapters.
type AuthenticationAuthorityErrorClass uint8

const (
	AuthenticationAuthorityErrorUnknown AuthenticationAuthorityErrorClass = iota
	// AuthenticationAuthorityErrorUnauthenticated is a principal-resolution
	// failure before an admitted authority exists.
	AuthenticationAuthorityErrorUnauthenticated
	// AuthenticationAuthorityErrorInvalidAdmission is an invalid or expired
	// admitted authority. Capability adapters decide whether their historical
	// wire contract projects this as 401 or 403.
	AuthenticationAuthorityErrorInvalidAdmission
	AuthenticationAuthorityErrorForbidden
)

// ClassifyAuthenticationAuthorityError recognizes only shared authentication
// and authority failures. It deliberately returns ok=false for capability
// errors so their owning adapter retains its public status, code, and message.
func ClassifyAuthenticationAuthorityError(err error) (class AuthenticationAuthorityErrorClass, ok bool) {
	var admissionErr *AdmissionError
	if errors.As(err, &admissionErr) && admissionErr != nil {
		switch admissionErr.Reason {
		case DenialInvalidAuthority, DenialExpired:
			return AuthenticationAuthorityErrorInvalidAdmission, true
		default:
			return AuthenticationAuthorityErrorForbidden, true
		}
	}

	switch {
	case errors.Is(err, ErrUnauthenticated),
		errors.Is(err, ErrInvalidPrincipal),
		errors.Is(err, ErrPrincipalExpired),
		errors.Is(err, ErrOpaqueAuthority):
		return AuthenticationAuthorityErrorUnauthenticated, true
	case errors.Is(err, ErrAdmissionDenied),
		errors.Is(err, ErrWorkspaceMismatch),
		errors.Is(err, ErrPrincipalClass),
		errors.Is(err, ErrActionNotAllowed):
		return AuthenticationAuthorityErrorForbidden, true
	default:
		return AuthenticationAuthorityErrorUnknown, false
	}
}
