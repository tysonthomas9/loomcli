package lead

import "testing"

func TestLeadResumeEligibility(t *testing.T) {
	tests := []struct {
		name         string
		sandboxLead  bool
		inheritedSID bool
		want         bool
	}{
		{name: "server-adopted sandbox occupant", sandboxLead: true, want: true},
		{name: "host lead", want: false},
		{name: "sandbox occupant with inherited session", sandboxLead: true, inheritedSID: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leadResumeEligible(tt.sandboxLead, tt.inheritedSID); got != tt.want {
				t.Fatalf("leadResumeEligible(%v, %v) = %v, want %v", tt.sandboxLead, tt.inheritedSID, got, tt.want)
			}
		})
	}
}
