package webui

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// newAuthProxy returns a reverse proxy that forwards /api/auth/* requests to
// the external BetterAuth service. This makes auth cookies same-origin with
// the frontend, avoiding cross-site cookie restrictions that block SameSite
// cookies over HTTP.
//
// Returns nil if extAuthURL is empty or invalid.
func newAuthProxy(extAuthURL string, logger *slog.Logger) http.Handler {
	if extAuthURL == "" {
		return nil
	}

	target, err := url.Parse(extAuthURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		if logger != nil {
			logger.Warn("auth proxy disabled: invalid auth URL", "url", extAuthURL, "error", err)
		}
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}

	type ctxKey struct{}
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		// Thread TLS state into context so ModifyResponse can condition on it.
		isTLS := req.TLS != nil ||
			strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
		*req = *req.WithContext(context.WithValue(req.Context(), ctxKey{}, isTLS))
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		// Rewrite Set-Cookie domains to match the frontend origin so
		// cookies are stored as first-party.
		if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
			isTLS, _ := resp.Request.Context().Value(ctxKey{}).(bool)
			resp.Header.Del("Set-Cookie")
			for _, c := range cookies {
				if strings.ContainsAny(c, "\r\n") {
					continue // drop malformed cookie from upstream
				}
				// Strip Domain= so the cookie defaults to the request host
				c = stripCookieAttr(c, "Domain")
				// Replace SameSite=None with Lax (same-origin proxy)
				c = replaceCookieAttr(c, "SameSite", "Lax")
				// If SameSite was absent, replaceCookieAttr is a no-op; append it.
				if !strings.Contains(strings.ToLower(c), "samesite=") {
					c = c + "; SameSite=Lax"
				}
				// Remove Partitioned flag (not needed for same-origin)
				c = stripCookieFlag(c, "Partitioned")
				if isTLS {
					// Ensure Secure is present (required for __Secure- prefix cookies)
					if !hasCookieFlag(c, "Secure") {
						c = c + "; Secure"
					}
				} else {
					// Strip Secure for plain HTTP (browsers reject Secure cookies over HTTP)
					c = stripCookieFlag(c, "Secure")
				}
				resp.Header.Add("Set-Cookie", c)
			}
		}
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if logger != nil {
			logger.Error("auth proxy error", "method", r.Method, "path", r.URL.Path, "error", err)
		}
		http.Error(w, `{"error":"auth service unavailable"}`, http.StatusBadGateway)
	}

	if logger != nil {
		logger.Info("auth proxy enabled", "target", extAuthURL)
	}

	return proxy
}

// stripCookieAttr removes a named attribute (e.g. "Domain") from a Set-Cookie header value.
func stripCookieAttr(cookie, attr string) string {
	parts := strings.Split(cookie, ";")
	filtered := parts[:0]
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if !strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(attr)+"=") {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, ";")
}

// replaceCookieAttr replaces a named attribute's value in a Set-Cookie header.
func replaceCookieAttr(cookie, attr, newVal string) string {
	parts := strings.Split(cookie, ";")
	for i, p := range parts {
		trimmed := strings.TrimSpace(p)
		if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(attr)+"=") {
			parts[i] = " " + attr + "=" + newVal
		}
	}
	return strings.Join(parts, ";")
}

// hasCookieFlag checks if a boolean flag (e.g. "Secure") is present in a Set-Cookie header.
func hasCookieFlag(cookie, flag string) bool {
	for _, p := range strings.Split(cookie, ";") {
		if strings.EqualFold(strings.TrimSpace(p), flag) {
			return true
		}
	}
	return false
}

// stripCookieFlag removes a boolean flag (e.g. "Secure", "Partitioned") from a Set-Cookie header.
func stripCookieFlag(cookie, flag string) string {
	parts := strings.Split(cookie, ";")
	filtered := parts[:0]
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if !strings.EqualFold(trimmed, flag) {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, ";")
}
