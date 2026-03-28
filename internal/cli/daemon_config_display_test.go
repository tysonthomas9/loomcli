package cli

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestApplyRestartPolicyDefaults_AllNil(t *testing.T) {
	rp := RestartPolicy{}
	applyRestartPolicyDefaults(&rp)

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"MaxRetries", *rp.MaxRetries, 3},
		{"BackoffInitial", *rp.BackoffInitial, 2},
		{"BackoffMax", *rp.BackoffMax, 300},
		{"OutputTimeout", *rp.OutputTimeout, 900},
		{"RateLimitBackoff", *rp.RateLimitBackoff, 30},
		{"RateLimitMaxWait", *rp.RateLimitMaxWait, 300},
		{"RateLimitNoCount", *rp.RateLimitNoCount, true},
		{"TimeoutBackoff", *rp.TimeoutBackoff, 5},
		{"NoWorkBackoff", *rp.NoWorkBackoff, 30},
		{"IdlePollInterval", *rp.IdlePollInterval, 30},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestApplyRestartPolicyDefaults_PartiallySet(t *testing.T) {
	rp := RestartPolicy{
		MaxRetries:       intPtr(10),
		RateLimitBackoff: intPtr(60),
	}
	applyRestartPolicyDefaults(&rp)

	// Pre-set values should not be overwritten
	if *rp.MaxRetries != 10 {
		t.Errorf("MaxRetries = %d, want 10 (should not be overwritten)", *rp.MaxRetries)
	}
	if *rp.RateLimitBackoff != 60 {
		t.Errorf("RateLimitBackoff = %d, want 60 (should not be overwritten)", *rp.RateLimitBackoff)
	}

	// Nil fields should get defaults
	if *rp.BackoffInitial != 2 {
		t.Errorf("BackoffInitial = %d, want 2", *rp.BackoffInitial)
	}
	if *rp.NoWorkBackoff != 30 {
		t.Errorf("NoWorkBackoff = %d, want 30", *rp.NoWorkBackoff)
	}
}

func TestApplyRestartPolicyDefaults_AlreadySet(t *testing.T) {
	rp := RestartPolicy{
		MaxRetries:       intPtr(5),
		BackoffInitial:   intPtr(10),
		BackoffMax:       intPtr(600),
		OutputTimeout:    intPtr(1800),
		RateLimitBackoff: intPtr(60),
		RateLimitMaxWait: intPtr(600),
		RateLimitNoCount: boolPtr(false),
		TimeoutBackoff:   intPtr(10),
		NoWorkBackoff:    intPtr(60),
		IdlePollInterval: intPtr(60),
	}
	applyRestartPolicyDefaults(&rp)

	if *rp.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", *rp.MaxRetries)
	}
	if *rp.RateLimitNoCount != false {
		t.Errorf("RateLimitNoCount = %v, want false", *rp.RateLimitNoCount)
	}
	if *rp.IdlePollInterval != 60 {
		t.Errorf("IdlePollInterval = %d, want 60", *rp.IdlePollInterval)
	}
}

func TestMaskDaemonSecrets_Set(t *testing.T) {
	ds := &DaemonSettings{
		RedisURL: "redis://secret:password@host:6379",
		FleetDB: &FleetDBSettings{
			RedisURL: "redis://fleet:secret@host:6379",
		},
	}
	maskDaemonSecrets(ds)

	if ds.RedisURL != "***" {
		t.Errorf("RedisURL = %q, want ***", ds.RedisURL)
	}
	if ds.FleetDB.RedisURL != "***" {
		t.Errorf("FleetDB.RedisURL = %q, want ***", ds.FleetDB.RedisURL)
	}
}

func TestMaskDaemonSecrets_Empty(t *testing.T) {
	ds := &DaemonSettings{}
	maskDaemonSecrets(ds)

	if ds.RedisURL != "" {
		t.Errorf("RedisURL = %q, want empty", ds.RedisURL)
	}
}

