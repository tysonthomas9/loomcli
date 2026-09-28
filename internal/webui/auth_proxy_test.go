package webui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRewriteAuthProxyCookies_DropsMalformedCRLF(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example/api/auth/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), authProxyCtxKey{}, false))
	resp := &http.Response{Header: http.Header{}, Request: req}
	resp.Header.Add("Set-Cookie", "good=1; Domain=.example.com")
	resp.Header.Add("Set-Cookie", "bad=2\r\nX-Injected: yes")

	if err := rewriteAuthProxyCookies(resp); err != nil {
		t.Fatalf("rewrite returned error: %v", err)
	}

	got := resp.Header.Values("Set-Cookie")
	if len(got) != 1 {
		t.Fatalf("expected 1 cookie after drop, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "good=1") {
		t.Errorf("expected good cookie preserved, got %q", got[0])
	}
	if resp.Header.Get("X-Injected") != "" {
		t.Errorf("header injection should not survive CRLF drop, got %q", resp.Header.Get("X-Injected"))
	}
}

func TestRewriteAuthProxyCookies_AppendsSameSiteWhenMissing(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example/api/auth/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), authProxyCtxKey{}, false))
	resp := &http.Response{Header: http.Header{}, Request: req}
	resp.Header.Add("Set-Cookie", "session=abc; HttpOnly")

	if err := rewriteAuthProxyCookies(resp); err != nil {
		t.Fatalf("rewrite returned error: %v", err)
	}

	got := resp.Header.Get("Set-Cookie")
	if !strings.Contains(got, "SameSite=Lax") {
		t.Errorf("expected SameSite=Lax appended, got %q", got)
	}
}

func TestReplaceCookieAttr_ExistingAttr(t *testing.T) {
	got := replaceCookieAttr("session=abc; SameSite=None; HttpOnly", "SameSite", "Lax")
	if !strings.Contains(got, "SameSite=Lax") {
		t.Errorf("expected SameSite=Lax, got %q", got)
	}
	if strings.Contains(got, "SameSite=None") {
		t.Errorf("expected old value replaced, got %q", got)
	}
}

func TestReplaceCookieAttr_AppendsWhenMissing(t *testing.T) {
	got := replaceCookieAttr("session=abc; HttpOnly", "SameSite", "Lax")
	if !strings.Contains(got, "SameSite=Lax") {
		t.Errorf("expected SameSite=Lax appended, got %q", got)
	}
}

func TestStripCookieAttr_RemovesNamedAttr(t *testing.T) {
	got := stripCookieAttr("session=abc; Domain=.example.com; Path=/", "Domain")
	if strings.Contains(strings.ToLower(got), "domain=") {
		t.Errorf("expected Domain stripped, got %q", got)
	}
	if !strings.Contains(got, "Path=/") {
		t.Errorf("expected Path preserved, got %q", got)
	}
}

func TestStripCookieFlag_RemovesFlag(t *testing.T) {
	got := stripCookieFlag("session=abc; Secure; HttpOnly", "Secure")
	if strings.Contains(got, "Secure") {
		t.Errorf("expected Secure stripped, got %q", got)
	}
	if !strings.Contains(got, "HttpOnly") {
		t.Errorf("expected HttpOnly preserved, got %q", got)
	}
}

func TestHasCookieFlag(t *testing.T) {
	if !hasCookieFlag("session=abc; Secure", "Secure") {
		t.Error("expected Secure detected")
	}
	if hasCookieFlag("session=abc; HttpOnly", "Secure") {
		t.Error("expected Secure absent")
	}
}

func TestNewAuthProxy_EmptyURL(t *testing.T) {
	if NewAuthProxy("", nil) != nil {
		t.Error("expected nil for empty URL")
	}
}

func TestNewAuthProxy_InvalidURL(t *testing.T) {
	if NewAuthProxy("not a url", nil) != nil {
		t.Error("expected nil for invalid URL")
	}
}

func TestNewAuthProxy_ValidURL(t *testing.T) {
	if NewAuthProxy("https://auth.example.com", nil) == nil {
		t.Error("expected non-nil for valid URL")
	}
}

func TestNewAuthProxy_UsesRewriteOnly(t *testing.T) {
	proxy, ok := NewAuthProxy("https://auth.example.com", nil).(*httputil.ReverseProxy)
	if !ok {
		t.Fatal("expected reverse proxy handler")
	}
	if proxy.Rewrite == nil {
		t.Error("expected Rewrite callback")
	}
	if proxy.Director != nil {
		t.Error("Director and Rewrite must not both be configured")
	}
}

