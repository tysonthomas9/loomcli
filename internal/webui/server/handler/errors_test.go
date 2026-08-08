package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

func TestHandleServiceError_AllKinds(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "KindNotFound",
			err:        apperrors.ErrNotFound("thing not found"),
			wantStatus: http.StatusNotFound,
			wantMsg:    "thing not found",
		},
		{
			name:       "KindValidation",
			err:        apperrors.ErrValidation("bad input"),
			wantStatus: http.StatusBadRequest,
			wantMsg:    "bad input",
		},
		{
			name:       "KindUnavailable",
			err:        apperrors.ErrUnavailable("service down"),
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    "service down",
		},
		{
			name:       "KindTimeout",
			err:        apperrors.ErrTimeout("took too long"),
			wantStatus: http.StatusGatewayTimeout,
			wantMsg:    "took too long",
		},
		{
			name:       "KindConflict",
			err:        apperrors.ErrConflict("already exists"),
			wantStatus: http.StatusConflict,
			wantMsg:    "already exists",
		},
		{
			name:       "KindInternal",
			err:        apperrors.ErrInternal("something broke", errors.New("db fail")),
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "something broke",
		},
		{
			name:       "KindForbidden",
			err:        apperrors.ErrForbidden("no access"),
			wantStatus: http.StatusForbidden,
			wantMsg:    "no access",
		},
		{
			name:       "KindUnauthorized",
			err:        apperrors.ErrUnauthorized("bad token"),
			wantStatus: http.StatusUnauthorized,
			wantMsg:    "bad token",
		},
		{
			name:       "KindLocked",
			err:        apperrors.ErrLocked("resource locked"),
			wantStatus: http.StatusLocked,
			wantMsg:    "resource locked",
		},
		{
			name:       "KindPayloadTooLarge",
			err:        apperrors.ErrPayloadTooLarge("too big"),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantMsg:    "too big",
		},
		{
			name:       "KindRateLimited",
			err:        apperrors.ErrRateLimited("slow down"),
			wantStatus: http.StatusTooManyRequests,
			wantMsg:    "slow down",
		},
		{
			name:       "KindBadGateway",
			err:        apperrors.ErrBadGateway("upstream failed"),
			wantStatus: http.StatusBadGateway,
			wantMsg:    "upstream failed",
		},
		{
			name:       "KindNotImplemented",
			err:        apperrors.ErrNotImplemented("coming soon"),
			wantStatus: http.StatusNotImplemented,
			wantMsg:    "coming soon",
		},
		{
			name:       "KindStarting",
			err:        apperrors.ErrStarting("workspace is loading"),
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    "workspace is loading",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			HandleServiceError(w, tt.err)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if body["error"] != tt.wantMsg {
				t.Errorf("expected error message %q, got %q", tt.wantMsg, body["error"])
			}
		})
	}
}

func TestHandleServiceError_WrappedError(t *testing.T) {
	inner := apperrors.ErrNotFound("thing missing")
	wrapped := fmt.Errorf("outer context: %w", inner)

	w := httptest.NewRecorder()
	HandleServiceError(w, wrapped)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for wrapped ServiceError, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "thing missing" {
		t.Fatalf("expected error message %q, got %q", "thing missing", body["error"])
	}
}

func TestHandleServiceError_NonServiceError(t *testing.T) {
	w := httptest.NewRecorder()
	HandleServiceError(w, errors.New("boom"))

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for non-ServiceError, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "internal server error" {
		t.Fatalf("expected %q, got %q", "internal server error", body["error"])
	}
}

func TestHandleServiceError_MessageVsError(t *testing.T) {
	// ErrInternal includes a Cause, so Error() returns "internal: msg: cause"
	// but the response body should only contain the Message field, not the full chain.
	cause := errors.New("db connection refused")
	svcErr := apperrors.ErrInternal("something went wrong", cause)

	w := httptest.NewRecorder()
	HandleServiceError(w, svcErr)

	resp := w.Result()
	defer resp.Body.Close()

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Must be the Message, not the full Error() string.
	if body["error"] != "something went wrong" {
		t.Fatalf("expected message %q, got %q", "something went wrong", body["error"])
	}
	// Verify it does NOT contain the cause string.
	if body["error"] == svcErr.Error() {
		t.Fatal("response body should not contain the full Error() string with cause chain")
	}
}

func TestHandleServiceError_UnknownKind(t *testing.T) {
	svcErr := apperrors.NewServiceError("custom_unknown", "mysterious failure", nil)

	w := httptest.NewRecorder()
	HandleServiceError(w, svcErr)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for unknown kind, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "mysterious failure" {
		t.Fatalf("expected %q, got %q", "mysterious failure", body["error"])
	}
}

