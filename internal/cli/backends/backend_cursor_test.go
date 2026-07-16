package backends

import (
	"errors"
	"os"
	"testing"
)

func TestCursorBackendHealthCheckAcceptsOAuthStatus(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "")
	prev := cursorAuthStatus
	cursorAuthStatus = func() error { return nil }
	t.Cleanup(func() { cursorAuthStatus = prev })

	hs := (&CursorBackend{}).HealthCheck()
	if !hs.Installed {
		t.Skip("cursor-agent not installed on this host")
	}
	if !hs.Healthy || !hs.APIKeySet {
		t.Fatalf("HealthCheck() = %+v, want OAuth status to satisfy auth", hs)
	}
}

func TestCursorBackendHealthCheckReportsMissingAuth(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "")
	prev := cursorAuthStatus
	cursorAuthStatus = func() error { return errors.New("not logged in") }
	t.Cleanup(func() { cursorAuthStatus = prev })

	hs := (&CursorBackend{}).HealthCheck()
	if !hs.Installed {
		t.Skip("cursor-agent not installed on this host")
	}
	if hs.Healthy || hs.APIKeySet {
		t.Fatalf("HealthCheck() = %+v, want missing auth", hs)
	}
	if got := hs.Message; got == "" || got == os.Getenv("CURSOR_API_KEY") {
		t.Fatalf("HealthCheck().Message = %q, want actionable missing-auth message", got)
	}
}
