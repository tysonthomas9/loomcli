package backend

import (
	"errors"
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// Error() format tests
// ---------------------------------------------------------------------------

func TestBackendError_Error_AllFields(t *testing.T) {
	cause := errors.New("disk full")
	be := &BackendError{Kind: KindInternal, Op: "Save", Message: "write failed", Cause: cause}

	want := "backend [internal] Save: write failed: disk full"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_NilCause(t *testing.T) {
	be := &BackendError{Kind: KindNotFound, Op: "Get", Message: "issue not found"}

	want := "backend [not_found] Get: issue not found"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_OpOnly(t *testing.T) {
	be := &BackendError{Kind: KindValidation, Op: "Create"}

	want := "backend [validation_error] Create"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_OpAndCauseOnly(t *testing.T) {
	cause := errors.New("timeout")
	be := &BackendError{Kind: KindTimeout, Op: "Fetch", Cause: cause}

	want := "backend [timeout] Fetch: timeout"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_MessageOnly(t *testing.T) {
	be := &BackendError{Kind: KindConflict, Message: "duplicate key"}

	want := "backend [conflict]: duplicate key"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_MessageAndCause(t *testing.T) {
	cause := errors.New("constraint violation")
	be := &BackendError{Kind: KindConflict, Message: "duplicate key", Cause: cause}

	want := "backend [conflict]: duplicate key: constraint violation"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_CauseOnly(t *testing.T) {
	cause := errors.New("unexpected")
	be := &BackendError{Kind: KindInternal, Cause: cause}

	want := "backend [internal]: unexpected"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_KindOnly(t *testing.T) {
	be := &BackendError{Kind: KindUnavailable}

	want := "backend [unavailable]"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_EmptyOpAndMessage(t *testing.T) {
	be := &BackendError{Kind: KindNotFound}

	want := "backend [not_found]"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBackendError_Error_NilReceiver(t *testing.T) {
	var be *BackendError

	want := "<nil BackendError>"
	if got := be.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Unwrap tests
// ---------------------------------------------------------------------------

func TestBackendError_Unwrap_ReturnsCause(t *testing.T) {
	cause := errors.New("root cause")
	be := &BackendError{Kind: KindInternal, Cause: cause}

	if got := be.Unwrap(); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestBackendError_Unwrap_NilCause(t *testing.T) {
	be := &BackendError{Kind: KindNotFound}

	if got := be.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
}

func TestBackendError_Unwrap_NilReceiver(t *testing.T) {
	var be *BackendError

	if got := be.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
}

func TestBackendError_ErrorsIs_Traversal(t *testing.T) {
	sentinel := errors.New("sentinel")
	be := &BackendError{Kind: KindTimeout, Op: "Poll", Cause: sentinel}

	if !errors.Is(be, sentinel) {
		t.Error("errors.Is should find sentinel in Cause via Unwrap")
	}
}

func TestBackendError_ErrorsIs_NoMatch(t *testing.T) {
	other := errors.New("other")
	be := &BackendError{Kind: KindTimeout, Op: "Poll", Cause: errors.New("different")}

	if errors.Is(be, other) {
		t.Error("errors.Is should not match unrelated error")
	}
}

// ---------------------------------------------------------------------------
// errors.As tests
// ---------------------------------------------------------------------------

func TestBackendError_ErrorsAs_Direct(t *testing.T) {
	be := ErrNotFound("Get", "issue not found")
	var target *BackendError
	if !errors.As(be, &target) {
		t.Fatal("errors.As should match *BackendError directly")
	}
	if target.Kind != KindNotFound {
		t.Errorf("Kind = %q, want %q", target.Kind, KindNotFound)
	}
}

func TestBackendError_ErrorsAs_WrappedViaFmtErrorf(t *testing.T) {
	be := ErrValidation("Create", "bad input")
	wrapped := fmt.Errorf("service layer: %w", be)

	var target *BackendError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As should match *BackendError through fmt.Errorf wrapping")
	}
	if target.Kind != KindValidation {
		t.Errorf("Kind = %q, want %q", target.Kind, KindValidation)
	}
	if target.Op != "Create" {
		t.Errorf("Op = %q, want %q", target.Op, "Create")
	}
}

func TestBackendError_ErrorsAs_NonBackendError(t *testing.T) {
	plain := errors.New("plain error")
	var target *BackendError
	if errors.As(plain, &target) {
		t.Error("errors.As should not match non-BackendError")
	}
}

// ---------------------------------------------------------------------------
// IsKind tests
// ---------------------------------------------------------------------------

func TestIsKind_MatchingKind(t *testing.T) {
	be := ErrNotFound("Get", "missing")
	if !IsKind(be, KindNotFound) {
		t.Error("IsKind should return true for matching kind")
	}
}

func TestIsKind_NonMatchingKind(t *testing.T) {
	be := ErrNotFound("Get", "missing")
	if IsKind(be, KindConflict) {
		t.Error("IsKind should return false for non-matching kind")
	}
}

func TestIsKind_NilError(t *testing.T) {
	if IsKind(nil, KindNotFound) {
		t.Error("IsKind should return false for nil error")
	}
}

func TestIsKind_NonBackendError(t *testing.T) {
	plain := errors.New("not a backend error")
	if IsKind(plain, KindInternal) {
		t.Error("IsKind should return false for non-BackendError")
	}
}

func TestIsKind_WrappedBackendError(t *testing.T) {
	be := ErrConflict("Claim", "already claimed")
	wrapped := fmt.Errorf("outer: %w", be)

	if !IsKind(wrapped, KindConflict) {
		t.Error("IsKind should find BackendError through wrapping chain")
	}
}

// ---------------------------------------------------------------------------
// Convenience constructor tests (table-driven)
// ---------------------------------------------------------------------------

func TestConvenienceConstructors_WithoutCause(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string, string) *BackendError
		kind ErrorKind
	}{
		{"ErrNotFound", ErrNotFound, KindNotFound},
		{"ErrValidation", ErrValidation, KindValidation},
		{"ErrConflict", ErrConflict, KindConflict},
		{"ErrNotImplemented", ErrNotImplemented, KindNotImplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be := tt.fn("TestOp", "test message")

			if be.Kind != tt.kind {
				t.Errorf("Kind = %q, want %q", be.Kind, tt.kind)
			}
			if be.Op != "TestOp" {
				t.Errorf("Op = %q, want %q", be.Op, "TestOp")
			}
			if be.Message != "test message" {
				t.Errorf("Message = %q, want %q", be.Message, "test message")
			}
			if be.Cause != nil {
				t.Errorf("Cause = %v, want nil", be.Cause)
			}
		})
	}
}

func TestConvenienceConstructors_WithCause(t *testing.T) {
	cause := errors.New("root cause")

	tests := []struct {
		name string
		fn   func(string, string, error) *BackendError
		kind ErrorKind
	}{
		{"ErrUnavailable", ErrUnavailable, KindUnavailable},
		{"ErrTimeout", ErrTimeout, KindTimeout},
		{"ErrInternal", ErrInternal, KindInternal},
		{"ErrCanceled", ErrCanceled, KindCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be := tt.fn("TestOp", "test message", cause)

			if be.Kind != tt.kind {
				t.Errorf("Kind = %q, want %q", be.Kind, tt.kind)
			}
			if be.Op != "TestOp" {
				t.Errorf("Op = %q, want %q", be.Op, "TestOp")
			}
			if be.Message != "test message" {
				t.Errorf("Message = %q, want %q", be.Message, "test message")
			}
			if be.Cause != cause {
				t.Errorf("Cause = %v, want %v", be.Cause, cause)
			}
		})
	}
}

func TestConvenienceConstructors_WithNilCause(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string, string, error) *BackendError
		kind ErrorKind
	}{
		{"ErrUnavailable", ErrUnavailable, KindUnavailable},
		{"ErrTimeout", ErrTimeout, KindTimeout},
		{"ErrInternal", ErrInternal, KindInternal},
		{"ErrCanceled", ErrCanceled, KindCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be := tt.fn("TestOp", "test message", nil)

			if be.Kind != tt.kind {
				t.Errorf("Kind = %q, want %q", be.Kind, tt.kind)
			}
			if be.Cause != nil {
				t.Errorf("Cause = %v, want nil", be.Cause)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewBackendError tests
// ---------------------------------------------------------------------------

func TestNewBackendError(t *testing.T) {
	cause := errors.New("underlying")
	be := NewBackendError(KindTimeout, "Fetch", "deadline exceeded", cause)

	if be.Kind != KindTimeout {
		t.Errorf("Kind = %q, want %q", be.Kind, KindTimeout)
	}
	if be.Op != "Fetch" {
		t.Errorf("Op = %q, want %q", be.Op, "Fetch")
	}
	if be.Message != "deadline exceeded" {
		t.Errorf("Message = %q, want %q", be.Message, "deadline exceeded")
	}
	if be.Cause != cause {
		t.Errorf("Cause = %v, want %v", be.Cause, cause)
	}
}

func TestNewBackendError_NilCause(t *testing.T) {
	be := NewBackendError(KindNotFound, "Get", "not found", nil)

	if be.Cause != nil {
		t.Errorf("Cause = %v, want nil", be.Cause)
	}
}

// ---------------------------------------------------------------------------
// ErrorKind constants sanity check
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ErrFilterNotSupported sentinel tests
// ---------------------------------------------------------------------------

func TestErrFilterNotSupported_Wrapping(t *testing.T) {
	wrapped := fmt.Errorf("wrapped: %w", ErrFilterNotSupported)
	if !errors.Is(wrapped, ErrFilterNotSupported) {
		t.Error("errors.Is should find ErrFilterNotSupported through fmt.Errorf wrapping")
	}
}

func TestErrFilterNotSupported_NotMatchOther(t *testing.T) {
	other := errors.New("some other error")
	if errors.Is(other, ErrFilterNotSupported) {
		t.Error("errors.Is should not match unrelated error as ErrFilterNotSupported")
	}
}

func TestErrFilterNotSupported_DoubleWrapping(t *testing.T) {
	inner := fmt.Errorf("inner: %w", ErrFilterNotSupported)
	outer := fmt.Errorf("outer: %w", inner)
	if !errors.Is(outer, ErrFilterNotSupported) {
		t.Error("errors.Is should find ErrFilterNotSupported through double wrapping")
	}
}

func TestErrorKindConstants(t *testing.T) {
	tests := []struct {
		kind ErrorKind
		want string
	}{
		{KindNotFound, "not_found"},
		{KindValidation, "validation_error"},
		{KindConflict, "conflict"},
		{KindUnavailable, "unavailable"},
		{KindTimeout, "timeout"},
		{KindNotImplemented, "not_implemented"},
		{KindInternal, "internal"},
		{KindCanceled, "canceled"},
	}
	for _, tt := range tests {
		if string(tt.kind) != tt.want {
			t.Errorf("ErrorKind constant = %q, want %q", tt.kind, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ErrorCode / ClaimRejectedPermanently
// ---------------------------------------------------------------------------

func codedErr(kind ErrorKind, msg, code string) *BackendError {
	be := &BackendError{Kind: kind, Op: "Claim", Message: msg}
	if code != "" {
		be.Meta = map[string]string{MetaErrorCode: code}
	}
	return be
}

func TestErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"plain error", errors.New("boom"), ""},
		{"backend error without meta", ErrConflict("Claim", "issue is not claimable"), ""},
		{"backend error with code", codedErr(KindConflict, "issue is not claimable", "not_claimable"), "not_claimable"},
		{"wrapped", fmt.Errorf("outer: %w", codedErr(KindConflict, "x", "already_claimed")), "already_claimed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrorCode(tt.err); got != tt.want {
				t.Errorf("ErrorCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaimRejectedPermanently_True(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"not_claimable code", codedErr(KindConflict, "issue is not claimable", "not_claimable")},
		{"invalid_transition code", codedErr(KindConflict, "issue is already closed", "invalid_transition")},
		{"not found kind", ErrNotFound("Claim", "issue PUPPET-1 not found")},
		{"message fallback, no code", ErrConflict("Claim", "issue is not claimable")},
		{"wrapped coded error", fmt.Errorf("claim: %w", codedErr(KindConflict, "nope", "not_claimable"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !ClaimRejectedPermanently(tt.err) {
				t.Errorf("ClaimRejectedPermanently(%v) = false, want true", tt.err)
			}
		})
	}
}

func TestClaimRejectedPermanently_False(t *testing.T) {
	// The critical regression guard: a bare conflict is the "someone else
	// holds the claim" case, which the retry logic depends on.
	foreignLock := &BackendError{
		Kind: KindConflict, Op: "Claim", Message: "issue already claimed",
		Meta: map[string]string{"existing_owner": "worktree-b"},
	}
	tests := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"foreign lock conflict", foreignLock},
		{"bare conflict", ErrConflict("Claim", "issue already claimed")},
		{"timeout", ErrTimeout("Claim", "deadline exceeded", nil)},
		{"unavailable", ErrUnavailable("Claim", "server unreachable", nil)},
		{"plain error", errors.New("connection reset")},
		{"unknown code", codedErr(KindConflict, "something odd", "future_code")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ClaimRejectedPermanently(tt.err) {
				t.Errorf("ClaimRejectedPermanently(%v) = true, want false", tt.err)
			}
		})
	}
}
