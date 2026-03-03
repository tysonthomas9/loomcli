package cli

import (
	"strings"
	"testing"
)

// TestBuildCommand_BackendResolutionChain verifies the per-agent > per-role > project > global precedence.
func TestBuildCommand_BackendResolutionChain(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("per-agent backend wins over per-role and project", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "global-backend"},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan", Backend: "agent-backend"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", Backend: "role-backend"},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "agent-backend" {
			t.Errorf("backend = %q, want %q (per-agent should win)", foundBackend, "agent-backend")
		}
	})

	t.Run("per-role backend wins when per-agent is empty", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "global-backend"},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"}, // no Backend
			roleConfig:   RoleConfig{Description: "Built-in plan agent", Backend: "role-backend"},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "role-backend" {
			t.Errorf("backend = %q, want %q (per-role should win)", foundBackend, "role-backend")
		}
	})

	t.Run("project backend used when per-agent and per-role are empty", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "project-backend"},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"}, // no Backend
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "project-backend" {
			t.Errorf("backend = %q, want %q (project should be used)", foundBackend, "project-backend")
		}
	})

	t.Run("no backend flag when all levels are empty", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}}, // no Backend
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		for _, arg := range cmd.Args {
			if arg == "--backend" {
				t.Error("--backend should not be present when all levels are empty")
			}
		}
	})
}

// TestBuildCommand_ToolConstraintEnvVars verifies LOOM_ALLOWED_TOOLS, LOOM_DENIED_TOOLS,
// and LOOM_READ_ONLY are set in cmd.Env when role has constraints.
func TestBuildCommand_ToolConstraintEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("allowed tools env var set", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", AllowedTools: []string{"read", "grep", "glob"}},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_ALLOWED_TOOLS=read,grep,glob" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_ALLOWED_TOOLS=read,grep,glob not found in cmd.Env")
		}
	})

	t.Run("denied tools env var set", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", DeniedTools: []string{"bash", "write", "edit"}},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_DENIED_TOOLS=bash,write,edit" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_DENIED_TOOLS=bash,write,edit not found in cmd.Env")
		}
	})

	t.Run("read-only env var set", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", ReadOnly: true},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_READ_ONLY=1" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_READ_ONLY=1 not found in cmd.Env")
		}
	})

	t.Run("all constraint env vars set together", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry: AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig: RoleConfig{
				Description:  "Built-in plan agent",
				AllowedTools: []string{"read"},
				DeniedTools:  []string{"write"},
				ReadOnly:     true,
			},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundAllowed, foundDenied, foundReadOnly := false, false, false
		for _, env := range cmd.Env {
			switch {
			case env == "LOOM_ALLOWED_TOOLS=read":
				foundAllowed = true
			case env == "LOOM_DENIED_TOOLS=write":
				foundDenied = true
			case env == "LOOM_READ_ONLY=1":
				foundReadOnly = true
			}
		}
		if !foundAllowed {
			t.Error("LOOM_ALLOWED_TOOLS not found")
		}
		if !foundDenied {
			t.Error("LOOM_DENIED_TOOLS not found")
		}
		if !foundReadOnly {
			t.Error("LOOM_READ_ONLY not found")
		}
	})
}

// TestBuildCommand_NoConstraints_BackwardCompat verifies no constraint env vars
// are set when role has no tool constraints.
func TestBuildCommand_NoConstraints_BackwardCompat(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"}, // no constraints
		worktreePath: tmpDir,
	}

	cmd := d.buildCommand(ap)
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ALLOWED_TOOLS=") {
			t.Error("LOOM_ALLOWED_TOOLS should not be set when AllowedTools is empty")
		}
		if strings.HasPrefix(env, "LOOM_DENIED_TOOLS=") {
			t.Error("LOOM_DENIED_TOOLS should not be set when DeniedTools is empty")
		}
		if strings.HasPrefix(env, "LOOM_READ_ONLY=") {
			t.Error("LOOM_READ_ONLY should not be set when ReadOnly is false")
		}
	}
}

