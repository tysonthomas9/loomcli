package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// discoverAssetFilenames returns the actual CSS and JS asset filenames from the embedded filesystem.
// This allows tests to work regardless of the build-time hashes in filenames.
func discoverAssetFilenames() (cssFile, jsFile string) {
	distFS, err := fs.Sub(frontendFS, "frontend/dist/assets")
	if err != nil {
		return "", ""
	}

	entries, err := fs.ReadDir(distFS, ".")
	if err != nil {
		return "", ""
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "index-") && strings.HasSuffix(name, ".css") {
			cssFile = name
		}
		if strings.HasPrefix(name, "index-") && strings.HasSuffix(name, ".js") && !strings.Contains(name, "react") {
			jsFile = name
		}
	}
	return cssFile, jsFile
}

func TestShouldCache(t *testing.T) {
	tests := []struct {
		name     string
		urlPath  string
		expected bool
	}{
		{
			name:     "index.html should not be cached",
			urlPath:  "/index.html",
			expected: false,
		},
		{
			name:     "root path should not be cached",
			urlPath:  "/",
			expected: false,
		},
		{
			name:     "hashed JS asset should be cached",
			urlPath:  "/assets/index-Dcxw5X7l.js",
			expected: true,
		},
		{
			name:     "hashed CSS asset should be cached",
			urlPath:  "/assets/index-Bj8-cpyB.css",
			expected: true,
		},
		{
			name:     "generic asset path should be cached",
			urlPath:  "/assets/some-file.woff2",
			expected: true,
		},
		{
			name:     "non-asset file should not be cached",
			urlPath:  "/favicon.ico",
			expected: false,
		},
		{
			name:     "nested non-asset path should not be cached",
			urlPath:  "/some/other/path.js",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldCache(tt.urlPath)
			if result != tt.expected {
				t.Errorf("shouldCache(%q) = %v, want %v", tt.urlPath, result, tt.expected)
			}
		})
	}
}

func TestSetCacheHeaders(t *testing.T) {
	tests := []struct {
		name          string
		cache         bool
		wantCacheCtrl string
		wantPragma    string
		wantExpires   string
	}{
		{
			name:          "cached assets get long cache headers",
			cache:         true,
			wantCacheCtrl: "public, max-age=31536000, immutable",
			wantPragma:    "",
			wantExpires:   "",
		},
		{
			name:          "non-cached files get no-cache headers",
			cache:         false,
			wantCacheCtrl: "no-cache, no-store, must-revalidate",
			wantPragma:    "no-cache",
			wantExpires:   "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			setCacheHeaders(w, tt.cache)

			gotCacheCtrl := w.Header().Get("Cache-Control")
			if gotCacheCtrl != tt.wantCacheCtrl {
				t.Errorf("Cache-Control = %q, want %q", gotCacheCtrl, tt.wantCacheCtrl)
			}

			gotPragma := w.Header().Get("Pragma")
			if gotPragma != tt.wantPragma {
				t.Errorf("Pragma = %q, want %q", gotPragma, tt.wantPragma)
			}

			gotExpires := w.Header().Get("Expires")
			if gotExpires != tt.wantExpires {
				t.Errorf("Expires = %q, want %q", gotExpires, tt.wantExpires)
			}
		})
	}
}

