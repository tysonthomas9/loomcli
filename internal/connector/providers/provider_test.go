package providers

import (
	"strings"
	"testing"
)

func TestSanitizeUpstreamMessage_Shapes(t *testing.T) {
	slackBotToken := "xox" + "b-1111111111-2222222222-AbCdEfGhIjKlMnOpQrStUvWx"
	slackAppToken := "x" + "app-1-A0000-12345-deadbeef"
	highEntropySecret := "ajNQ9zLp7WmK4sVx2BcY8TdR5FgH0JrE6UiO1"

	tests := []struct {
		name       string
		msg        string
		credential string
		rawSecret  string
		want       string
		contains   string
	}{
		{
			name:      "slack bot token",
			msg:       "auth failed for " + slackBotToken,
			rawSecret: slackBotToken,
			want:      "auth failed for [redacted]",
		},
		{
			name:      "slack app token",
			msg:       slackAppToken + " rejected",
			rawSecret: slackAppToken,
			want:      "[redacted] rejected",
		},
		{
			name:      "datadog api key with header context",
			msg:       "Invalid DD-API-KEY: 0123456789abcdef0123456789abcdef",
			rawSecret: "0123456789abcdef0123456789abcdef",
			want:      "Invalid [redacted]",
		},
		{
			name:      "datadog app key with text context",
			msg:       "datadog app key 0123456789abcdef0123456789abcdef01234567 rejected",
			rawSecret: "0123456789abcdef0123456789abcdef01234567",
			want:      "[redacted] rejected",
		},
		{
			name:      "bearer echo",
			msg:       "header Bearer some-opaque-value rejected",
			rawSecret: "some-opaque-value",
			want:      "header [redacted] rejected",
		},
		{
			name:      "github pat",
			msg:       "github_pat_11AAAA0000bbbbCCCC leaked",
			rawSecret: "github_pat_11AAAA0000bbbbCCCC",
			want:      "[redacted] leaked",
		},
		{
			name:       "literal credential",
			msg:        "bad " + testToken + " rejected",
			credential: testToken,
			rawSecret:  testToken,
			want:       "bad [redacted] rejected",
		},
		{
			name:      "canonical backstop",
			msg:       "opaque " + highEntropySecret + " leaked",
			rawSecret: highEntropySecret,
			want:      "opaque REDACTED leaked",
		},
		{
			name:     "control characters collapse",
			msg:      "line1\nline2",
			want:     "line1 line2",
			contains: "line1 line2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeUpstreamMessage(tt.msg, tt.credential)
			if got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.msg, got, tt.want)
			}
			if tt.rawSecret != "" && strings.Contains(got, tt.rawSecret) {
				t.Errorf("secret survived sanitization: %q", got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("sanitize(%q) = %q, want to contain %q", tt.msg, got, tt.contains)
			}
		})
	}
}

func TestSanitizeUpstreamMessage_PreservesBareHexSHA(t *testing.T) {
	sha := "5f3a1b2c4d6e8f0a1b2c3d4e5f6a7b8c9d0e1f2a"
	msg := "merge failed at " + sha + " (head)"

	got := sanitizeUpstreamMessage(msg, "")
	if got != msg {
		t.Errorf("sanitize(%q) = %q, want unchanged", msg, got)
	}
	if !strings.Contains(got, sha) {
		t.Errorf("bare sha was redacted: %q", got)
	}
}

func TestSanitizeUpstreamMessage_LengthCap(t *testing.T) {
	got := sanitizeUpstreamMessage(strings.Repeat("x", 500), "")
	if len(got) > maxSanitizedLen+3 {
		t.Errorf("sanitized length %d exceeds cap", len(got))
	}
}
