package agent

import (
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

func TestBuildOneshotCommand_FullScript(t *testing.T) {
	cfg := SandboxOneshotConfig{
		AgentType: "task", AgentName: "falcon", ParentID: "epic-1",
		ServerURL: "http://host.docker.internal:8080", WorkspaceID: "ws-123",
	}
	script := buildOneshotCommand("feature/x", cfg, "https://github.com/o/r.git", "claude")

	wants := []string{
		"set -e\n",
		"chmod +x /sandbox/bin/loom\n",
		"export GIT_SSL_NO_VERIFY=1\n",
		"git clone --branch 'feature/x' --single-branch 'https://github.com/o/r.git' /sandbox/repo\n",
		"cd /sandbox/repo\n",
		// FleetDB connectivity: the agent inside reaches loom-serve for task state.
		"export LOOM_SERVER_URL='http://host.docker.internal:8080'\n",
		"export LOOM_WORKSPACE='ws-123'\n",
		"/sandbox/bin/loom 'task' 'worktrees/falcon' --backend 'claude' --parent 'epic-1'\n",
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

	if !strings.Contains(script, "/sandbox/bin/loom 'plan' 'worktrees/nova'\n") {
		t.Errorf("expected bare loom invocation without flags\n%s", script)
	}
	if strings.Contains(script, "--backend") {
		t.Error("expected no --backend when backend override empty")
	}
	if strings.Contains(script, "--parent") {
		t.Error("expected no --parent when ParentID empty")
	}
}

func TestBuildOneshotCreateArgs_OpenNetwork(t *testing.T) {
	cfg := SandboxConfig{Network: "open", Providers: []string{"claude", "github"}}
	args := buildOneshotCreateArgs(cfg, "loom-falcon-abc", "br",
		SandboxOneshotConfig{AgentType: "task", AgentName: "falcon"}, "https://x")

	if !slices.Equal(args[:4], []string{"sandbox", "create", "--name", "loom-falcon-abc"}) {
		t.Errorf("unexpected leading args: %v", args[:4])
	}
	// "open" must NOT pass --policy, and one-shot must stay interactive (no --no-tty).
	if slices.Contains(args, "--policy") {
		t.Error("open network must not pass --policy")
	}
	if slices.Contains(args, "--no-tty") {
		t.Error("one-shot mode must be interactive (no --no-tty)")
	}
	if n := countFlag(args, "--provider"); n != 2 {
		t.Errorf("--provider count = %d, want 2", n)
	}
	assertTrailingShellCommand(t, args)
}

func TestBuildOneshotCreateArgs_CustomNetworkAndImage(t *testing.T) {
	cfg := SandboxConfig{Network: "/etc/policy.yaml", From: "ubuntu:22.04"}
	args := buildOneshotCreateArgs(cfg, "loom-x-1", "br",
		SandboxOneshotConfig{AgentType: "task", AgentName: "x"}, "https://x")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--policy /etc/policy.yaml") {
		t.Errorf("custom network must pass --policy: %v", args)
	}
	if !strings.Contains(joined, "--from ubuntu:22.04") {
		t.Errorf("From must pass --from: %v", args)
	}
	assertTrailingShellCommand(t, args)
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

// countFlag counts occurrences of flag in args.
func countFlag(args []string, flag string) int {
	n := 0
	for _, a := range args {
		if a == flag {
			n++
		}
	}
	return n
}

// assertTrailingShellCommand checks the args end with `-- sh -c <script>`.
func assertTrailingShellCommand(t *testing.T, args []string) {
	t.Helper()
	if len(args) < 4 {
		t.Fatalf("args too short for trailing command: %v", args)
	}
	tail := args[len(args)-4:]
	if tail[0] != "--" || tail[1] != "sh" || tail[2] != "-c" {
		t.Errorf("expected trailing `-- sh -c <script>`, got %v", tail)
	}
	if !strings.Contains(tail[3], "git clone") {
		t.Errorf("trailing script should be the bootstrap command, got %q", tail[3])
	}
}
