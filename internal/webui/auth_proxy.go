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
		isTLS, _ := resp.Request.Context().Value(ctxKey{}).(bool)
		rewriteAuthCookies(resp, isTLS)
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

// rewriteAuthCookies rewrites Set-Cookie headers from the upstream auth service
// to work as first-party cookies through the same-origin proxy.
func rewriteAuthCookies(resp *http.Response, isTLS bool) {
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, c := range cookies {
		if strings.ContainsAny(c, "\r\n") {
			continue // drop malformed cookie from upstream
		}
		c = stripCookieAttr(c, "Domain")
		c = replaceCookieAttr(c, "SameSite", "Lax")
		if !strings.Contains(strings.ToLower(c), "samesite=") {
			c += "; SameSite=Lax"
		}
		c = stripCookieFlag(c, "Partitioned")
		if isTLS {
			if !hasCookieFlag(c, "Secure") {
				c += "; Secure"
			}
		} else {
			c = stripCookieFlag(c, "Secure")
		}
		resp.Header.Add("Set-Cookie", c)
	}
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
