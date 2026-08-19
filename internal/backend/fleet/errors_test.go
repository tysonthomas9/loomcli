package fleet

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestClassifyHTTPError_Success(t *testing.T) {
	err := classifyHTTPError("Get", 200, apiResponse{Success: true})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestClassifyHTTPError_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       apiResponse
		wantKind   backend.ErrorKind
	}{
		{"400 validation", 400, apiResponse{Error: "bad input"}, backend.KindValidation},
		{"422 validation", 422, apiResponse{Error: "parent issue not found"}, backend.KindValidation},
		{"401 auth", 401, apiResponse{Error: "unauthorized"}, backend.KindUnavailable},
		{"403 forbidden", 403, apiResponse{Error: "forbidden"}, backend.KindUnavailable},
		{"404 not found", 404, apiResponse{Error: "issue not found"}, backend.KindNotFound},
		{"409 conflict", 409, apiResponse{Error: "already claimed"}, backend.KindConflict},
		{"429 rate limit", 429, apiResponse{Error: "too many requests"}, backend.KindUnavailable},
		{"500 internal", 500, apiResponse{Error: "server error"}, backend.KindInternal},
		{"503 unavailable", 503, apiResponse{Error: "maintenance"}, backend.KindUnavailable},
		{"504 timeout", 504, apiResponse{Error: "gateway timeout"}, backend.KindTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Op", tt.statusCode, tt.body)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !backend.IsKind(err, tt.wantKind) {
				t.Fatalf("expected kind %s, got %v", tt.wantKind, err)
			}
		})
	}
}

func TestClassifyHTTPErrorUsesStructuredCode(t *testing.T) {
	tests := []struct {
		name     string
		body     apiResponse
		wantKind backend.ErrorKind
	}{
		{"validation", apiResponse{Code: "validation_failed", Error: "parent issue not found"}, backend.KindValidation},
		{"conflict", apiResponse{Code: "not_claimable", Error: "issue cannot be claimed"}, backend.KindConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Create", 422, tt.body)
			if !backend.IsKind(err, tt.wantKind) {
				t.Fatalf("expected kind %s, got %v", tt.wantKind, err)
			}
		})
	}
}

func TestParseFleetResponsePreservesNativeErrorCode(t *testing.T) {
	response, err := parseFleetResponse([]byte(`{"error":{"code":"validation_failed","message":"bad parent"}}`), 422)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if response.Code != "validation_failed" {
		t.Fatalf("code = %q, want validation_failed", response.Code)
	}
}

func TestClassifyHTTPError_SuccessFalseStringMatching(t *testing.T) {
	tests := []struct {
		name     string
		body     apiResponse
		wantKind backend.ErrorKind
	}{
		{"not found", apiResponse{Error: "issue not found"}, backend.KindNotFound},
		{"already claimed", apiResponse{Error: "task already claimed by another"}, backend.KindConflict},
		{"already closed", apiResponse{Error: "issue is already closed"}, backend.KindConflict},
		{"is closed", apiResponse{Error: "issue loom-x-01 is closed"}, backend.KindConflict},
		{"validation", apiResponse{Error: "validation failed"}, backend.KindValidation},
		{"invalid", apiResponse{Error: "invalid input"}, backend.KindValidation},
		{"other", apiResponse{Error: "something unexpected"}, backend.KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Op", 200, tt.body)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !backend.IsKind(err, tt.wantKind) {
				t.Fatalf("expected kind %s, got %v", tt.wantKind, err)
			}
		})
	}
}

// Regression (parity harness 14-error-handling.spec.ts): closing an
// already-closed issue must map to HTTP 409 (KindConflict), not 500. The
// fleet-db backend returns {"success":false,"error":"issue is already
// closed"} (observed in loom-fleet logs as `error="backend [internal]
// Close: issue is already closed"`). Without this classifier entry the
// webui returned 500, failing the idempotency-parity spec.
func TestClassifyHTTPError_AlreadyClosed_IsConflict(t *testing.T) {
	err := classifyHTTPError("Close", 200, apiResponse{
		Success: false,
		Error:   "issue is already closed",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("expected KindConflict, got %v", err)
	}
}

func TestClassifyTransportError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKind backend.ErrorKind
	}{
		{"nil", nil, ""},
		{"context canceled", context.Canceled, backend.KindCanceled},
		{"deadline exceeded", context.DeadlineExceeded, backend.KindTimeout},
		{
			"net error",
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: "example.com", Err: "no such host"}},
			backend.KindUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyTransportError("Op", tt.err)
			if tt.err == nil {
				if result != nil {
					t.Fatalf("expected nil, got %v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected error, got nil")
			}
			if !backend.IsKind(result, tt.wantKind) {
				t.Fatalf("expected kind %s, got %v", tt.wantKind, result)
			}
		})
	}
}

// metaOf returns the BackendError meta map for assertions, or nil.
func metaOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T (%v)", err, err)
	}
	return be.Meta
}

// The regression the PUPPET-127 fix hangs on: "not_claimable" must keep
// classifying as KindConflict (nothing downstream changes) AND now carry the
// server code, which is what makes the permanent/transient split possible.
func TestClassifyHTTPError_NotClaimableCarriesCode(t *testing.T) {
	err := classifyHTTPError("Claim", 422, apiResponse{
		Error: "issue is not claimable",
		Code:  "not_claimable",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("kind changed: want conflict, got %v", err)
	}
	if got := backend.ErrorCode(err); got != "not_claimable" {
		t.Fatalf("ErrorCode() = %q, want %q", got, "not_claimable")
	}
	if !backend.ClaimRejectedPermanently(err) {
		t.Fatal("ClaimRejectedPermanently() = false, want true")
	}
}

func TestClassifyHTTPError_CodeAndMetaCoexist(t *testing.T) {
	err := classifyHTTPError("Claim", 409, apiResponse{
		Error: "issue already claimed",
		Code:  "already_claimed",
		Meta:  map[string]string{"existing_owner": "worktree-b"},
	})
	meta := metaOf(t, err)
	if meta["existing_owner"] != "worktree-b" {
		t.Errorf("existing_owner = %q, want %q", meta["existing_owner"], "worktree-b")
	}
	if meta[backend.MetaErrorCode] != "already_claimed" {
		t.Errorf("error_code = %q, want %q", meta[backend.MetaErrorCode], "already_claimed")
	}
	// A foreign-held claim is contended, not permanently rejected.
	if backend.ClaimRejectedPermanently(err) {
		t.Error("ClaimRejectedPermanently() = true for already_claimed, want false")
	}
}

func TestClassifyHTTPError_NoCodeLeavesMetaUntouched(t *testing.T) {
	err := classifyHTTPError("Op", 500, apiResponse{Error: "server error"})
	if !backend.IsKind(err, backend.KindInternal) {
		t.Fatalf("kind changed: want internal, got %v", err)
	}
	if meta := metaOf(t, err); meta != nil {
		if _, ok := meta[backend.MetaErrorCode]; ok {
			t.Errorf("error_code set on a codeless response: %v", meta)
		}
	}
}