// TestReadOnlyPreamble verifies the function returns preamble when env is set
// and empty string when not set.
func TestReadOnlyPreamble(t *testing.T) {
	t.Run("returns preamble when LOOM_READ_ONLY=1", func(t *testing.T) {
		t.Setenv("LOOM_READ_ONLY", "1")
		result := ReadOnlyPreamble()
		if result == "" {
			t.Error("ReadOnlyPreamble() = empty, want non-empty")
		}
		if !strings.Contains(result, "READ-ONLY") {
			t.Errorf("ReadOnlyPreamble() = %q, want contains 'READ-ONLY'", result)
		}
	})

	t.Run("returns empty when LOOM_READ_ONLY not set", func(t *testing.T) {
		// t.Setenv with empty value is different from unset; use Setenv("", "") won't help.
		// Just don't set LOOM_READ_ONLY — the test framework starts with a clean env override.
		t.Setenv("LOOM_READ_ONLY", "")
		result := ReadOnlyPreamble()
		if result != "" {
			t.Errorf("ReadOnlyPreamble() = %q, want empty", result)
		}
	})

	t.Run("returns empty when LOOM_READ_ONLY=0", func(t *testing.T) {
		t.Setenv("LOOM_READ_ONLY", "0")
		result := ReadOnlyPreamble()
		if result != "" {
			t.Errorf("ReadOnlyPreamble() = %q, want empty (only '1' triggers)", result)
		}
	})
}

// TestBuildRouterTaskCheck_NilWhenNoConstraints verifies nil returned for unconstrained roles.
func TestBuildRouterTaskCheck_NilWhenNoConstraints(t *testing.T) {
	rc := RoleConfig{Description: "basic"} // no Skills, PathPatterns, or MaxPriority
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check != nil {
		t.Error("BuildRouterTaskCheck() should return nil for unconstrained role")
	}
}

// TestBuildRouterTaskCheck_NonNilWithSkills verifies non-nil returned when role has skills.
func TestBuildRouterTaskCheck_NonNilWithSkills(t *testing.T) {
	rc := RoleConfig{
		Description: "security specialist",
		Skills:      []string{"security", "auth"},
	}
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil for role with skills")
	}
}

// TestBuildRouterTaskCheck_NonNilWithPathPatterns verifies non-nil returned when role has path patterns.
func TestBuildRouterTaskCheck_NonNilWithPathPatterns(t *testing.T) {
	rc := RoleConfig{
		Description:  "frontend specialist",
		PathPatterns: []string{"src/components/**"},
	}
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil for role with path patterns")
	}
}

// TestBuildRouterTaskCheck_NonNilWithMaxPriority verifies non-nil returned when role has max priority.
func TestBuildRouterTaskCheck_NonNilWithMaxPriority(t *testing.T) {
	maxP := 2
	rc := RoleConfig{
		Description: "p0-p2 only",
		MaxPriority: &maxP,
	}
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil for role with max priority")
	}
}

// TestBuildRouterTaskCheck_AgentOverridesPathPatterns verifies agent-level path patterns
// override role-level ones when determining if router is needed.
func TestBuildRouterTaskCheck_AgentOverridesPathPatterns(t *testing.T) {
	rc := RoleConfig{Description: "no constraints"} // no PathPatterns
	ae := AgentEntry{
		Worktree:     "falcon",
		Role:         "task",
		PathPatterns: []string{"internal/**"},
	}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil when agent has path patterns")
	}
}

// TestConcurrencyTracker_NilReceiver verifies nil receiver safety for all methods.
func TestConcurrencyTracker_NilReceiver(t *testing.T) {
	var ct *ConcurrencyTracker

	// All should be no-ops or return safe defaults
	if !ct.Acquire("task") {
		t.Error("Acquire on nil should return true")
	}
	if !ct.TryAcquire("task") {
		t.Error("TryAcquire on nil should return true")
	}
	ct.Release("task") // should not panic
	ct.Close()         // should not panic
}

// TestDaemonStop_ClosesConcurrencyTracker verifies that Daemon.Stop() calls
// concurrency.Close() to unblock waiters.
func TestDaemonStop_ClosesConcurrencyTracker(t *testing.T) {
	limit := 1
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(0),
					BackoffInitial: intPtr(1),
					BackoffMax:     intPtr(1),
				},
			},
		},
		agents:       []*AgentProcess{},
		epicAssigner: NewEpicAssigner(),
		concurrency:  ct,
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Fill the slot
	ct.Acquire("task")

	// Try to acquire in background — should block
	acquired := make(chan bool, 1)
	go func() {
		acquired <- ct.Acquire("task")
	}()

	// Stop should close the tracker and unblock the goroutine
	d.Stop()

	result := <-acquired
	if result {
		t.Error("Acquire after Stop should return false (tracker closed)")
	}
}
