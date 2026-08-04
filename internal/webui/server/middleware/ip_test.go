package middleware

import (
	"net/http"
	"testing"
)

func TestExtractClientIP_HostPort(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"ipv4 with port", "192.168.1.1:12345", "192.168.1.1"},
		{"ipv6 with port", "[::1]:8080", "::1"},
		{"localhost with port", "127.0.0.1:54321", "127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tt.remoteAddr}
			got := ExtractClientIP(r)
			if got != tt.want {
				t.Errorf("ExtractClientIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

func TestExtractClientIP_Fallback(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"bare ipv4 no port", "10.0.0.1", "10.0.0.1"},
		{"empty string", "", ""},
		{"unix socket path", "/var/run/app.sock", "/var/run/app.sock"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tt.remoteAddr}
			got := ExtractClientIP(r)
			if got != tt.want {
				t.Errorf("ExtractClientIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
			}
		})
	}
}
