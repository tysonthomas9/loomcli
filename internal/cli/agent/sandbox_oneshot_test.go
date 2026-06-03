package agent

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sandbox"
)

func TestSandboxCloneURL(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_REPO_URL", "git://host.containers.internal:9418/repo")
		if got := sandboxCloneURL("git://127.0.0.1:9418/repo"); got != "git://host.containers.internal:9418/repo" {
			t.Errorf("got %q, want the override", got)
		}
	})
	t.Run("localhost rewritten to host gateway", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_REPO_URL", "")
		if got := sandboxCloneURL("http://127.0.0.1:9419/r.git"); got != "http://host.docker.internal:9419/r.git" {
			t.Errorf("got %q, want host-gateway rewrite", got)
		}
	})
}

func TestBuildOneshotCommand_FullScript(t *testing.T) {
	cfg := SandboxOneshotConfig{
		AgentType: "task", AgentName: "falcon", ParentID: "epic-1",
		FleetDBURL: "http://host.docker.internal:8080", FleetDBKey: "sk-dev",
		FleetDBActor: "sandbox:ws-123:falcon:1", WorkspaceID: "ws-123",
	}
	script := buildOneshotCommand("feature/x", cfg, "https://github.com/o/r.git", sandbox.Config{Backend: "claude"})

	wants := []string{
		"set -e\n",
		"chmod +x /sandbox/loom\n",
		"export GIT_SSL_NO_VERIFY=1\n",
		"git clone --branch 'feature/x' --single-branch 'https://github.com/o/r.git' /sandbox/repo\n",
		"cd /sandbox/repo\n",
		// FleetDB connectivity: the agent talks ONLY to fleet-db with a scoped key.
		"export LOOM_FLEET_DB_URL='http://host.docker.internal:8080'\n",
		"export LOOM_FLEET_DB_API_KEY='sk-dev'\n",
		"export LOOM_FLEET_DB_ACTOR='sandbox:ws-123:falcon:1'\n",
		"export LOOM_WORKSPACE='ws-123'\n",
		"/sandbox/loom 'task' 'worktrees/falcon' --backend 'claude' --parent 'epic-1'\n",
		"git diff --cached --quiet || git commit -m 'sandbox agent work [feature/x]'\n",
		"git push origin 'feature/x'\n",
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("script missing %q\n--- script ---\n%s", w, script)
		}
	}
	// The agent reaches fleet-db directly; LOOM_SERVER_URL (loom-serve) must NOT
	// be exported, or the issue backend would route tasks through serve instead.
	if strings.Contains(script, "LOOM_SERVER_URL") {
		t.Errorf("bootstrap must not export LOOM_SERVER_URL (agent uses fleet-db directly):\n%s", script)
	}
	// v5 task state is FleetDB-backed; the beads `bd sync` step must not return.
	if strings.Contains(script, "bd ") || strings.Contains(script, "beads") {
		t.Errorf("bootstrap script still references beads:\n%s", script)
	}
}

func TestBuildOneshotCommand_NoBackendNoParent(t *testing.T) {
	cfg := SandboxOneshotConfig{AgentType: "plan", AgentName: "nova"}
	script := buildOneshotCommand("main", cfg, "git@x:o/r.git", sandbox.Config{})

	if !strings.Contains(script, "/sandbox/loom 'plan' 'worktrees/nova'\n") {
		t.Errorf("expected bare loom invocation without flags\n%s", script)
	}
	if strings.Contains(script, "--backend") {
		t.Error("expected no --backend when backend override empty")
	}
	if strings.Contains(script, "--parent") {
		t.Error("expected no --parent when ParentID empty")
	}
}

func TestResolveSandboxFleetDBURL(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "https://fleet.internal:9000")
		t.Setenv("LOOM_FLEET_DB_URL", "http://localhost:8080")
		if got := resolveSandboxFleetDBURL(); got != "https://fleet.internal:9000" {
			t.Errorf("got %q, want the explicit override", got)
		}
	})
	t.Run("localhost rewritten to host gateway", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "")
		t.Setenv("LOOM_FLEET_DB_URL", "http://localhost:8080")
		if got := resolveSandboxFleetDBURL(); got != "http://host.docker.internal:8080" {
			t.Errorf("got %q, want host-gateway rewrite", got)
		}
	})
	t.Run("127.0.0.1 rewritten", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "")
		t.Setenv("LOOM_FLEET_DB_URL", "http://127.0.0.1:8080")
		if got := resolveSandboxFleetDBURL(); got != "http://host.docker.internal:8080" {
			t.Errorf("got %q, want host-gateway rewrite", got)
		}
	})
	t.Run("none configured returns empty", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "")
		t.Setenv("LOOM_FLEET_DB_URL", "")
		if got := resolveSandboxFleetDBURL(); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// Without an admin fleet-db key on the host, credential provisioning is a no-op
// (dev / auth-off path): no scoped key is minted and the run is left to use the
// host's ambient credential. The returned revoke func must be safe to call.
func TestProvisionSandboxCredential_NoopWithoutAdminKey(t *testing.T) {
	t.Setenv("LOOM_FLEET_DB_API_KEY", "")
	t.Setenv("LOOM_FLEET_DB_URL", "")
	cfg := SandboxOneshotConfig{AgentName: "falcon", WorkspaceID: "WS1"}
	revoke, err := provisionSandboxCredential(t.Context(), &cfg)
	if err != nil {
		t.Fatalf("no-op provisioning should not error: %v", err)
	}
	if cfg.FleetDBKey != "" || cfg.FleetDBActor != "" {
		t.Errorf("no admin key → no provisioned credential; got key=%q actor=%q", cfg.FleetDBKey, cfg.FleetDBActor)
	}
	revoke() // must be safe to call
}
