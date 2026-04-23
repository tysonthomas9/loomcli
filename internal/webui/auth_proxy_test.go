package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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

// TestNewAuthProxy_RewriteSetsContext exercises the Rewrite callback end-to-end
// to verify that the outbound request carries the correct Host header and that
// TLS state — determined from the inbound request — is threaded through the
// context so that ModifyResponse emits cookies with or without the Secure flag.
func TestNewAuthProxy_RewriteSetsContext(t *testing.T) {
	var gotHost, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotPath = r.URL.Path
		w.Header().Add("Set-Cookie", "session=abc; HttpOnly")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy := NewAuthProxy(upstream.URL, nil)
	if proxy == nil {
		t.Fatal("NewAuthProxy returned nil")
	}
	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	t.Run("plain_HTTP_no_Secure_flag", func(t *testing.T) {
		gotHost, gotPath = "", ""
		req, _ := http.NewRequest("GET", proxySrv.URL+"/api/auth/session", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if gotHost != upstreamURL.Host {
			t.Errorf("expected upstream Host %q, got %q", upstreamURL.Host, gotHost)
		}
		if gotPath != "/api/auth/session" {
			t.Errorf("expected upstream path %q, got %q", "/api/auth/session", gotPath)
		}
		cookies := resp.Header.Values("Set-Cookie")
		if len(cookies) != 1 {
			t.Fatalf("expected 1 cookie, got %d: %v", len(cookies), cookies)
		}
		if hasCookieFlag(cookies[0], "Secure") {
			t.Errorf("expected Secure absent on plain HTTP, got %q", cookies[0])
		}
	})

	t.Run("X_Forwarded_Proto_https_adds_Secure_flag", func(t *testing.T) {
		gotHost, gotPath = "", ""
		req, _ := http.NewRequest("GET", proxySrv.URL+"/api/auth/session", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if gotHost != upstreamURL.Host {
			t.Errorf("expected upstream Host %q, got %q", upstreamURL.Host, gotHost)
		}
		if gotPath != "/api/auth/session" {
			t.Errorf("expected upstream path %q, got %q", "/api/auth/session", gotPath)
		}
		cookies := resp.Header.Values("Set-Cookie")
		if len(cookies) != 1 {
			t.Fatalf("expected 1 cookie, got %d: %v", len(cookies), cookies)
		}
		if !hasCookieFlag(cookies[0], "Secure") {
			t.Errorf("expected Secure added when X-Forwarded-Proto=https, got %q", cookies[0])
		}
	})
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

func newFakeAuthUpstream(t *testing.T, cookies []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, c := range cookies {
			w.Header().Add("Set-Cookie", c)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
}

func findCookie(cookies []string, namePrefix string) string {
	for _, c := range cookies {
		if strings.HasPrefix(strings.TrimSpace(c), namePrefix) {
			return c
		}
	}
	return ""
}

func assertCookieContains(t *testing.T, cookie, substr string) {
	t.Helper()
	if !strings.Contains(cookie, substr) {
		t.Errorf("expected cookie to contain %q, got %q", substr, cookie)
	}
}

func assertCookieNotContains(t *testing.T, cookie, substr string) {
	t.Helper()
	if strings.Contains(cookie, substr) {
		t.Errorf("expected cookie NOT to contain %q, got %q", substr, cookie)
	}
}

func TestModifyResponse_Integration(t *testing.T) {
	tests := []struct {
		name            string
		upstreamCookies []string
		isTLS           bool
		check           func(t *testing.T, resp *http.Response)
	}{
		{
			name:            "TLS_on_preserves_Secure",
			upstreamCookies: []string{"session=abc; Domain=auth.example.com; Secure; HttpOnly; SameSite=None; Path=/"},
			isTLS:           true,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 1 {
					t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
				}
				c := got[0]
				assertCookieNotContains(t, strings.ToLower(c), "domain=")
				assertCookieContains(t, c, "Secure")
				assertCookieContains(t, c, "SameSite=Lax")
				assertCookieNotContains(t, c, "SameSite=None")
				assertCookieNotContains(t, c, "Partitioned")
				assertCookieContains(t, c, "HttpOnly")
				assertCookieContains(t, c, "Path=/")
				assertCookieContains(t, c, "session=abc")
			},
		},
		{
			name:            "TLS_off_strips_Secure",
			upstreamCookies: []string{"session=abc; Domain=auth.example.com; Secure; HttpOnly; SameSite=None; Path=/"},
			isTLS:           false,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 1 {
					t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
				}
				c := got[0]
				if hasCookieFlag(c, "Secure") {
					t.Errorf("expected Secure absent on HTTP, got %q", c)
				}
				assertCookieContains(t, c, "SameSite=Lax")
				assertCookieNotContains(t, strings.ToLower(c), "domain=")
			},
		},
		{
			name:            "SameSite_None_replaced_with_Lax",
			upstreamCookies: []string{"token=xyz; SameSite=None"},
			isTLS:           false,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 1 {
					t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
				}
				assertCookieContains(t, got[0], "SameSite=Lax")
				assertCookieNotContains(t, got[0], "SameSite=None")
			},
		},
		{
			name:            "absent_SameSite_appended",
			upstreamCookies: []string{"token=xyz; HttpOnly"},
			isTLS:           false,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 1 {
					t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
				}
				assertCookieContains(t, got[0], "SameSite=Lax")
			},
		},
		{
			name:            "Partitioned_stripped",
			upstreamCookies: []string{"session=abc; Partitioned; Secure; HttpOnly"},
			isTLS:           true,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 1 {
					t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
				}
				assertCookieNotContains(t, got[0], "Partitioned")
				assertCookieContains(t, got[0], "Secure")
			},
		},
		{
			name: "multiple_cookies_rewritten_independently",
			upstreamCookies: []string{
				"a=1; Domain=x.example.com; SameSite=None; HttpOnly",
				"b=2; Secure; Partitioned",
				"c=3; Path=/api",
			},
			isTLS: false,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 3 {
					t.Fatalf("expected 3 cookies, got %d: %v", len(got), got)
				}
				a := findCookie(got, "a=")
				if a == "" {
					t.Fatalf("missing cookie a, got %v", got)
				}
				assertCookieNotContains(t, strings.ToLower(a), "domain=")
				assertCookieContains(t, a, "SameSite=Lax")
				assertCookieContains(t, a, "HttpOnly")

				b := findCookie(got, "b=")
				if b == "" {
					t.Fatalf("missing cookie b, got %v", got)
				}
				if hasCookieFlag(b, "Secure") {
					t.Errorf("expected Secure stripped on HTTP for b, got %q", b)
				}
				assertCookieNotContains(t, b, "Partitioned")
				assertCookieContains(t, b, "SameSite=Lax")

				c := findCookie(got, "c=")
				if c == "" {
					t.Fatalf("missing cookie c, got %v", got)
				}
				assertCookieContains(t, c, "Path=/api")
				assertCookieContains(t, c, "SameSite=Lax")
			},
		},
		{
			name:            "no_cookies_passthrough",
			upstreamCookies: nil,
			isTLS:           false,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 0 {
					t.Fatalf("expected 0 cookies, got %d: %v", len(got), got)
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
				}
			},
		},
		{
			name:            "TLS_on_adds_Secure_flag_when_missing",
			upstreamCookies: []string{"session=abc; HttpOnly"},
			isTLS:           true,
			check: func(t *testing.T, resp *http.Response) {
				got := resp.Header.Values("Set-Cookie")
				if len(got) != 1 {
					t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
				}
				if !hasCookieFlag(got[0], "Secure") {
					t.Errorf("expected Secure added on TLS, got %q", got[0])
				}
				assertCookieContains(t, got[0], "SameSite=Lax")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newFakeAuthUpstream(t, tc.upstreamCookies)
			defer upstream.Close()

			proxy := NewAuthProxy(upstream.URL, nil)
			if proxy == nil {
				t.Fatal("NewAuthProxy returned nil")
			}
			proxySrv := httptest.NewServer(proxy)
			defer proxySrv.Close()

			req, err := http.NewRequest("GET", proxySrv.URL+"/api/auth/session", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if tc.isTLS {
				req.Header.Set("X-Forwarded-Proto", "https")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			tc.check(t, resp)
		})
	}
}

