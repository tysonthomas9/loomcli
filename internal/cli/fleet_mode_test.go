//go:build ignore

package cli

import (
	"testing"
	"time"
)

func TestIsFleetMode_NilConfig(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	if isFleetMode(nil) {
		t.Error("isFleetMode(nil) = true, want false")
	}
}

func TestIsFleetMode_EmptyBackend(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &DaemonConfig{Backend: ""}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with empty backend = true, want false")
	}
}

func TestIsFleetMode_BeadsBackend(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &DaemonConfig{Backend: "beads"}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with backend=beads = true, want false")
	}
}

func TestIsFleetMode_FleetBackend(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	cfg := &DaemonConfig{Backend: BackendFleet}
	if !isFleetMode(cfg) {
		t.Error("isFleetMode with backend=fleet = false, want true")
	}
}

func TestIsFleetMode_AIBackendNotFleet(t *testing.T) {
	// AI backends (claude, codex) should NOT trigger fleet mode
	t.Setenv(fleetModeEnvVar, "")
	for _, b := range []string{"claude", "codex", "opencode"} {
		cfg := &DaemonConfig{Backend: b}
		if isFleetMode(cfg) {
			t.Errorf("isFleetMode with backend=%s = true, want false", b)
		}
	}
}

func TestIsFleetMode_EnvVarOverride(t *testing.T) {
	t.Setenv(fleetModeEnvVar, BackendFleet)
	// Env var should override config, even with empty/nil config
	if !isFleetMode(nil) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=fleet and nil config = false, want true")
	}
	cfg := &DaemonConfig{Backend: "beads"}
	if !isFleetMode(cfg) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=fleet and backend=beads = false, want true")
	}
}

func TestIsFleetMode_EnvVarOtherValue(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "beads")
	cfg := &DaemonConfig{Backend: ""}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with LOOM_ISSUE_BACKEND=beads = true, want false")
	}
}

func TestIsFleetMode_FleetDBIsNotFleetMode(t *testing.T) {
	// "fleetdb" (embedded SQLite fleet store) is NOT the same as "fleet" (external fleet server)
	t.Setenv(fleetModeEnvVar, "")
	cfg := &DaemonConfig{Backend: "fleetdb"}
	if isFleetMode(cfg) {
		t.Error("isFleetMode with backend=fleetdb = true, want false (fleetdb != fleet)")
	}
}

func TestIsFleetModeFromEnv_NoFleetMode(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")
	// Without env var and without a project config, should return false
	origDir := t.TempDir()
	t.Chdir(origDir)
	if isFleetModeFromEnv() {
		t.Error("isFleetModeFromEnv() = true, want false (no env var, no config)")
	}
}

func TestIsFleetModeFromEnv_EnvVar(t *testing.T) {
	t.Setenv(fleetModeEnvVar, BackendFleet)
	if !isFleetModeFromEnv() {
		t.Error("isFleetModeFromEnv() = false, want true (LOOM_ISSUE_BACKEND=fleet)")
	}
}

func TestDaemonStart_FleetMode_SkipsAgentSupervision(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")

	// Create a daemon with fleet mode config and one agent
	d := &Daemon{
		config: &DaemonConfig{
			Backend: BackendFleet,
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(0),
					BackoffInitial: intPtr(1),
					BackoffMax:     intPtr(1),
				},
			},
		},
		agents: []*AgentProcess{
			{
				entry: AgentEntry{Worktree: "test-agent", Role: "task"},
			},
		},
		concurrency: NewConcurrencyTracker(nil),
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// In fleet mode, agent stopCh and done should NOT be initialized
	// because superviseAgent goroutines were not launched
	if d.agents[0].stopCh != nil {
		t.Error("agent stopCh should be nil in fleet mode (supervision skipped)")
	}
	if d.agents[0].done != nil {
		t.Error("agent done should be nil in fleet mode (supervision skipped)")
	}

	// Stop should complete quickly since no goroutines were launched
	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not complete within 5 seconds")
	}
}

func TestDaemonStart_NonFleetMode_LaunchesAgents(t *testing.T) {
	t.Setenv(fleetModeEnvVar, "")

	// With empty backend (default beads mode), agents should be supervised
	d := &Daemon{
		config: &DaemonConfig{
			Backend: "",
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(0),
					BackoffInitial: intPtr(1),
					BackoffMax:     intPtr(1),
				},
			},
		},
		agents: []*AgentProcess{
			{
				entry: AgentEntry{Worktree: "test-agent", Role: "task"},
			},
		},
		concurrency: NewConcurrencyTracker(nil),
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// In non-fleet mode, agent channels should be initialized
	if d.agents[0].stopCh == nil {
		t.Error("agent stopCh should be non-nil in non-fleet mode")
	}
	if d.agents[0].done == nil {
		t.Error("agent done should be non-nil in non-fleet mode")
	}

	// Clean up
	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() did not complete within 10 seconds")
	}
}