func TestNewAuthProxy_ForwardsTargetAndTrustedRequestMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Path", r.URL.EscapedPath())
		w.Header().Set("X-Seen-Query", r.URL.RawQuery)
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-Forwarded-For", r.Header.Get("X-Forwarded-For"))
		w.Header().Set("X-Seen-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
		w.Header().Set("X-Seen-Forwarded-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Add("Set-Cookie", "session=abc; Domain=auth.example.com; SameSite=None")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	handler := NewAuthProxy(upstream.URL+"/better-auth", nil)
	req := httptest.NewRequest(http.MethodGet, "https://frontend.example/api/auth/session?next=%2Fhome", nil)
	req.Header.Set("X-Forwarded-For", "spoofed-client")
	req.Header.Set("X-Forwarded-Host", "spoofed.example")
	req.Header.Set("X-Forwarded-Proto", "http")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if got, want := resp.Header.Get("X-Seen-Path"), "/better-auth/api/auth/session"; got != want {
		t.Errorf("upstream path = %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("X-Seen-Query"), "next=%2Fhome"; got != want {
		t.Errorf("upstream query = %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("X-Seen-Host"), strings.TrimPrefix(upstream.URL, "http://"); got != want {
		t.Errorf("upstream Host = %q, want %q", got, want)
	}
	if got := resp.Header.Get("X-Seen-Forwarded-For"); got == "" || strings.Contains(got, "spoofed-client") {
		t.Errorf("X-Forwarded-For = %q, want trusted client address only", got)
	}
	if got, want := resp.Header.Get("X-Seen-Forwarded-Host"), "frontend.example"; got != want {
		t.Errorf("X-Forwarded-Host = %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("X-Seen-Forwarded-Proto"), "https"; got != want {
		t.Errorf("X-Forwarded-Proto = %q, want %q", got, want)
	}
	cookie := resp.Header.Get("Set-Cookie")
	if strings.Contains(strings.ToLower(cookie), "domain=") {
		t.Errorf("rewritten cookie retained Domain: %q", cookie)
	}
	if !strings.Contains(cookie, "SameSite=Lax") || !hasCookieFlag(cookie, "Secure") {
		t.Errorf("TLS cookie = %q, want SameSite=Lax and Secure", cookie)
	}
}

func TestNewAuthProxy_ForwardedTLSContextReachesCookieRewrite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", "session=abc; SameSite=None")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	req := httptest.NewRequest(http.MethodGet, "http://frontend.example/api/auth/session", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	NewAuthProxy(upstream.URL, nil).ServeHTTP(recorder, req)

	if cookie := recorder.Header().Get("Set-Cookie"); !hasCookieFlag(cookie, "Secure") {
		t.Errorf("forwarded TLS cookie = %q, want Secure", cookie)
	}
}

func TestNewAuthProxy_UpstreamErrorReturnsStableBadGateway(t *testing.T) {
	proxy, ok := NewAuthProxy("https://auth.example.com", nil).(*httputil.ReverseProxy)
	if !ok {
		t.Fatal("expected reverse proxy handler")
	}
	proxy.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upstream unavailable")
	})

	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://frontend.example/api/auth/session", nil))

	if got, want := recorder.Code, http.StatusBadGateway; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := recorder.Body.String(), "{\"error\":\"auth service unavailable\"}\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestCookieStripAttr(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		attr   string
		want   string
	}{
		{
			name:   "attribute present in middle",
			cookie: "sid=abc; Domain=example.com; Path=/; HttpOnly",
			attr:   "Domain",
			want:   "sid=abc; Path=/; HttpOnly",
		},
		{
			name:   "attribute absent",
			cookie: "sid=abc; Path=/; HttpOnly",
			attr:   "Domain",
			want:   "sid=abc; Path=/; HttpOnly",
		},
		{
			name:   "attribute at start (not cookie value)",
			cookie: "Path=/; Domain=example.com; HttpOnly",
			attr:   "Path",
			want:   " Domain=example.com; HttpOnly",
		},
		{
			name:   "attribute at end",
			cookie: "sid=abc; Path=/; Domain=example.com",
			attr:   "Domain",
			want:   "sid=abc; Path=/",
		},
		{
			name:   "case insensitive",
			cookie: "sid=abc; domain=example.com; Path=/",
			attr:   "Domain",
			want:   "sid=abc; Path=/",
		},
		{
			name:   "multiple attributes stripped",
			cookie: "sid=abc; Domain=a.com; Path=/; Domain=b.com",
			attr:   "Domain",
			want:   "sid=abc; Path=/",
		},
		{
			name:   "empty cookie",
			cookie: "",
			attr:   "Domain",
			want:   "",
		},
		{
			name:   "cookie with only value no attributes",
			cookie: "sid=abc",
			attr:   "Domain",
			want:   "sid=abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCookieAttr(tt.cookie, tt.attr)
			if got != tt.want {
				t.Errorf("stripCookieAttr(%q, %q) = %q, want %q", tt.cookie, tt.attr, got, tt.want)
			}
		})
	}
}

