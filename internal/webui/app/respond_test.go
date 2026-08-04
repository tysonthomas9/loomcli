package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRespondJSON_Success(t *testing.T) {
	w := httptest.NewRecorder()
	respondJSON(w, http.StatusOK, map[string]string{"key": "value"})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["key"] != "value" {
		t.Errorf("body[key] = %q, want %q", body["key"], "value")
	}
}

func TestRespondJSON_ErrorStatus(t *testing.T) {
	codes := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	}
	for _, code := range codes {
		w := httptest.NewRecorder()
		respondJSON(w, code, map[string]bool{"ok": false})
		if w.Code != code {
			t.Errorf("status = %d, want %d", w.Code, code)
		}
	}
}

func TestRespondJSON_TypedStruct(t *testing.T) {
	type resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	w := httptest.NewRecorder()
	respondJSON(w, http.StatusCreated, resp{Success: true})

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	var body resp
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !body.Success {
		t.Errorf("Success = false, want true")
	}
	if body.Error != "" {
		t.Errorf("Error = %q, want empty", body.Error)
	}
}

func TestRespondJSON_NilValue(t *testing.T) {
	w := httptest.NewRecorder()
	respondJSON(w, http.StatusOK, nil)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if body != "null\n" {
		t.Errorf("body = %q, want %q", body, "null\n")
	}
}

func TestRespondError_BasicError(t *testing.T) {
	w := httptest.NewRecorder()
	respondError(w, http.StatusBadRequest, "something went wrong")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "something went wrong" {
		t.Errorf("error = %q, want %q", body["error"], "something went wrong")
	}
}

func TestRespondError_StatusCodes(t *testing.T) {
	codes := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	}
	for _, code := range codes {
		w := httptest.NewRecorder()
		respondError(w, code, "test error")
		if w.Code != code {
			t.Errorf("status = %d, want %d", w.Code, code)
		}
	}
}

func TestRespondError_EmptyMessage(t *testing.T) {
	w := httptest.NewRecorder()
	respondError(w, http.StatusBadRequest, "")

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "" {
		t.Errorf("error = %q, want empty string", body["error"])
	}
}

func TestRespondError_ContentType(t *testing.T) {
	w := httptest.NewRecorder()
	respondError(w, http.StatusInternalServerError, "fail")

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}
