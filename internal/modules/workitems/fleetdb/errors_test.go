package fleetdb

import (
	"context"
	"net"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
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
		wantKind   workitems.ErrorKind
	}{
		{"400 validation", 400, apiResponse{Error: "bad input"}, workitems.KindValidation},
		{"422 validation", 422, apiResponse{Error: "parent issue not found"}, workitems.KindValidation},
		{"401 auth", 401, apiResponse{Error: "unauthorized"}, workitems.KindUnavailable},
		{"403 forbidden", 403, apiResponse{Error: "forbidden"}, workitems.KindUnavailable},
		{"404 not found", 404, apiResponse{Error: "issue not found"}, workitems.KindNotFound},
		{"409 conflict", 409, apiResponse{Error: "already claimed"}, workitems.KindConflict},
		{"429 rate limit", 429, apiResponse{Error: "too many requests"}, workitems.KindUnavailable},
		{"500 internal", 500, apiResponse{Error: "server error"}, workitems.KindInternal},
		{"503 unavailable", 503, apiResponse{Error: "maintenance"}, workitems.KindUnavailable},
		{"504 timeout", 504, apiResponse{Error: "gateway timeout"}, workitems.KindTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Op", tt.statusCode, tt.body)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !workitems.IsKind(err, tt.wantKind) {
				t.Fatalf("expected kind %s, got %v", tt.wantKind, err)
			}
		})
	}
}

func TestClassifyHTTPErrorUsesStructuredCode(t *testing.T) {
	tests := []struct {
		name     string
		body     apiResponse
		wantKind workitems.ErrorKind
	}{
		{"validation", apiResponse{Code: "validation_failed", Error: "parent issue not found"}, workitems.KindValidation},
		{"conflict", apiResponse{Code: "not_claimable", Error: "issue cannot be claimed"}, workitems.KindConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Create", 422, tt.body)
			if !workitems.IsKind(err, tt.wantKind) {
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
		wantKind workitems.ErrorKind
	}{
		{"not found", apiResponse{Error: "issue not found"}, workitems.KindNotFound},
		{"already claimed", apiResponse{Error: "task already claimed by another"}, workitems.KindConflict},
		{"already closed", apiResponse{Error: "issue is already closed"}, workitems.KindConflict},
		{"is closed", apiResponse{Error: "issue loom-x-01 is closed"}, workitems.KindConflict},
		{"validation", apiResponse{Error: "validation failed"}, workitems.KindValidation},
		{"invalid", apiResponse{Error: "invalid input"}, workitems.KindValidation},
		{"other", apiResponse{Error: "something unexpected"}, workitems.KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Op", 200, tt.body)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !workitems.IsKind(err, tt.wantKind) {
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
	if !workitems.IsKind(err, workitems.KindConflict) {
		t.Fatalf("expected KindConflict, got %v", err)
	}
}

func TestClassifyTransportError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKind workitems.ErrorKind
	}{
		{"nil", nil, ""},
		{"context canceled", context.Canceled, workitems.KindCanceled},
		{"deadline exceeded", context.DeadlineExceeded, workitems.KindTimeout},
		{
			"net error",
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: "example.com", Err: "no such host"}},
			workitems.KindUnavailable,
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
			if !workitems.IsKind(result, tt.wantKind) {
				t.Fatalf("expected kind %s, got %v", tt.wantKind, result)
			}
		})
	}
}