func TestMaskDaemonSecrets_FleetDBNil(t *testing.T) {
	ds := &DaemonSettings{
		RedisURL: "redis://host:6379",
	}
	maskDaemonSecrets(ds)

	if ds.RedisURL != "***" {
		t.Errorf("RedisURL = %q, want ***", ds.RedisURL)
	}
	// Should not panic with nil FleetDB
}

func TestResolvedConfigForDisplay_Integration(t *testing.T) {
	cfg := &DaemonConfig{
		Backend: "claude",
		Daemon: DaemonSettings{
			PIDFile:  ".loom/daemon.pid",
			LogDir:   ".loom/logs",
			RedisURL: "redis://secret@host:6379",
			RestartPolicy: RestartPolicy{
				MaxRetries:     intPtr(3),
				BackoffInitial: intPtr(2),
				// Leave other fields nil to test defaults
			},
			FleetDB: &FleetDBSettings{
				RedisURL: "redis://fleet-secret@host:6379",
			},
		},
		Roles: map[string]RoleConfig{
			"task": {Description: "task runner"},
		},
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "task"},
		},
	}

	display := resolvedConfigForDisplay(cfg)

	// Secrets masked
	if display.Daemon.RedisURL != "***" {
		t.Errorf("RedisURL = %q, want ***", display.Daemon.RedisURL)
	}
	if display.Daemon.FleetDB.RedisURL != "***" {
		t.Errorf("FleetDB.RedisURL = %q, want ***", display.Daemon.FleetDB.RedisURL)
	}

	// Defaults filled in
	if display.Daemon.RestartPolicy.RateLimitBackoff == nil || *display.Daemon.RestartPolicy.RateLimitBackoff != 30 {
		t.Error("RateLimitBackoff should be 30 (default)")
	}
	if display.Daemon.RestartPolicy.NoWorkBackoff == nil || *display.Daemon.RestartPolicy.NoWorkBackoff != 30 {
		t.Error("NoWorkBackoff should be 30 (default)")
	}

	// Pre-set values preserved
	if *display.Daemon.RestartPolicy.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", *display.Daemon.RestartPolicy.MaxRetries)
	}

	// Output should be valid YAML
	data, err := yaml.Marshal(display)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}
	var roundtrip DaemonConfig
	if err := yaml.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("yaml.Unmarshal round-trip failed: %v", err)
	}
}

func TestResolvedConfigForDisplay_DoesNotMutateOriginal(t *testing.T) {
	cfg := &DaemonConfig{
		Daemon: DaemonSettings{
			RedisURL: "redis://secret@host:6379",
			RestartPolicy: RestartPolicy{
				MaxRetries: intPtr(5),
				// Leave others nil
			},
			FleetDB: &FleetDBSettings{
				RedisURL: "redis://fleet@host:6379",
			},
		},
		Roles: map[string]RoleConfig{
			"task": {Description: "original"},
		},
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "task"},
		},
	}

	_ = resolvedConfigForDisplay(cfg)

	// Original should be unchanged
	if cfg.Daemon.RedisURL != "redis://secret@host:6379" {
		t.Errorf("Original RedisURL mutated to %q", cfg.Daemon.RedisURL)
	}
	if cfg.Daemon.FleetDB.RedisURL != "redis://fleet@host:6379" {
		t.Errorf("Original FleetDB.RedisURL mutated to %q", cfg.Daemon.FleetDB.RedisURL)
	}
	if cfg.Daemon.RestartPolicy.RateLimitBackoff != nil {
		t.Error("Original RateLimitBackoff should still be nil")
	}
	if cfg.Daemon.RestartPolicy.NoWorkBackoff != nil {
		t.Error("Original NoWorkBackoff should still be nil")
	}
}

func TestDaemonConfigCmd_Registered(t *testing.T) {
	found := false
	for _, cmd := range daemonCmd.Commands() {
		if cmd.Use == "config" {
			found = true
			break
		}
	}
	if !found {
		t.Error("daemonCmd should have a 'config' subcommand")
	}
}
