package supervisor

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// fakeManagedBackend implements cli.Backend + cli.ManagedRuntimeBackend.
type fakeManagedBackend struct {
	name  string
	ready bool
}

func (f *fakeManagedBackend) Name() string                           { return f.name }
func (f *fakeManagedBackend) InvokeInteractive(_, _, _ string) error { return nil }
func (f *fakeManagedBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return nil
}
func (f *fakeManagedBackend) ManagedRuntimeReady() (bool, string) {
	if f.ready {
		return true, ""
	}
	return false, "node missing"
}

// TestGateBackendAvailable_ManagedRuntime verifies the daemon gates a
// managed-runtime backend (no CLI on PATH, e.g. flue) on its own readiness
// rather than a PATH lookup — a ready runtime spawns, a not-ready one parks.
func TestGateBackendAvailable_ManagedRuntime(t *testing.T) {
	cli.RegisterBackend(&fakeManagedBackend{name: "fakemanaged-ready", ready: true})
	cli.RegisterBackend(&fakeManagedBackend{name: "fakemanaged-down", ready: false})

	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} }}

	// Ready managed runtime → spawn allowed (no PATH binary required).
	apReady := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova", Backend: "fakemanaged-ready"}}
	if err := s.gateBackendAvailable(apReady); err != nil {
		t.Errorf("ready managed backend was gated: %v", err)
	}

	// Not-ready managed runtime → parked as backend-unavailable.
	apDown := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova2", Backend: "fakemanaged-down"}}
	if err := s.gateBackendAvailable(apDown); !errors.Is(err, ErrBackendUnavailable) {
		t.Errorf("not-ready managed backend: err = %v, want ErrBackendUnavailable", err)
	}
	if apDown.StopReason != StopReasonBackendUnavailable {
		t.Errorf("StopReason = %v, want %v", apDown.StopReason, StopReasonBackendUnavailable)
	}
}