// CRLF-injection at the integration level is not testable through the Go
// HTTP stack: http.ResponseWriter strips CR/LF on write, and http.Response
// parses CR/LF in upstream header values as new-header delimiters on read.
// The CRLF guard in rewriteAuthProxyCookies is defense-in-depth and is
// exercised by TestRewriteAuthProxyCookies_DropsMalformedCRLF at the unit
// level (direct call on a crafted http.Response).

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

func TestStripCookieNamePrefix(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		want   string
	}{
		{
			name:   "__Secure- prefix stripped",
			cookie: "__Secure-better-auth.session_token=abc",
			want:   "better-auth.session_token=abc",
		},
		{
			name:   "__Host- prefix stripped",
			cookie: "__Host-session=abc; Path=/",
			want:   "session=abc; Path=/",
		},
		{
			name:   "no prefix no-op",
			cookie: "session=abc",
			want:   "session=abc",
		},
		{
			name:   "case-sensitive lowercase __secure- NOT stripped",
			cookie: "__secure-foo=bar",
			want:   "__secure-foo=bar",
		},
		{
			name:   "cookie with no = sign is no-op",
			cookie: "malformed",
			want:   "malformed",
		},
		{
			name:   "empty string is no-op",
			cookie: "",
			want:   "",
		},
		{
			name:   "value contains = does not affect first =",
			cookie: "__Secure-token=base64==value; Secure",
			want:   "token=base64==value; Secure",
		},
		{
			name:   "attributes preserved after name",
			cookie: "__Secure-session=abc; HttpOnly; Path=/",
			want:   "session=abc; HttpOnly; Path=/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCookieNamePrefix(tt.cookie)
			if got != tt.want {
				t.Errorf("stripCookieNamePrefix(%q) = %q, want %q", tt.cookie, got, tt.want)
			}
		})
	}
}

