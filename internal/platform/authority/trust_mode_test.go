package authority

import "testing"

func TestValidTrustMode(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{TrustModeOpen, true},
		{TrustModeOIDC, true},
		{"none", false},
		{"external", false},
		{"", false},
		{"banana", false},
	}
	for _, test := range tests {
		if got := ValidTrustMode(test.mode); got != test.want {
			t.Errorf("ValidTrustMode(%q) = %v, want %v", test.mode, got, test.want)
		}
	}
}

func TestTrustModeWireValues(t *testing.T) {
	if TrustModeOpen != "open" {
		t.Errorf("TrustModeOpen = %q, want open", TrustModeOpen)
	}
	if TrustModeOIDC != "oidc" {
		t.Errorf("TrustModeOIDC = %q, want oidc", TrustModeOIDC)
	}
}