func TestCookieReplaceCookieAttr(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		attr   string
		newVal string
		want   string
	}{
		{
			name:   "attribute present replaced",
			cookie: "sid=abc; SameSite=None; Path=/",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "sid=abc; SameSite=Lax; Path=/",
		},
		{
			name:   "attribute absent appended",
			cookie: "sid=abc; Path=/",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "sid=abc; Path=/; SameSite=Lax",
		},
		{
			name:   "case insensitive",
			cookie: "sid=abc; samesite=None; Path=/",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "sid=abc; SameSite=Lax; Path=/",
		},
		{
			name:   "cookie with only value",
			cookie: "sid=abc",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "sid=abc; SameSite=Lax",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceCookieAttr(tt.cookie, tt.attr, tt.newVal)
			if got != tt.want {
				t.Errorf("replaceCookieAttr(%q, %q, %q) = %q, want %q", tt.cookie, tt.attr, tt.newVal, got, tt.want)
			}
		})
	}
}

func TestCookieHasFlag(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		flag   string
		want   bool
	}{
		{
			name:   "flag present",
			cookie: "sid=abc; Secure; Path=/",
			flag:   "Secure",
			want:   true,
		},
		{
			name:   "flag absent",
			cookie: "sid=abc; Path=/",
			flag:   "Secure",
			want:   false,
		},
		{
			name:   "case insensitive",
			cookie: "sid=abc; secure; Path=/",
			flag:   "Secure",
			want:   true,
		},
		{
			name:   "flag as substring of attribute value should not match",
			cookie: "sid=abc; SameSite=Secure; Path=/",
			flag:   "Secure",
			want:   false,
		},
		{
			name:   "empty cookie",
			cookie: "",
			flag:   "Secure",
			want:   false,
		},
		{
			name:   "cookie with only value",
			cookie: "sid=abc",
			flag:   "Secure",
			want:   false,
		},
		{
			name:   "flag at end",
			cookie: "sid=abc; Path=/; HttpOnly",
			flag:   "HttpOnly",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasCookieFlag(tt.cookie, tt.flag)
			if got != tt.want {
				t.Errorf("hasCookieFlag(%q, %q) = %v, want %v", tt.cookie, tt.flag, got, tt.want)
			}
		})
	}
}

func TestCookieStripFlag(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		flag   string
		want   string
	}{
		{
			name:   "flag present removed",
			cookie: "sid=abc; Secure; Path=/",
			flag:   "Secure",
			want:   "sid=abc; Path=/",
		},
		{
			name:   "flag absent no-op",
			cookie: "sid=abc; Path=/",
			flag:   "Secure",
			want:   "sid=abc; Path=/",
		},
		{
			name:   "case insensitive",
			cookie: "sid=abc; secure; Path=/",
			flag:   "Secure",
			want:   "sid=abc; Path=/",
		},
		{
			name:   "does not strip attribute with same prefix",
			cookie: "sid=abc; SameSite=Lax; Secure; Path=/",
			flag:   "Secure",
			want:   "sid=abc; SameSite=Lax; Path=/",
		},
		{
			name:   "empty cookie",
			cookie: "",
			flag:   "Secure",
			want:   "",
		},
		{
			name:   "cookie with only value",
			cookie: "sid=abc",
			flag:   "Secure",
			want:   "sid=abc",
		},
		{
			name:   "partitioned flag removed",
			cookie: "sid=abc; Partitioned; Path=/; Secure",
			flag:   "Partitioned",
			want:   "sid=abc; Path=/; Secure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCookieFlag(tt.cookie, tt.flag)
			if got != tt.want {
				t.Errorf("stripCookieFlag(%q, %q) = %q, want %q", tt.cookie, tt.flag, got, tt.want)
			}
		})
	}
}