func TestRewriteAuthProxyCookies_StripsSecurePrefixOverHTTP(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example/api/auth/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), authProxyCtxKey{}, false))
	resp := &http.Response{Header: http.Header{}, Request: req}
	resp.Header.Add("Set-Cookie", "__Secure-better-auth.session_token=abc; Secure; HttpOnly; SameSite=None")

	if err := rewriteAuthProxyCookies(resp); err != nil {
		t.Fatalf("rewrite returned error: %v", err)
	}

	got := resp.Header.Values("Set-Cookie")
	if len(got) != 1 {
		t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
	}
	c := got[0]
	assertCookieContains(t, c, "better-auth.session_token=abc")
	assertCookieNotContains(t, c, "__Secure-")
	if hasCookieFlag(c, "Secure") {
		t.Errorf("expected Secure absent on HTTP, got %q", c)
	}
	assertCookieContains(t, c, "SameSite=Lax")
	assertCookieContains(t, c, "HttpOnly")
}

func TestRewriteAuthProxyCookies_PreservesSecurePrefixOverTLS(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example/api/auth/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), authProxyCtxKey{}, true))
	resp := &http.Response{Header: http.Header{}, Request: req}
	resp.Header.Add("Set-Cookie", "__Secure-better-auth.session_token=abc; Secure; HttpOnly; SameSite=None")

	if err := rewriteAuthProxyCookies(resp); err != nil {
		t.Fatalf("rewrite returned error: %v", err)
	}

	got := resp.Header.Values("Set-Cookie")
	if len(got) != 1 {
		t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
	}
	c := got[0]
	assertCookieContains(t, c, "__Secure-better-auth.session_token=abc")
	if !hasCookieFlag(c, "Secure") {
		t.Errorf("expected Secure present on TLS, got %q", c)
	}
	assertCookieContains(t, c, "SameSite=Lax")
}

func TestRewriteAuthProxyCookies_StripsHostPrefixOverHTTP(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example/api/auth/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), authProxyCtxKey{}, false))
	resp := &http.Response{Header: http.Header{}, Request: req}
	resp.Header.Add("Set-Cookie", "__Host-session=abc; Secure; Path=/")

	if err := rewriteAuthProxyCookies(resp); err != nil {
		t.Fatalf("rewrite returned error: %v", err)
	}

	got := resp.Header.Values("Set-Cookie")
	if len(got) != 1 {
		t.Fatalf("expected 1 cookie, got %d: %v", len(got), got)
	}
	c := got[0]
	assertCookieContains(t, c, "session=abc")
	assertCookieNotContains(t, c, "__Host-")
	if hasCookieFlag(c, "Secure") {
		t.Errorf("expected Secure absent on HTTP, got %q", c)
	}
	assertCookieContains(t, c, "Path=/")
}
