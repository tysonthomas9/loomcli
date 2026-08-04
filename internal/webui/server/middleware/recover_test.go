package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecover_NoPanic(t *testing.T) {
	mw := Recover(slog.Default())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", rec.Body.String())
	}
}

func TestRecover_Panic(t *testing.T) {
	mw := Recover(slog.Default())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})

	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest("GET", "/explode", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON body: %v", err)
	}
	if body["error"] != "internal server error" {
		t.Fatalf("expected error %q, got %q", "internal server error", body["error"])
	}
}

func TestRecover_NilLogger(t *testing.T) {
	// Passing nil should not panic — it should default to slog.Default().
	mw := Recover(nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("nil logger panic")
	})

	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 with nil logger, got %d", rec.Code)
	}
}
