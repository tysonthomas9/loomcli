package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestWriteServiceError_ServiceError(t *testing.T) {
	svcErr := service.ErrNotFound("issue abc-123")
	w := httptest.NewRecorder()
	webui.WriteServiceError(w, svcErr)

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
	webui.WriteServiceError(w, wrapped)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestWriteServiceError_NonServiceError(t *testing.T) {
	w := httptest.NewRecorder()
	webui.WriteServiceError(w, errors.New("something broke"))

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
	webui.WriteServiceError(w, svcErr)

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Should use Message ("database query failed"), not Error() ("internal: database query failed: connection refused")
	if body["error"] != "database query failed" {
		t.Errorf("body error = %q, want %q", body["error"], "database query failed")
	}
}
