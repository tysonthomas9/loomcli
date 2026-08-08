package workspace

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

type healthBackend struct {
	name   string
	status backends.HealthStatus
}

func (h *healthBackend) Name() string { return h.name }

func (h *healthBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (h *healthBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return nil
}

func (h *healthBackend) HealthCheck() backends.HealthStatus { return h.status }

func TestCheckBackendUsesRegisteredBackendHealth(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(&healthBackend{
		name: "localdogfood",
		status: backends.HealthStatus{
			Healthy:   true,
			Installed: true,
			Version:   "1",
			Message:   "ready",
		},
	})

	info, err := checkBackend("localdogfood")
	if err != nil {
		t.Fatalf("CheckBackend returned error: %v", err)
	}
	if !info.Installed {
		t.Fatalf("Installed = false, want true")
	}
	if info.DetectedVersion != "1" {
		t.Fatalf("DetectedVersion = %q, want %q", info.DetectedVersion, "1")
	}
	if info.InstallHint != "" {
		t.Fatalf("InstallHint = %q, want empty", info.InstallHint)
	}
}

func TestCheckBackendUsesRegisteredBackendMissingMessage(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(&healthBackend{
		name: "gone",
		status: backends.HealthStatus{
			Healthy:   false,
			Installed: false,
			Message:   "binary no longer found",
		},
	})

	info, err := checkBackend("gone")
	if err != nil {
		t.Fatalf("CheckBackend returned error: %v", err)
	}
	if info.Installed {
		t.Fatalf("Installed = true, want false")
	}
	if info.InstallHint != "binary no longer found" {
		t.Fatalf("InstallHint = %q, want %q", info.InstallHint, "binary no longer found")
	}
}

func TestCheckBackendFallsBackToDiscoveryForUnregisteredName(t *testing.T) {
	cli.TestingResetBackendState(t)

	info, err := checkBackend("definitely-not-on-path-backendcheck")
	if err != nil {
		t.Fatalf("CheckBackend returned error: %v", err)
	}
	if info.Installed {
		t.Fatalf("Installed = true, want false")
	}
	if info.Binary != "definitely-not-on-path-backendcheck" {
		t.Fatalf("Binary = %q, want raw lookup name", info.Binary)
	}
}
