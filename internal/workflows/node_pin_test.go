package workflows

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestNodeVersionPinIsNode22AtLeast22_18: the packaged built-in artifacts run
// on the pinned Node release. The Flue CLI at FLUE_COMMIT declares
// ENGINES_LABEL='>=22.18 or >=23.6', so the pin must be a 22.x with x >= 18.
func TestNodeVersionPinIsNode22AtLeast22_18(t *testing.T) {
	parts := strings.Split(PinnedNodeVersion, ".")
	if len(parts) != 3 {
		t.Fatalf("NODE_VERSION = %q, want major.minor.patch", PinnedNodeVersion)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major != 22 {
		t.Fatalf("NODE_VERSION = %q, want major 22", PinnedNodeVersion)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 18 {
		t.Fatalf("NODE_VERSION = %q, want minor >= 18 (Flue CLI engines floor)", PinnedNodeVersion)
	}
	if _, err := strconv.Atoi(parts[2]); err != nil {
		t.Fatalf("NODE_VERSION = %q, want numeric patch", PinnedNodeVersion)
	}
}

func TestFlueCommitPinIsFortyLowercaseHex(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(PinnedFlueCommit) {
		t.Fatalf("FLUE_COMMIT = %q, want 40 lowercase hex characters", PinnedFlueCommit)
	}
}
