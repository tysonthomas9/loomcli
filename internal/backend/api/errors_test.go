package api

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

func TestClassifyHTTPError_2xxSuccessFalse(t *testing.T) {
	tests := []struct {
		name     string
		body     apiResponse
		wantKind backend.ErrorKind
	}{
		{"not found", apiResponse{Success: false, Error: "issue not found"}, backend.KindNotFound},
		{"already claimed", apiResponse{Success: false, Error: "task already claimed by another"}, backend.KindConflict},
		{"validation", apiResponse{Success: false, Error: "validation failed"}, backend.KindValidation},
		{"invalid", apiResponse{Success: false, Error: "invalid input"}, backend.KindValidation},
		{"unknown", apiResponse{Success: false, Error: "something unexpected"}, backend.KindInternal},
		{"empty error", apiResponse{Success: false}, backend.KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Op", 200, tt.body)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var be *backend.BackendError
			if !errors.As(err, &be) {
				t.Fatalf("expected *BackendError, got %T", err)
			}
			if be.Kind != tt.wantKind {
				t.Fatalf("expected kind %s, got %s", tt.wantKind, be.Kind)
			}
		})
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
		{"502 bad gateway", 502, apiResponse{Error: "bad gateway"}, backend.KindInternal},
		{"503 unavailable", 503, apiResponse{Error: "maintenance"}, backend.KindUnavailable},
		{"504 timeout", 504, apiResponse{Error: "gateway timeout"}, backend.KindTimeout},
		{"418 teapot", 418, apiResponse{Error: "teapot"}, backend.KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyHTTPError("Op", tt.statusCode, tt.body)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var be *backend.BackendError
			if !errors.As(err, &be) {
				t.Fatalf("expected *BackendError, got %T", err)
			}
			if be.Kind != tt.wantKind {
				t.Fatalf("expected kind %s, got %s", tt.wantKind, be.Kind)
			}
		})
	}
}

func TestClassifyHTTPError_AuthMessagePrefix(t *testing.T) {
	err := classifyHTTPError("Get", 401, apiResponse{Error: "token expired"})
	if err == nil {
		t.Fatal("expected error")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected BackendError, got %T", err)
	}
	if be.Kind != backend.KindUnavailable {
		t.Errorf("Kind = %s, want %s", be.Kind, backend.KindUnavailable)
	}
	if got := be.Error(); got == "" {
		t.Error("empty error message")
	}
}

func TestClassifyHTTPError_EmptyError(t *testing.T) {
	err := classifyHTTPError("Get", 500, apiResponse{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("Kind = %v, want KindInternal", err)
	}
}

func TestClassifyErrorString(t *testing.T) {
	tests := []struct {
		name     string
		msg      string
		wantKind backend.ErrorKind
	}{
		{"not found lowercase", "issue not found", backend.KindNotFound},
		{"not found uppercase", "ISSUE NOT FOUND", backend.KindNotFound},
		{"already claimed", "task already claimed", backend.KindConflict},
		{"validation", "validation failed", backend.KindValidation},
		{"invalid", "INVALID input", backend.KindValidation},
		{"other", "mystery error", backend.KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyErrorString("Op", tt.msg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !backend.IsKind(err, tt.wantKind) {
				t.Fatalf("expected kind %s, got %v", tt.wantKind, err)
			}
		})
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
		{"other error", errors.New("unexpected"), backend.KindUnavailable},
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
			var be *backend.BackendError
			if !errors.As(result, &be) {
				t.Fatalf("expected *BackendError, got %T", result)
			}
			if be.Kind != tt.wantKind {
				t.Fatalf("expected kind %s, got %s", tt.wantKind, be.Kind)
			}
		})
	}
}
