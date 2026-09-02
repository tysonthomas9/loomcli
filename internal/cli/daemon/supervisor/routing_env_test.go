package supervisor

import (
	"context"
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// TestBuildCommand_RoutingEnvVars verifies LOOM_ROLE_SKILLS, LOOM_ROLE_LABELS,
// LOOM_ROLE_EXCLUDE_LABELS, LOOM_ROLE_PATH_PATTERNS, LOOM_ROLE_MAX_PRIORITY,
// LOOM_ROLE_TASK_FILTER, and LOOM_ROLE are set in cmd.Env.
func TestBuildCommand_RoutingEnvVars(t *testing.T) {
	tmpDir := t.TempDir()
	maxP := 2

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan", PathPatterns: []string{"cmd/**"}},
		RoleConfig: cfgpkg.RoleConfig{
			Description:   "Built-in plan agent",
			Skills:        []string{"go", "daemon"},
			Labels:        []string{"plan-ready", "approved"},
			ExcludeLabels: []string{"plan-reviewed"},
			PathPatterns:  []string{"internal/**"},
			MaxPriority:   &maxP,
			TaskFilter:    "needs_plan",
		},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(context.Background(), ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	envMap := make(map[string]string)
	for _, env := range cmd.Env {
		if idx := strings.IndexByte(env, '='); idx >= 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	if v, ok := envMap["LOOM_ROLE_SKILLS"]; !ok || v != "go,daemon" {
		t.Errorf("LOOM_ROLE_SKILLS = %q, want %q", v, "go,daemon")
	}
	if v, ok := envMap["LOOM_ROLE_LABELS"]; !ok || v != "plan-ready,approved" {
		t.Errorf("LOOM_ROLE_LABELS = %q, want %q", v, "plan-ready,approved")
	}
	if v, ok := envMap["LOOM_ROLE_EXCLUDE_LABELS"]; !ok || v != "plan-reviewed" {
		t.Errorf("LOOM_ROLE_EXCLUDE_LABELS = %q, want %q", v, "plan-reviewed")
	}
	if v, ok := envMap["LOOM_ROLE_PATH_PATTERNS"]; !ok || v != "internal/**" {
		t.Errorf("LOOM_ROLE_PATH_PATTERNS = %q, want %q", v, "internal/**")
	}
	if v, ok := envMap["LOOM_ROLE_MAX_PRIORITY"]; !ok || v != "2" {
		t.Errorf("LOOM_ROLE_MAX_PRIORITY = %q, want %q", v, "2")
	}
	if v, ok := envMap["LOOM_ROLE_TASK_FILTER"]; !ok || v != "needs_plan" {
		t.Errorf("LOOM_ROLE_TASK_FILTER = %q, want %q", v, "needs_plan")
	}
	if v, ok := envMap["LOOM_ROLE"]; !ok || v != "plan" {
		t.Errorf("LOOM_ROLE = %q, want %q", v, "plan")
	}
	if v, ok := envMap["LOOM_AGENT_PATH_PATTERNS"]; !ok || v != "cmd/**" {
		t.Errorf("LOOM_AGENT_PATH_PATTERNS = %q, want %q", v, "cmd/**")
	}
}

// TestBuildCommand_NoRoutingEnvVars verifies no routing env vars are set when
// role has no routing config (existing behavior).
func TestBuildCommand_NoRoutingEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"}, // no routing config
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(context.Background(), ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ROLE_SKILLS=") {
			t.Error("LOOM_ROLE_SKILLS should not be set when Skills is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_LABELS=") {
			t.Error("LOOM_ROLE_LABELS should not be set when Labels is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_EXCLUDE_LABELS=") {
			t.Error("LOOM_ROLE_EXCLUDE_LABELS should not be set when ExcludeLabels is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_PATH_PATTERNS=") {
			t.Error("LOOM_ROLE_PATH_PATTERNS should not be set when PathPatterns is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_MAX_PRIORITY=") {
			t.Error("LOOM_ROLE_MAX_PRIORITY should not be set when MaxPriority is nil")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_TASK_FILTER=") {
			t.Error("LOOM_ROLE_TASK_FILTER should not be set when TaskFilter is empty")
		}
		if strings.HasPrefix(env, "LOOM_AGENT_PATH_PATTERNS=") {
			t.Error("LOOM_AGENT_PATH_PATTERNS should not be set when AgentEntry has no PathPatterns")
		}
		if strings.HasPrefix(env, "LOOM_SOURCE_REPOS=") {
			t.Error("LOOM_SOURCE_REPOS should not be set when agent has no repo affinity")
		}
	}
	// LOOM_ROLE should always be set
	foundRole := false
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ROLE=") {
			foundRole = true
		}
	}
	if !foundRole {
		t.Error("LOOM_ROLE should always be set")
	}
}
