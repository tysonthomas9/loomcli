package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders_AllHeadersSet(t *testing.T) {
	middleware := NewSecurityHeadersMiddleware(SecurityConfig{})
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	expected := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self' 'wasm-unsafe-eval' 'sha256-E907z9SPF4o7blRe1MXfQVC2tBrJopXOXrMYZvksy/o='; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; report-uri /api/csp-report",
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
	middleware := NewSecurityHeadersMiddleware(SecurityConfig{})
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
	securityMiddleware := NewSecurityHeadersMiddleware(SecurityConfig{})
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

	middleware := NewSecurityHeadersMiddleware(SecurityConfig{})
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

	middleware := NewSecurityHeadersMiddleware(SecurityConfig{})
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

func TestSecurityHeaders_HSTSEnabled(t *testing.T) {
	middleware := NewSecurityHeadersMiddleware(SecurityConfig{HSTSEnabled: true})
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	want := "max-age=63072000; includeSubDomains"
	if got := w.Header().Get("Strict-Transport-Security"); got != want {
		t.Errorf("Strict-Transport-Security = %q, want %q", got, want)
	}
}

func TestSecurityHeaders_HSTSDisabledByDefault(t *testing.T) {
	middleware := NewSecurityHeadersMiddleware(SecurityConfig{})
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q, want empty (HSTS should be disabled by default)", got)
	}
}

func TestSecurityHeaders_ExtAuthOriginInCSP(t *testing.T) {
	middleware := NewSecurityHeadersMiddleware(SecurityConfig{
		ExtAuthOrigin: "https://auth.example.com",
	})
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")

	// connect-src should include both 'self' and the auth origin
	wantConnectSrc := "connect-src 'self' https://auth.example.com"
	if !strings.Contains(csp, wantConnectSrc) {
		t.Errorf("CSP = %q, want it to contain %q", csp, wantConnectSrc)
	}
}

func TestSecurityHeaders_NoExtAuth_DefaultCSP(t *testing.T) {
	middleware := NewSecurityHeadersMiddleware(SecurityConfig{})
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")

	// connect-src should be just 'self' (no external auth origin)
	wantConnectSrc := "connect-src 'self';"
	if !strings.Contains(csp, wantConnectSrc) {
		t.Errorf("CSP = %q, want it to contain %q", csp, wantConnectSrc)
	}

	// Ensure no extra origins in connect-src
	unwanted := "connect-src 'self' "
	if strings.Contains(csp, unwanted) {
		t.Errorf("CSP = %q, should not contain %q (no ext auth configured)", csp, unwanted)
	}
}
