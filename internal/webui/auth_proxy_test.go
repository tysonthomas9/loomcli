package webui

import (
	"context"
	"net/http"
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
