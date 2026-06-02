package agent

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"abc":       "'abc'",
		"a b":       "'a b'",
		"it's":      `'it'\''s'`,
		"":          "''",
		"feature/x": "'feature/x'",
		"a'b'c":     `'a'\''b'\''c'`,
		"$(rm -rf)": "'$(rm -rf)'",
		"a\nb":      "'a\nb'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultSandboxConfig(t *testing.T) {
	c := defaultSandboxConfig()
	if c.Network != "open" {
		t.Errorf("Network = %q, want open", c.Network)
	}
	if !slices.Equal(c.Providers, []string{"claude", "github"}) {
		t.Errorf("Providers = %v, want [claude github]", c.Providers)
	}
}

func TestDefaultSandboxConfig_EnvOverrides(t *testing.T) {
	t.Run("policy + providers", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_POLICY", "/tmp/p.yaml")
		t.Setenv("LOOM_SANDBOX_PROVIDERS", "claude")
		c := defaultSandboxConfig()
		if c.Network != "/tmp/p.yaml" {
			t.Errorf("Network = %q, want /tmp/p.yaml", c.Network)
		}
		if !slices.Equal(c.Providers, []string{"claude"}) {
			t.Errorf("Providers = %v, want [claude]", c.Providers)
		}
	})
	t.Run("empty providers disables attachment", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_PROVIDERS", "")
		if c := defaultSandboxConfig(); len(c.Providers) != 0 {
			t.Errorf("Providers = %v, want none", c.Providers)
		}
	})
}

func TestBuildOneshotCommand_FullScript(t *testing.T) {
	cfg := SandboxOneshotConfig{
		AgentType: "task", AgentName: "falcon", ParentID: "epic-1",
		ServerURL: "http://host.docker.internal:8080", WorkspaceID: "ws-123",
	}
	script := buildOneshotCommand("feature/x", cfg, "https://github.com/o/r.git", "claude")

	wants := []string{
		"set -e\n",
		"chmod +x /sandbox/loom\n",
		"export GIT_SSL_NO_VERIFY=1\n",
		"git clone --branch 'feature/x' --single-branch 'https://github.com/o/r.git' /sandbox/repo\n",
		"cd /sandbox/repo\n",
		// FleetDB connectivity: the agent inside reaches loom-serve for task state.
		"export LOOM_SERVER_URL='http://host.docker.internal:8080'\n",
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
	// v5 task state is FleetDB-backed; the beads `bd sync` step must not return.
	if strings.Contains(script, "bd ") || strings.Contains(script, "beads") {
		t.Errorf("bootstrap script still references beads:\n%s", script)
	}
}

func TestBuildOneshotCommand_NoBackendNoParent(t *testing.T) {
	cfg := SandboxOneshotConfig{AgentType: "plan", AgentName: "nova"}
	script := buildOneshotCommand("main", cfg, "git@x:o/r.git", "")

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

func TestBuildSandboxCreateArgs(t *testing.T) {
	t.Run("open network with providers", func(t *testing.T) {
		args := buildSandboxCreateArgs("loom-falcon-abc",
			SandboxConfig{Network: "open", Providers: []string{"claude", "github"}})

		if !slices.Equal(args[:4], []string{"sandbox", "create", "--name", "loom-falcon-abc"}) {
			t.Errorf("unexpected leading args: %v", args[:4])
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"--provider claude", "--provider github", "--auto-providers"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in %v", want, args)
			}
		}
		// v0.0.53: no --upload create flag; one-shot stays interactive (no
		// --no-tty); "open" passes no --policy. Create ends with a trivial
		// `-- true` so it returns instead of attaching an interactive shell (F2).
		if slices.Contains(args, "--upload") {
			t.Error("v0.0.53 has no --upload create flag")
		}
		if slices.Contains(args, "--no-tty") {
			t.Error("one-shot create is interactive")
		}
		if slices.Contains(args, "--policy") {
			t.Error("open network must not pass --policy")
		}
		if n := len(args); n < 2 || args[n-2] != "--" || args[n-1] != "true" {
			t.Errorf("create must end with `-- true` (keep-alive without a shell), got %v", args)
		}
	})

	t.Run("custom network and image", func(t *testing.T) {
		args := buildSandboxCreateArgs("loom-x-1",
			SandboxConfig{Network: "/etc/policy.yaml", From: "ubuntu:22.04"})
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--policy /etc/policy.yaml") {
			t.Errorf("custom network must pass --policy: %v", args)
		}
		if !strings.Contains(joined, "--from ubuntu:22.04") {
			t.Errorf("From must pass --from: %v", args)
		}
		if slices.Contains(args, "--auto-providers") {
			t.Error("no providers => no --auto-providers")
		}
	})
}

func TestResolveSandboxServerURL(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_SERVER_URL", "https://fleet.internal:9000")
		t.Setenv("LOOM_SERVER_URL", "http://localhost:8080")
		if got := resolveSandboxServerURL(); got != "https://fleet.internal:9000" {
			t.Errorf("got %q, want the explicit override", got)
		}
	})
	t.Run("localhost rewritten to host gateway", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_SERVER_URL", "")
		t.Setenv("LOOM_SERVER_URL", "http://localhost:8080")
		if got := resolveSandboxServerURL(); got != "http://host.docker.internal:8080" {
			t.Errorf("got %q, want host-gateway rewrite", got)
		}
	})
	t.Run("127.0.0.1 rewritten", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_SERVER_URL", "")
		t.Setenv("LOOM_SERVER_URL", "http://127.0.0.1:8080")
		if got := resolveSandboxServerURL(); got != "http://host.docker.internal:8080" {
			t.Errorf("got %q, want host-gateway rewrite", got)
		}
	})
	t.Run("none configured returns empty", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_SERVER_URL", "")
		t.Setenv("LOOM_SERVER_URL", "")
		if got := resolveSandboxServerURL(); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestResolveSandboxLoomBinary(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_LOOM_BIN", "/tmp/loom-linux")
		got, err := resolveSandboxLoomBinary()
		if err != nil || got != "/tmp/loom-linux" {
			t.Fatalf("got (%q, %v), want (/tmp/loom-linux, nil)", got, err)
		}
	})
	t.Run("non-linux host without override errors", func(t *testing.T) {
		if runtime.GOOS == "linux" {
			t.Skip("on a linux host the running binary is uploadable")
		}
		t.Setenv("LOOM_SANDBOX_LOOM_BIN", "")
		_, err := resolveSandboxLoomBinary()
		if err == nil || !strings.Contains(err.Error(), "linux") {
			t.Fatalf("expected a 'needs a linux build' error, got %v", err)
		}
	})
}
