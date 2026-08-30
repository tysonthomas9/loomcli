package backends

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCursorBackendHealthCheckAcceptsOAuthStatus(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "")
	prev := cursorAuthStatus
	cursorAuthStatus = func(context.Context) error { return nil }
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
	cursorAuthStatus = func(context.Context) error { return errors.New("not logged in") }
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

func TestCursorBackendAdmissionHealthCheckHonorsContext(t *testing.T) {
	binary := writeProbeScript(t, "cursor-agent", "exit 0")
	t.Setenv("PATH", filepath.Dir(binary))
	t.Setenv("CURSOR_API_KEY", "")

	started := make(chan struct{})
	prev := cursorAuthStatus
	cursorAuthStatus = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { cursorAuthStatus = prev })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	hs := (&CursorBackend{}).HealthCheckForAdmission(ctx)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v, want context.Canceled", ctx.Err())
	}
	if !hs.Installed || hs.Healthy || hs.APIKeySet || hs.Version != "" {
		t.Fatalf("admission health = %+v, want canceled auth readiness without version", hs)
	}
}
