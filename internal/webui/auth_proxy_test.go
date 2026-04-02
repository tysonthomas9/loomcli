package webui

import "testing"

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
			name:   "attribute absent no-op",
			cookie: "sid=abc; Path=/",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "sid=abc; Path=/",
		},
		{
			name:   "case insensitive",
			cookie: "sid=abc; samesite=None; Path=/",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "sid=abc; SameSite=Lax; Path=/",
		},
		{
			name:   "empty cookie",
			cookie: "",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "",
		},
		{
			name:   "cookie with only value",
			cookie: "sid=abc",
			attr:   "SameSite",
			newVal: "Lax",
			want:   "sid=abc",
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
