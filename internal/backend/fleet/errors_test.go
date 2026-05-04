package fleet

import (
	"context"
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
