package config

import "testing"

// TestOverlayDaemonSettings_FlueSandbox locks in that the daemon profile's
// flue_sandbox setting survives the overlay merge (the path that feeds the
// daemon's config snapshot → LOOM_FLUE_SANDBOX injection).
func TestOverlayDaemonSettings_FlueSandbox(t *testing.T) {
	dst := &DaemonSettings{}
	OverlayDaemonSettings(dst, &DaemonSettings{FlueSandbox: "daytona"})
	if dst.FlueSandbox != "daytona" {
		t.Fatalf("FlueSandbox after overlay = %q, want daytona", dst.FlueSandbox)
	}

	// An empty src must not clobber an existing value.
	OverlayDaemonSettings(dst, &DaemonSettings{})
	if dst.FlueSandbox != "daytona" {
		t.Fatalf("empty overlay clobbered FlueSandbox: %q", dst.FlueSandbox)
	}
}
