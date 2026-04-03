package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// allKinds lists every ErrorKind constant from service/errors.go.
// If a new kind is added there, this list must be updated — which
// forces the mapping table to be updated too.
var allKinds = []service.ErrorKind{
	service.KindNotFound,
	service.KindValidation,
	service.KindUnavailable,
	service.KindTimeout,
	service.KindConflict,
	service.KindInternal,
	service.KindForbidden,
	service.KindUnauthorized,
	service.KindLocked,
	service.KindPayloadTooLarge,
	service.KindRateLimited,
	service.KindBadGateway,
	service.KindNotImplemented,
}

func TestKindStatus_Completeness(t *testing.T) {
	for _, kind := range allKinds {
		if _, ok := kindStatus[kind]; !ok {
			t.Errorf("kindStatus missing entry for %q", kind)
		}
	}
}

func TestKindStatus_NoExtras(t *testing.T) {
	known := make(map[service.ErrorKind]bool, len(allKinds))
	for _, k := range allKinds {
		known[k] = true
	}
	for kind := range kindStatus {
		if !known[kind] {
			t.Errorf("kindStatus has unexpected entry %q not in allKinds", kind)
		}
	}
}

func TestStatusForKind(t *testing.T) {
	tests := []struct {
		kind service.ErrorKind
		want int
	}{
		{service.KindNotFound, http.StatusNotFound},
		{service.KindValidation, http.StatusBadRequest},
		{service.KindUnavailable, http.StatusServiceUnavailable},
		{service.KindTimeout, http.StatusGatewayTimeout},
		{service.KindConflict, http.StatusConflict},
		{service.KindInternal, http.StatusInternalServerError},
		{service.KindForbidden, http.StatusForbidden},
		{service.KindUnauthorized, http.StatusUnauthorized},
		{service.KindLocked, http.StatusLocked},
		{service.KindPayloadTooLarge, http.StatusRequestEntityTooLarge},
		{service.KindRateLimited, http.StatusTooManyRequests},
		{service.KindBadGateway, http.StatusBadGateway},
		{service.KindNotImplemented, http.StatusNotImplemented},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := statusForKind(tt.kind); got != tt.want {
				t.Errorf("statusForKind(%q) = %d, want %d", tt.kind, got, tt.want)
			}
		})
	}
}

func TestStatusForKind_UnknownKind(t *testing.T) {
	got := statusForKind(service.ErrorKind("totally_unknown"))
	if got != http.StatusInternalServerError {
		t.Errorf("statusForKind(unknown) = %d, want %d", got, http.StatusInternalServerError)
	}
}

func TestWriteServiceError_ServiceError(t *testing.T) {
	svcErr := service.ErrNotFound("issue abc-123")
	w := httptest.NewRecorder()
	WriteServiceError(w, svcErr)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "issue abc-123" {
		t.Errorf("body error = %q, want %q", body["error"], "issue abc-123")
	}
}

func TestWriteServiceError_WrappedServiceError(t *testing.T) {
	svcErr := service.ErrConflict("duplicate key")
	wrapped := fmt.Errorf("operation failed: %w", svcErr)
	w := httptest.NewRecorder()
	WriteServiceError(w, wrapped)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestWriteServiceError_NonServiceError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteServiceError(w, errors.New("something broke"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "internal server error" {
		t.Errorf("body error = %q, want %q", body["error"], "internal server error")
	}
}

func TestWriteServiceError_UsesMessageNotErrorString(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	svcErr := service.ErrInternal("database query failed", cause)
	w := httptest.NewRecorder()
	WriteServiceError(w, svcErr)

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Should use Message ("database query failed"), not Error() ("internal: database query failed: connection refused")
	if body["error"] != "database query failed" {
		t.Errorf("body error = %q, want %q", body["error"], "database query failed")
	}
}
