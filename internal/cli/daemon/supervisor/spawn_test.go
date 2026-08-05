package supervisor

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestAppendRoleEnv_MaxBudgetUSD(t *testing.T) {
	t.Parallel()

	t.Run("set when non-nil", func(t *testing.T) {
		t.Parallel()
		budget := 8.50
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: &budget,
			},
		}

		env := appendRoleEnv(nil, ap)

		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				found = true
				want := fmt.Sprintf("LOOM_MAX_BUDGET_USD=%.2f", budget)
				if entry != want {
					t.Errorf("env entry = %q, want %q", entry, want)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected LOOM_MAX_BUDGET_USD in env, got %v", env)
		}
	})

	t.Run("absent when nil", func(t *testing.T) {
		t.Parallel()
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: nil,
			},
		}

		env := appendRoleEnv(nil, ap)

		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				t.Errorf("expected LOOM_MAX_BUDGET_USD to be absent, but found %q", entry)
			}
		}
	})

	t.Run("zero value is formatted", func(t *testing.T) {
		t.Parallel()
		budget := 0.0
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: &budget,
			},
		}

		env := appendRoleEnv(nil, ap)

		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				found = true
				if entry != "LOOM_MAX_BUDGET_USD=0.00" {
					t.Errorf("env entry = %q, want %q", entry, "LOOM_MAX_BUDGET_USD=0.00")
				}
				break
			}
		}
		if !found {
			t.Errorf("expected LOOM_MAX_BUDGET_USD=0.00 in env, got %v", env)
		}
	})
}

func TestAppendRoleEnv_Effort(t *testing.T) {
	t.Parallel()

	ap := &AgentProcess{
		RoleConfig: cfgpkg.RoleConfig{
			Effort: "max",
		},
	}

	env := appendRoleEnv(nil, ap)

	want := map[string]bool{
		"LOOM_AGENT_EFFORT=max":  false,
		"LOOM_CLAUDE_EFFORT=max": false,
	}
	for _, entry := range env {
		if _, ok := want[entry]; ok {
			want[entry] = true
		}
	}
	for entry, found := range want {
		if !found {
			t.Fatalf("expected %s in env, got %v", entry, env)
		}
	}
}

func TestAppendRoleEnv_ReadOnlyRoleConfigIsAuthoritative(t *testing.T) {
	t.Parallel()

	t.Run("writable role removes inherited policy", func(t *testing.T) {
		t.Parallel()
		ap := &AgentProcess{RoleConfig: cfgpkg.RoleConfig{ReadOnly: false}}

		env := appendRoleEnv([]string{"PATH=/bin", "LOOM_READ_ONLY=1"}, ap)

		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_READ_ONLY=") {
				t.Fatalf("writable role inherited stale policy %q", entry)
			}
		}
	})

	t.Run("read-only role replaces inherited value", func(t *testing.T) {
		t.Parallel()
		ap := &AgentProcess{RoleConfig: cfgpkg.RoleConfig{ReadOnly: true}}

		env := appendRoleEnv([]string{"LOOM_READ_ONLY=0"}, ap)

		if len(env) != 1 || env[0] != "LOOM_READ_ONLY=1" {
			t.Fatalf("read-only role env = %v, want [LOOM_READ_ONLY=1]", env)
		}
	})
}

func TestAppendSessionEnvConcurrentIPCAuthAccess(t *testing.T) {
	ap := &AgentProcess{}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			ap.Mu.Lock()
			ap.AgentIPCAuthToken = fmt.Sprintf("token-%d", i)
			ap.Mu.Unlock()
			runtime.Gosched()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			_ = appendSessionEnv(nil, ap)
			runtime.Gosched()
		}
	}()

	wg.Wait()
}
