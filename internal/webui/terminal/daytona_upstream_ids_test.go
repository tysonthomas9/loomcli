package terminal

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// The broker creates the lead PTY under its own constant and stores the id
// nowhere; the terminal attaches by id alone. If these two literals drift,
// every attach fails as "session not found" and nothing else catches it.
func TestLeadPTYSessionIDMatchesPlacement(t *testing.T) {
	if DefaultDaytonaLeadPTYSessionID != placement.LeadPTYSessionID {
		t.Fatalf("terminal attaches to %q but placement creates %q",
			DefaultDaytonaLeadPTYSessionID, placement.LeadPTYSessionID)
	}
}