func TestFrontendHandler(t *testing.T) {
	handler := frontendHandler()

	// Discover actual asset filenames from the embedded filesystem
	cssFile, jsFile := discoverAssetFilenames()
	if cssFile == "" || jsFile == "" {
		t.Skip("Could not discover asset filenames from embedded filesystem")
	}

	tests := []struct {
		name             string
		path             string
		wantStatus       int
		wantCacheControl string
		wantBodyContains string
	}{
		{
			name:             "root path serves index.html with no-cache",
			path:             "/",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache, no-store, must-revalidate",
			wantBodyContains: "<!DOCTYPE html>",
		},
		{
			name:             "explicit index.html redirects to root",
			path:             "/index.html",
			wantStatus:       http.StatusMovedPermanently, // FileServer redirects /index.html to /
			wantCacheControl: "no-cache, no-store, must-revalidate",
			wantBodyContains: "", // redirect response has no body
		},
		{
			name:             "non-existent path serves index.html (SPA routing)",
			path:             "/dashboard",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache, no-store, must-revalidate",
			wantBodyContains: "<!DOCTYPE html>",
		},
		{
			name:             "deep non-existent path serves index.html (SPA routing)",
			path:             "/issues/123/details",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache, no-store, must-revalidate",
			wantBodyContains: "<!DOCTYPE html>",
		},
		{
			name:             "hashed CSS asset serves with long cache",
			path:             "/assets/" + cssFile,
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
			wantBodyContains: "", // don't check body for assets
		},
		{
			name:             "hashed JS asset serves with long cache",
			path:             "/assets/" + jsFile,
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
			wantBodyContains: "", // don't check body for assets
		},
		{
			name:             "non-existent asset serves index.html (SPA routing)",
			path:             "/assets/nonexistent.js",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache, no-store, must-revalidate",
			wantBodyContains: "<!DOCTYPE html>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			gotCacheControl := w.Header().Get("Cache-Control")
			if gotCacheControl != tt.wantCacheControl {
				t.Errorf("Cache-Control = %q, want %q", gotCacheControl, tt.wantCacheControl)
			}

			if tt.wantBodyContains != "" {
				body := w.Body.String()
				if !strings.Contains(body, tt.wantBodyContains) {
					t.Errorf("body does not contain %q, got: %s", tt.wantBodyContains, body[:min(100, len(body))])
				}
			}
		})
	}
}

func TestFrontendHandlerSPARouting(t *testing.T) {
	handler := frontendHandler()

	// Test various SPA routes that should all serve index.html
	spaRoutes := []string{
		"/dashboard",
		"/settings",
		"/issues",
		"/issues/123",
		"/issues/123/edit",
		"/user/profile",
		"/some/deep/nested/route",
	}

	// First, get the expected index.html content
	reqIndex := httptest.NewRequest(http.MethodGet, "/", nil)
	wIndex := httptest.NewRecorder()
	handler.ServeHTTP(wIndex, reqIndex)
	expectedBody := wIndex.Body.String()

	for _, route := range spaRoutes {
		t.Run("SPA route: "+route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, route, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}

			if w.Body.String() != expectedBody {
				t.Errorf("body does not match index.html content")
			}

			// Verify no-cache headers are set for SPA routes
			gotPragma := w.Header().Get("Pragma")
			if gotPragma != "no-cache" {
				t.Errorf("Pragma = %q, want %q", gotPragma, "no-cache")
			}
		})
	}
}

func TestHandleHealth(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		wantStatus     int
		wantBodyStatus string
	}{
		{
			name:           "GET returns healthy status",
			method:         http.MethodGet,
			wantStatus:     http.StatusOK,
			wantBodyStatus: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/health", nil)
			w := httptest.NewRecorder()

			handleHealth(nil)(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			contentType := w.Header().Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
			}

			var response map[string]string
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if response["status"] != tt.wantBodyStatus {
				t.Errorf("response status = %q, want %q", response["status"], tt.wantBodyStatus)
			}
		})
	}
}

func TestSetupRoutes(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "") // nil pool, hub, getMutationsSince, termManager for basic routing tests

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "health endpoint responds",
			method:     http.MethodGet,
			path:       "/health",
			wantStatus: http.StatusOK,
		},
		{
			name:       "root path serves frontend",
			method:     http.MethodGet,
			path:       "/",
			wantStatus: http.StatusOK,
		},
		{
			name:       "SPA route serves frontend",
			method:     http.MethodGet,
			path:       "/dashboard",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestHealthEndpointJSON(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "") // nil pool, hub, getMutationsSince, termManager for basic health tests

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// Verify it's valid JSON
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	// Verify expected fields
	status, ok := response["status"]
	if !ok {
		t.Error("response missing 'status' field")
	}
	if status != "ok" {
		t.Errorf("status = %v, want %q", status, "ok")
	}
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
