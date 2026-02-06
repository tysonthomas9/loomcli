package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// testHandler returns a simple handler that writes "OK" for testing.
func testHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}

func TestCORSMiddleware_Disabled(t *testing.T) {
	config := CORSConfig{
		Enabled: false,
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should pass through without CORS headers
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// No CORS headers should be set when disabled
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Access-Control-Allow-Origin should not be set when CORS is disabled")
	}
}

func TestCORSMiddleware_EnabledWithDefaultOrigin(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Check CORS headers
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PATCH, PUT, DELETE, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, "GET, POST, PATCH, PUT, DELETE, OPTIONS")
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization, X-Requested-With" {
		t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, "Content-Type, Authorization, X-Requested-With")
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, "true")
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("Access-Control-Max-Age = %q, want %q", got, "86400")
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Origin", "http://malicious-site.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Request should still succeed, but without CORS headers
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// No CORS headers for disallowed origin
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Access-Control-Allow-Origin should not be set for disallowed origin")
	}

	// Vary header should still be set
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

func TestCORSMiddleware_EmptyOrigin(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	// No Origin header (same-origin request)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// No CORS headers for same-origin requests
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Access-Control-Allow-Origin should not be set for same-origin request")
	}
}

func TestCORSMiddleware_NullOrigin(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Origin", "null") // 'null' origin (potential security issue)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// 'null' origin should be rejected
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Access-Control-Allow-Origin should not be set for 'null' origin")
	}
}

func TestCORSMiddleware_PreflightRequest(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/issues", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Preflight should return 204 No Content
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Check CORS headers on preflight response
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PATCH, PUT, DELETE, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, "GET, POST, PATCH, PUT, DELETE, OPTIONS")
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("Access-Control-Max-Age = %q, want %q", got, "86400")
	}

	// Body should be empty for preflight
	if w.Body.Len() != 0 {
		t.Errorf("preflight response body should be empty, got %d bytes", w.Body.Len())
	}
}

func TestCORSMiddleware_PreflightDisallowedOrigin(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/issues", nil)
	req.Header.Set("Origin", "http://malicious-site.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Preflight from disallowed origin should be rejected at middleware level
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}

	// No CORS headers for disallowed origin
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Access-Control-Allow-Origin should not be set for disallowed origin")
	}

	// Body should be empty for rejected preflight
	if w.Body.Len() != 0 {
		t.Errorf("rejected preflight response body should be empty, got %d bytes", w.Body.Len())
	}
}

func TestCORSMiddleware_PreflightNoOrigin(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/issues", nil)
	// No Origin header
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Preflight without Origin should be rejected
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCORSMiddleware_MultipleAllowedOrigins(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000", "http://localhost:5173", "https://example.com"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	tests := []struct {
		origin string
		want   string
	}{
		{"http://localhost:3000", "http://localhost:3000"},
		{"http://localhost:5173", "http://localhost:5173"},
		{"https://example.com", "https://example.com"},
		{"http://unauthorized.com", ""},
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
			req.Header.Set("Origin", tt.origin)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			got := w.Header().Get("Access-Control-Allow-Origin")
			if got != tt.want {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCORSMiddleware_TrailingSlashHandling(t *testing.T) {
	// Config with trailing slash
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000/"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	tests := []struct {
		name   string
		origin string
		want   string
	}{
		{"without trailing slash", "http://localhost:3000", "http://localhost:3000"},
		{"with trailing slash", "http://localhost:3000/", "http://localhost:3000/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
			req.Header.Set("Origin", tt.origin)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			got := w.Header().Get("Access-Control-Allow-Origin")
			if got != tt.want {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCORSMiddleware_VaryHeaderAlwaysSet(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	tests := []struct {
		name   string
		origin string
	}{
		{"allowed origin", "http://localhost:3000"},
		{"disallowed origin", "http://evil.com"},
		{"no origin", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if got := w.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, want %q", got, "Origin")
			}
		})
	}
}

func TestCORSMiddleware_HandlerResponsePreserved(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	// Custom handler that returns specific status and body
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"created": true}`))
	})

	middleware := NewCORSMiddleware(config)
	handler := middleware(customHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Check handler response is preserved
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get("X-Custom-Header"); got != "test-value" {
		t.Errorf("X-Custom-Header = %q, want %q", got, "test-value")
	}
	if got := w.Body.String(); got != `{"created": true}` {
		t.Errorf("body = %q, want %q", got, `{"created": true}`)
	}

	// Check CORS headers are also present
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
}

func TestCORSMiddleware_CaseSensitiveOrigins(t *testing.T) {
	config := CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"http://localhost:3000"},
	}

	middleware := NewCORSMiddleware(config)
	handler := middleware(testHandler())

	// Origins are case-sensitive per the spec
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Origin", "http://LOCALHOST:3000")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should not match due to case difference
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Access-Control-Allow-Origin should not be set for case-mismatched origin")
	}
}