func TestHandleServiceError_KindStarting(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantRetryAfter string
		wantMsg        string
	}{
		{
			name:           "KindStarting returns 503 with Retry-After header",
			err:            apperrors.ErrStarting("workspace is loading"),
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "5",
			wantMsg:        "workspace is loading",
		},
		{
			name:           "KindUnavailable does NOT set Retry-After header",
			err:            apperrors.ErrUnavailable("service down"),
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "",
			wantMsg:        "service down",
		},
		{
			name:           "KindTimeout does NOT set Retry-After header",
			err:            apperrors.ErrTimeout("took too long"),
			wantStatus:     http.StatusGatewayTimeout,
			wantRetryAfter: "",
			wantMsg:        "took too long",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			HandleServiceError(w, tt.err)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			gotRetryAfter := resp.Header.Get("Retry-After")
			if gotRetryAfter != tt.wantRetryAfter {
				t.Errorf("Retry-After header = %q, want %q", gotRetryAfter, tt.wantRetryAfter)
			}

			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if body["error"] != tt.wantMsg {
				t.Errorf("expected error message %q, got %q", tt.wantMsg, body["error"])
			}
		})
	}
}

func TestHandleServiceError_KindStarting_InAllKindsTable(t *testing.T) {
	// Verify KindStarting is present and correct in the AllKinds table test above
	w := httptest.NewRecorder()
	HandleServiceError(w, apperrors.ErrStarting("loading"))

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for KindStarting, got %d", resp.StatusCode)
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter != "5" {
		t.Errorf("expected Retry-After=5, got %q", retryAfter)
	}
}

func TestStatusForKind(t *testing.T) {
	tests := []struct {
		kind apperrors.ErrorKind
		want int
	}{
		{apperrors.KindNotFound, http.StatusNotFound},
		{apperrors.KindValidation, http.StatusBadRequest},
		{apperrors.KindUnavailable, http.StatusServiceUnavailable},
		{apperrors.KindTimeout, http.StatusGatewayTimeout},
		{apperrors.KindConflict, http.StatusConflict},
		{apperrors.KindInternal, http.StatusInternalServerError},
		{apperrors.KindForbidden, http.StatusForbidden},
		{apperrors.KindUnauthorized, http.StatusUnauthorized},
		{apperrors.KindLocked, http.StatusLocked},
		{apperrors.KindPayloadTooLarge, http.StatusRequestEntityTooLarge},
		{apperrors.KindRateLimited, http.StatusTooManyRequests},
		{apperrors.KindBadGateway, http.StatusBadGateway},
		{apperrors.KindNotImplemented, http.StatusNotImplemented},
		{apperrors.KindStarting, http.StatusServiceUnavailable},
		{apperrors.KindPreconditionFailed, http.StatusPreconditionFailed},
		{apperrors.KindPreconditionRequired, http.StatusPreconditionRequired},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := StatusForKind(tt.kind); got != tt.want {
				t.Errorf("StatusForKind(%q) = %d, want %d", tt.kind, got, tt.want)
			}
		})
	}
}

func TestStatusForKind_UnknownKind(t *testing.T) {
	got := StatusForKind(apperrors.ErrorKind("totally_unknown"))
	if got != http.StatusInternalServerError {
		t.Errorf("StatusForKind(unknown) = %d, want %d", got, http.StatusInternalServerError)
	}
}

func TestKindToStatus_Completeness(t *testing.T) {
	// All ErrorKind constants defined in service/errors.go.
	allKinds := []apperrors.ErrorKind{
		apperrors.KindNotFound,
		apperrors.KindValidation,
		apperrors.KindUnavailable,
		apperrors.KindTimeout,
		apperrors.KindConflict,
		apperrors.KindInternal,
		apperrors.KindForbidden,
		apperrors.KindUnauthorized,
		apperrors.KindLocked,
		apperrors.KindPayloadTooLarge,
		apperrors.KindRateLimited,
		apperrors.KindBadGateway,
		apperrors.KindNotImplemented,
		apperrors.KindStarting,
		apperrors.KindPreconditionFailed,
		apperrors.KindPreconditionRequired,
	}

	for _, kind := range allKinds {
		if _, ok := kindToStatus[kind]; !ok {
			t.Errorf("kindToStatus is missing mapping for %q", kind)
		}
	}

	if len(kindToStatus) != len(allKinds) {
		t.Errorf("kindToStatus has %d entries, expected %d (possible stale entries)",
			len(kindToStatus), len(allKinds))
	}
}
