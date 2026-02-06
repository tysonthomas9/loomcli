package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders_AllHeadersSet(t *testing.T) {
	middleware := NewSecurityHeadersMiddleware()
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	expected := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"X-Frame-Options":         "DENY",
		"Permissions-Policy":      "camera=(), microphone=(), geolocation=(), payment=()",
	}

	for header, want := range expected {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestSecurityHeaders_APIResponses(t *testing.T) {
	middleware := NewSecurityHeadersMiddleware()
	handler := middleware(testHandler())

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/issues", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
			}
			if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want %q", got, "DENY")
			}
		})
	}
}

func TestSecurityHeaders_WithCORSMiddleware(t *testing.T) {
	corsConfig := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	corsMiddleware := NewCORSMiddleware(corsConfig)
	securityMiddleware := NewSecurityHeadersMiddleware()
	handler := securityMiddleware(corsMiddleware(testHandler()))

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Security headers present
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "DENY")
	}

	// CORS headers also present
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
}

func TestSecurityHeaders_PreservesHandlerResponse(t *testing.T) {
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"created": true}`))
	})

	middleware := NewSecurityHeadersMiddleware()
	handler := middleware(customHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get("X-Custom-Header"); got != "test-value" {
		t.Errorf("X-Custom-Header = %q, want %q", got, "test-value")
	}
	if got := w.Body.String(); got != `{"created": true}` {
		t.Errorf("body = %q, want %q", got, `{"created": true}`)
	}

	// Security headers still present
	if got := w.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("Content-Security-Policy should be set")
	}
}

func TestSecurityHeaders_ErrorResponses(t *testing.T) {
	errorHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})

	middleware := NewSecurityHeadersMiddleware()
	handler := middleware(errorHandler)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	// Security headers should still be set on error responses
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "DENY")
	}
}
