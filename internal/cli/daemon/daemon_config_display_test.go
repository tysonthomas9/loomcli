package daemon

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

	if *rp.MaxRetries != 10 {
		t.Errorf("MaxRetries = %d, want 10", *rp.MaxRetries)
	}
	if *rp.RateLimitBackoff != 60 {
		t.Errorf("RateLimitBackoff = %d, want 60", *rp.RateLimitBackoff)
	}
	if *rp.BackoffInitial != 2 {
		t.Errorf("BackoffInitial = %d, want 2", *rp.BackoffInitial)
	}
	if *rp.NoWorkBackoff != 30 {
		t.Errorf("NoWorkBackoff = %d, want 30", *rp.NoWorkBackoff)
	}
}

func TestMaskDaemonSecrets_RedisURL(t *testing.T) {
	ds := &DaemonSettings{RedisURL: "redis://secret:password@host:6379"}

	maskDaemonSecrets(ds)

	if ds.RedisURL != "***" {
		t.Errorf("RedisURL = %q, want ***", ds.RedisURL)
	}
}

func TestResolvedConfigForDisplay_Integration(t *testing.T) {
	cfg := &DaemonConfig{
		Backend: "codex",
		Daemon: DaemonSettings{
			PIDFile:  ".loom/daemon.pid",
			LogDir:   ".loom/logs",
			RedisURL: "redis://secret@host:6379",
			RestartPolicy: RestartPolicy{
				MaxRetries:     intPtr(3),
				BackoffInitial: intPtr(2),
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

	if display.Daemon.RedisURL != "***" {
		t.Errorf("RedisURL = %q, want ***", display.Daemon.RedisURL)
	}
	if display.Daemon.RestartPolicy.RateLimitBackoff == nil || *display.Daemon.RestartPolicy.RateLimitBackoff != 30 {
		t.Error("RateLimitBackoff should be 30")
	}
	if display.Daemon.RestartPolicy.NoWorkBackoff == nil || *display.Daemon.RestartPolicy.NoWorkBackoff != 30 {
		t.Error("NoWorkBackoff should be 30")
	}
	if *display.Daemon.RestartPolicy.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", *display.Daemon.RestartPolicy.MaxRetries)
	}

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

	if cfg.Daemon.RedisURL != "redis://secret@host:6379" {
		t.Errorf("Original RedisURL mutated to %q", cfg.Daemon.RedisURL)
	}
	if cfg.Daemon.RestartPolicy.RateLimitBackoff != nil {
		t.Error("Original RateLimitBackoff should still be nil")
	}
	if cfg.Daemon.RestartPolicy.NoWorkBackoff != nil {
		t.Error("Original NoWorkBackoff should still be nil")
	}
}

// TestAccountWallCooldown_RoundTrip covers the fleet-wide account-wall knob
// end to end: it parses from YAML, defaults to 900 seconds when unset, and
// survives the deep copy the config display makes (an aliased pointer here
// would let a display call mutate the live daemon config).
func TestAccountWallCooldown_RoundTrip(t *testing.T) {
	var rp RestartPolicy
	if err := yaml.Unmarshal([]byte("account_wall_cooldown: 120\n"), &rp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rp.AccountWallCooldown == nil || *rp.AccountWallCooldown != 120 {
		t.Fatalf("AccountWallCooldown = %v, want 120", rp.AccountWallCooldown)
	}

	out, err := yaml.Marshal(&rp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back RestartPolicy
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.AccountWallCooldown == nil || *back.AccountWallCooldown != 120 {
		t.Errorf("round-tripped AccountWallCooldown = %v, want 120", back.AccountWallCooldown)
	}

	copied := deepCopyRestartPolicy(&rp)
	if copied.AccountWallCooldown == rp.AccountWallCooldown {
		t.Error("deepCopyRestartPolicy must clone AccountWallCooldown, not alias it")
	}
	if *copied.AccountWallCooldown != 120 {
		t.Errorf("copied AccountWallCooldown = %d, want 120", *copied.AccountWallCooldown)
	}

	var unset RestartPolicy
	applyRestartPolicyDefaults(&unset)
	if unset.AccountWallCooldown == nil || *unset.AccountWallCooldown != 900 {
		t.Errorf("default AccountWallCooldown = %v, want 900", unset.AccountWallCooldown)
	}
}
