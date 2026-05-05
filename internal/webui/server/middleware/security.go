package middleware

import (
	"fmt"
	"net/http"
	"net/url"
)

// SecurityConfig controls optional security headers.
type SecurityConfig struct {
	HSTSEnabled   bool
	ExtAuthOrigin string // Auth service origin to add to CSP connect-src (e.g., "https://auth.loomcli.com")
}

// SecurityHeaders creates a middleware that sets standard HTTP
// security headers on all responses. These headers protect against common
// web attacks (XSS, clickjacking, MIME sniffing, information leakage).
func SecurityHeaders(cfg SecurityConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// Note: 'unsafe-inline' is required in style-src for renderer and app
			// inline styles that do not support CSP nonces. The risk is mitigated
			// by img-src 'self' which prevents CSS-based data exfiltration via
			// background-image URLs.
			//
			// The sha256 hash allows the inline theme-detection script in index.html
			// (prevents flash-of-wrong-theme). If that script changes, regenerate with:
			//   python3 -c "import hashlib,base64;f=open('internal/webui/frontend/index.html').read();s=f[f.index('<script>')+8:f.index('</script>')];print('sha256-'+base64.b64encode(hashlib.sha256(s.encode()).digest()).decode())"
			connectSrc := "'self'"
			if cfg.ExtAuthOrigin != "" {
				connectSrc += " " + cfg.ExtAuthOrigin
			}
			h.Set("Content-Security-Policy",
				fmt.Sprintf("default-src 'self'; script-src 'self' 'sha256-E907z9SPF4o7blRe1MXfQVC2tBrJopXOXrMYZvksy/o='; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src %s; font-src 'self'; frame-ancestors 'none'; report-uri /api/csp-report", connectSrc))
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
			if cfg.HSTSEnabled {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ExtractOrigin returns the scheme://host portion of a URL, suitable for
// CORS and CSP origin matching.
func ExtractOrigin(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
