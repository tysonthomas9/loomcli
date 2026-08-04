package service

import (
	"errors"
	"fmt"
	"testing"
)

func TestServiceError_Error_WithCause(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	err := ErrInternal("database failed", cause)
	want := "internal: database failed: connection refused"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestServiceError_Error_WithoutCause(t *testing.T) {
	err := ErrNotFound("issue xyz")
	want := "not_found: issue xyz"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestServiceError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	err := ErrInternal("wrapped", cause)
	if !errors.Is(err, cause) {
		t.Error("errors.Is did not find wrapped cause")
	}
}

func TestServiceError_Unwrap_Nil(t *testing.T) {
	err := ErrNotFound("gone")
	if err.Unwrap() != nil {
		t.Error("Unwrap returned non-nil for error without cause")
	}
}

func TestServiceError_ErrorsAs(t *testing.T) {
	err := ErrValidation("bad input")
	var svcErr *ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatal("errors.As failed to extract *ServiceError")
	}
	if svcErr.Kind != KindValidation {
		t.Errorf("Kind = %q, want %q", svcErr.Kind, KindValidation)
	}
}

func TestServiceError_ErrorsAs_Wrapped(t *testing.T) {
	inner := ErrConflict("duplicate key")
	wrapped := fmt.Errorf("operation failed: %w", inner)
	var svcErr *ServiceError
	if !errors.As(wrapped, &svcErr) {
		t.Fatal("errors.As failed to extract *ServiceError from wrapped error")
	}
	if svcErr.Kind != KindConflict {
		t.Errorf("Kind = %q, want %q", svcErr.Kind, KindConflict)
	}
}

func TestNewServiceError(t *testing.T) {
	cause := fmt.Errorf("timeout")
	err := NewServiceError(KindTimeout, "RPC timed out", cause)
	if err.Kind != KindTimeout {
		t.Errorf("Kind = %q, want %q", err.Kind, KindTimeout)
	}
	if err.Message != "RPC timed out" {
		t.Errorf("Message = %q, want %q", err.Message, "RPC timed out")
	}
	if !errors.Is(err, cause) {
		t.Error("cause not unwrappable")
	}
}

func TestConvenienceConstructors_Kind(t *testing.T) {
	tests := []struct {
		name string
		err  *ServiceError
		want ErrorKind
	}{
		{"NotFound", ErrNotFound("x"), KindNotFound},
		{"Validation", ErrValidation("x"), KindValidation},
		{"Unavailable", ErrUnavailable("x"), KindUnavailable},
		{"Timeout", ErrTimeout("x"), KindTimeout},
		{"Conflict", ErrConflict("x"), KindConflict},
		{"Internal", ErrInternal("x", nil), KindInternal},
		{"Forbidden", ErrForbidden("x"), KindForbidden},
		{"Unauthorized", ErrUnauthorized("x"), KindUnauthorized},
		{"Locked", ErrLocked("x"), KindLocked},
		{"PayloadTooLarge", ErrPayloadTooLarge("x"), KindPayloadTooLarge},
		{"RateLimited", ErrRateLimited("x"), KindRateLimited},
		{"BadGateway", ErrBadGateway("x"), KindBadGateway},
		{"NotImplemented", ErrNotImplemented("x"), KindNotImplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Kind != tt.want {
				t.Errorf("Kind = %q, want %q", tt.err.Kind, tt.want)
			}
		})
	}
}
