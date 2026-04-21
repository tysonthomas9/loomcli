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

// authProxyCtxKey threads TLS state from Director into ModifyResponse.
type authProxyCtxKey struct{}

// NewAuthProxy returns a reverse proxy that forwards /api/auth/* requests to
// the external BetterAuth service. This makes auth cookies same-origin with
// the frontend, avoiding cross-site cookie restrictions that block SameSite
// cookies over HTTP.
//
// Returns nil if extAuthURL is empty or invalid.
func NewAuthProxy(extAuthURL string, logger *slog.Logger) http.Handler {
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

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		isTLS := req.TLS != nil ||
			strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
		*req = *req.WithContext(context.WithValue(req.Context(), authProxyCtxKey{}, isTLS))
	}

	proxy.ModifyResponse = rewriteAuthProxyCookies
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

// rewriteAuthProxyCookies rewrites Set-Cookie headers so cookies are
// first-party to the frontend origin (Domain stripped, SameSite=Lax, Secure
// conditioned on TLS). When downgrading to HTTP, __Secure-/__Host- name
// prefixes are also stripped since browsers reject such cookies without the
// Secure attribute. Malformed upstream cookies containing CR/LF are dropped
// as defense-in-depth.
func rewriteAuthProxyCookies(resp *http.Response) error {
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return nil
	}
	isTLS, _ := resp.Request.Context().Value(authProxyCtxKey{}).(bool)
	resp.Header.Del("Set-Cookie")
	for _, c := range cookies {
		if strings.ContainsAny(c, "\r\n") {
			continue // drop malformed cookie from upstream
		}
		c = stripCookieAttr(c, "Domain")
		c = replaceCookieAttr(c, "SameSite", "Lax")
		c = stripCookieFlag(c, "Partitioned")
		if isTLS {
			if !hasCookieFlag(c, "Secure") {
				c += "; Secure"
			}
		} else {
			c = stripCookieFlag(c, "Secure")
			c = stripCookieNamePrefix(c)
		}
		resp.Header.Add("Set-Cookie", c)
	}
	return nil
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
	found := false
	for i, p := range parts {
		trimmed := strings.TrimSpace(p)
		if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(attr)+"=") {
			parts[i] = " " + attr + "=" + newVal
			found = true
		}
	}
	if !found {
		parts = append(parts, " "+attr+"="+newVal)
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

// stripCookieNamePrefix removes a __Secure- or __Host- prefix from the cookie
// name in a raw Set-Cookie header value. Browsers reject cookies with these
// prefixes unless the Secure attribute is present, so when downgrading from
// HTTPS to HTTP the proxy strips both the flag and the name prefix. Prefix
// matching is case-sensitive per RFC 6265bis.
func stripCookieNamePrefix(cookie string) string {
	eqIdx := strings.Index(cookie, "=")
	if eqIdx < 0 {
		return cookie
	}
	name := cookie[:eqIdx]
	for _, prefix := range []string{"__Secure-", "__Host-"} {
		if strings.HasPrefix(name, prefix) {
			return name[len(prefix):] + cookie[eqIdx:]
		}
	}
	return cookie
}
