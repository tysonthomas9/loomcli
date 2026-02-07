package webui

import (
	"net/http"
	"net/url"
	"strings"
)

// CORSConfig holds configuration for the CORS middleware.
type CORSConfig struct {
	Enabled          bool     // Whether CORS is enabled
	AllowedOrigins   []string // List of allowed origins (e.g., "http://localhost:3000")
	RejectDisallowed bool     // When true, reject non-preflight requests from disallowed origins on /api/ routes (always active in internal/webui)
}

// NewCORSMiddleware creates a middleware that adds CORS headers to responses.
// If the config is not enabled, it returns a passthrough handler.
func NewCORSMiddleware(config CORSConfig) func(http.Handler) http.Handler {
	if !config.Enabled {
		// Return passthrough middleware when CORS is disabled
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	// Build allowed origins map for O(1) lookup
	allowedMap := make(map[string]bool, len(config.AllowedOrigins))
	for _, origin := range config.AllowedOrigins {
		// Normalize: remove trailing slash if present
		origin = strings.TrimSuffix(origin, "/")
		allowedMap[origin] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Always set Vary header to help with caching
			w.Header().Add("Vary", "Origin")

			// Handle preflight OPTIONS requests
			if r.Method == http.MethodOptions {
				// Only set CORS headers if origin is present and allowed
				if origin != "" && origin != "null" {
					normalizedOrigin := strings.TrimSuffix(origin, "/")
					if allowedMap[normalizedOrigin] {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
						w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
						w.Header().Set("Access-Control-Allow-Credentials", "true")
						w.Header().Set("Access-Control-Max-Age", "86400")
						w.WriteHeader(http.StatusNoContent)
						return
					}
				}
				// Disallowed or missing origin - reject preflight
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// For non-preflight requests: if Origin is present, it must be
			// same-origin or in the allowed list. Requests without an Origin
			// header (same-origin or non-browser) pass through.
			if origin != "" {
				if origin == "null" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				normalizedOrigin := strings.TrimSuffix(origin, "/")
				sameOrigin := isSameOrigin(normalizedOrigin, r.Host)
				if !sameOrigin && !allowedMap[normalizedOrigin] {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if allowedMap[normalizedOrigin] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Max-Age", "86400")
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isSameOrigin checks whether the given origin URL's host matches the
// request Host header, meaning the request is same-origin.
func isSameOrigin(origin, requestHost string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == requestHost
}
