package authmode

import "testing"

func TestValid(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"open", true},
		{"oidc", true},
		{"none", false},
		{"external", false},
		{"", false},
		{"banana", false},
	}
	for _, tt := range tests {
		if got := Valid(tt.mode); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestConstants(t *testing.T) {
	if ModeOpen != "open" {
		t.Errorf("ModeOpen = %q, want %q", ModeOpen, "open")
	}
	if ModeOIDC != "oidc" {
		t.Errorf("ModeOIDC = %q, want %q", ModeOIDC, "oidc")
	}
}
