package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

// --- ReadJSON tests ---

func TestReadJSON_Success(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	body := `{"name":"Tyson","age":30}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var got payload
	err := ReadJSON(w, r, &got)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.Name != "Tyson" || got.Age != 30 {
		t.Fatalf("unexpected decoded value: %+v", got)
	}
}

func TestReadJSON_BodyTooLarge(t *testing.T) {
	// Build a valid JSON payload that exceeds MaxRequestBody (1MB).
	// We use {"data":"aaa...aaa"} where the 'a' string is large enough.
	padding := strings.Repeat("a", MaxRequestBody+1)
	bigJSON := `{"data":"` + padding + `"}`

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(bigJSON))
	w := httptest.NewRecorder()

	var dst map[string]string
	err := ReadJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for oversized body, got nil")
	}

	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T: %v", err, err)
	}
	if svcErr.Kind != apperrors.KindPayloadTooLarge {
		t.Fatalf("expected KindPayloadTooLarge, got %s", svcErr.Kind)
	}
}

func TestReadJSON_InvalidJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not json`))
	w := httptest.NewRecorder()

	var dst map[string]string
	err := ReadJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}

	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T: %v", err, err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}

func TestReadJSON_TrailingContent(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}GARBAGE`))
	w := httptest.NewRecorder()

	var dst map[string]string
	err := ReadJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for trailing content, got nil")
	}

	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T: %v", err, err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}

func TestReadJSON_EmptyBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	w := httptest.NewRecorder()

	var dst map[string]string
	err := ReadJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}

	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T: %v", err, err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}

// --- WriteJSON tests ---

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	payload := map[string]string{"hello": "world"}

	WriteJSON(w, http.StatusCreated, payload)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if got["hello"] != "world" {
		t.Fatalf("unexpected response body: %v", got)
	}
}

func TestWriteJSON_NilValue(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusOK, nil)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	// json.Encoder writes "null\n"
	if got := buf.String(); got != "null\n" {
		t.Fatalf("expected %q, got %q", "null\n", got)
	}
}

// --- ParseListOpts tests ---

func TestParseListOpts_Defaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items", nil)

	opts, err := ParseListOpts(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Limit != DefaultListLimit {
		t.Fatalf("expected limit %d, got %d", DefaultListLimit, opts.Limit)
	}
	if opts.Offset != 0 {
		t.Fatalf("expected offset 0, got %d", opts.Offset)
	}
	if opts.SortOrder != "asc" {
		t.Fatalf("expected sort_order %q, got %q", "asc", opts.SortOrder)
	}
}

func TestParseListOpts_ValidParams(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet,
		"/items?limit=50&offset=10&q=search&status=active&sort_by=name&sort_order=desc", nil)

	opts, err := ParseListOpts(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Limit != 50 {
		t.Errorf("expected limit 50, got %d", opts.Limit)
	}
	if opts.Offset != 10 {
		t.Errorf("expected offset 10, got %d", opts.Offset)
	}
	if opts.Query != "search" {
		t.Errorf("expected query %q, got %q", "search", opts.Query)
	}
	if opts.Status != "active" {
		t.Errorf("expected status %q, got %q", "active", opts.Status)
	}
	if opts.SortBy != "name" {
		t.Errorf("expected sort_by %q, got %q", "name", opts.SortBy)
	}
	if opts.SortOrder != "desc" {
		t.Errorf("expected sort_order %q, got %q", "desc", opts.SortOrder)
	}
}

func TestParseListOpts_LimitCapping(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?limit=5000", nil)

	opts, err := ParseListOpts(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Limit != MaxListLimit {
		t.Fatalf("expected limit capped to %d, got %d", MaxListLimit, opts.Limit)
	}
}

func TestParseListOpts_LimitZero(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?limit=0", nil)

	opts, err := ParseListOpts(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Limit != DefaultListLimit {
		t.Fatalf("expected limit reset to default %d, got %d", DefaultListLimit, opts.Limit)
	}
}

func TestParseListOpts_InvalidLimit(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?limit=abc", nil)

	_, err := ParseListOpts(r)
	if err == nil {
		t.Fatal("expected error for non-integer limit")
	}
	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T", err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}

func TestParseListOpts_NegativeLimit(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?limit=-5", nil)

	_, err := ParseListOpts(r)
	if err == nil {
		t.Fatal("expected error for negative limit")
	}
	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T", err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}

func TestParseListOpts_InvalidOffset(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?offset=xyz", nil)

	_, err := ParseListOpts(r)
	if err == nil {
		t.Fatal("expected error for non-integer offset")
	}
	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T", err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}

func TestParseListOpts_NegativeOffset(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?offset=-1", nil)

	_, err := ParseListOpts(r)
	if err == nil {
		t.Fatal("expected error for negative offset")
	}
	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T", err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}

func TestParseListOpts_InvalidSortOrder(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?sort_order=random", nil)

	_, err := ParseListOpts(r)
	if err == nil {
		t.Fatal("expected error for invalid sort_order")
	}
	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *apperrors.ServiceError, got %T", err)
	}
	if svcErr.Kind != apperrors.KindValidation {
		t.Fatalf("expected KindValidation, got %s", svcErr.Kind)
	}
}
