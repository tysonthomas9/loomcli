package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- Strategy Name tests ----------

func TestDirectStrategy_Name(t *testing.T) {
	s := &DirectStrategy{}
	if got := s.Name(); got != "direct" {
		t.Errorf("DirectStrategy.Name() = %q, want %q", got, "direct")
	}
}

func TestSandboxStrategy_Name(t *testing.T) {
	s := &SandboxStrategy{}
	if got := s.Name(); got != "sandbox" {
		t.Errorf("SandboxStrategy.Name() = %q, want %q", got, "sandbox")
	}
}

// ---------- buildCreateArgs ----------

func TestSandboxStrategy_BuildCreateArgs(t *testing.T) {
	t.Run("basic flags present", func(t *testing.T) {
		s := &SandboxStrategy{
			cfg: SandboxConfig{
				Providers: []string{"claude", "github"},
				Network:   "open",
			},
			projectDir: t.TempDir(),
			repoURL:    "git@github.com:user/repo.git",
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon"},
			worktreePath: "/tmp/worktrees/falcon",
		}

		args := s.buildCreateArgs(ap, "loom-falcon-abc123")
		joined := strings.Join(args, " ")

		// Verify --name flag
		if !strings.Contains(joined, "--name loom-falcon-abc123") {
			t.Errorf("expected --name flag, got args: %v", args)
		}

		// Verify --upload flag (contains :/sandbox/bin/loom)
		hasUpload := false
		for i, a := range args {
			if a == "--upload" && i+1 < len(args) && strings.Contains(args[i+1], ":/sandbox/bin/loom") {
				hasUpload = true
				break
			}
		}
		if !hasUpload {
			t.Errorf("expected --upload with :/sandbox/bin/loom, got args: %v", args)
		}

		// Verify --provider flags
		providerCount := 0
		for _, a := range args {
			if a == "--provider" {
				providerCount++
			}
		}
		if providerCount != 2 {
			t.Errorf("expected 2 --provider flags, got %d in args: %v", providerCount, args)
		}

		// Verify --policy flag present (ensurePolicyFile for "open" creates a file)
		hasPolicy := false
		for _, a := range args {
			if a == "--policy" {
				hasPolicy = true
				break
			}
		}
		if !hasPolicy {
			t.Errorf("expected --policy flag, got args: %v", args)
		}

		// Verify --no-tty
		if !strings.Contains(joined, "--no-tty") {
			t.Errorf("expected --no-tty flag, got args: %v", args)
		}

		// Verify trailing "-- sh -c"
		n := len(args)
		if n < 3 || args[n-3] != "--" || args[n-2] != "sh" || !strings.HasPrefix(args[n-1], "set -e") {
			// The last 3 positional elements before the script are: "--", "sh", "-c"
			// Actually: args end with [..., "--", "sh", "-c", <script>]
			foundSep := false
			for i, a := range args {
				if a == "--" && i+2 < len(args) && args[i+1] == "sh" && args[i+2] == "-c" {
					foundSep = true
					break
				}
			}
			if !foundSep {
				t.Errorf("expected trailing '-- sh -c <script>', got args: %v", args)
			}
		}
	})

	t.Run("with image configured", func(t *testing.T) {
		s := &SandboxStrategy{
			cfg: SandboxConfig{
				Providers: []string{"claude"},
				From:      "ubuntu:22.04",
				Network:   "open",
			},
			projectDir: t.TempDir(),
			repoURL:    "git@github.com:user/repo.git",
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon"},
			worktreePath: "/tmp/worktrees/falcon",
		}

		args := s.buildCreateArgs(ap, "loom-falcon-abc")
		hasFrom := false
		for i, a := range args {
			if a == "--from" && i+1 < len(args) && args[i+1] == "ubuntu:22.04" {
				hasFrom = true
				break
			}
		}
		if !hasFrom {
			t.Errorf("expected --from ubuntu:22.04, got args: %v", args)
		}
	})

	t.Run("without image configured", func(t *testing.T) {
		s := &SandboxStrategy{
			cfg: SandboxConfig{
				Providers: []string{"claude"},
				Network:   "open",
			},
			projectDir: t.TempDir(),
			repoURL:    "git@github.com:user/repo.git",
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon"},
			worktreePath: "/tmp/worktrees/falcon",
		}

		args := s.buildCreateArgs(ap, "loom-falcon-abc")
		for _, a := range args {
			if a == "--from" {
				t.Errorf("--from should be absent when image is empty, got args: %v", args)
				break
			}
		}
	})
}

// ---------- buildSandboxCommand ----------

func TestSandboxStrategy_BuildSandboxCommand(t *testing.T) {
	t.Run("contains expected commands", func(t *testing.T) {
		s := &SandboxStrategy{
			cfg:     SandboxConfig{Backend: ""},
			repoURL: "git@github.com:user/repo.git",
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Backend: "claude"},
			worktreePath: "/tmp/worktrees/falcon",
		}

		script := s.buildSandboxCommand(ap)

		// Verify git clone with correct branch
		if !strings.Contains(script, "git clone --branch") {
			t.Error("expected git clone --branch in script")
		}
		if !strings.Contains(script, "'falcon'") {
			t.Error("expected branch 'falcon' in script")
		}

		// Verify loom task with --auto --daemon-mode
		if !strings.Contains(script, "loom task") {
			t.Error("expected 'loom task' in script")
		}
		if !strings.Contains(script, "--auto --daemon-mode") {
			t.Error("expected --auto --daemon-mode in script")
		}

		// Verify bd sync and git push
		if !strings.Contains(script, "bd sync") {
			t.Error("expected 'bd sync' in script")
		}
		if !strings.Contains(script, "git push origin") {
			t.Error("expected 'git push origin' in script")
		}
	})

	t.Run("shell quoting for special branch names", func(t *testing.T) {
		s := &SandboxStrategy{
			cfg:     SandboxConfig{},
			repoURL: "git@github.com:user/repo.git",
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "feature/it's-done"},
			worktreePath: "/tmp/worktrees/feature",
		}

		script := s.buildSandboxCommand(ap)

		// The branch name contains a single quote, so shellQuote should
		// escape it as: 'feature/it'\''s-done'
		if !strings.Contains(script, "'feature/it'\\''s-done'") {
			t.Errorf("expected escaped single quote in branch name, got script:\n%s", script)
		}
	})

	t.Run("backend override from config", func(t *testing.T) {
		s := &SandboxStrategy{
			cfg:     SandboxConfig{Backend: "openai"},
			repoURL: "git@github.com:user/repo.git",
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Backend: "claude"},
			worktreePath: "/tmp/worktrees/falcon",
		}

		script := s.buildSandboxCommand(ap)

		// SandboxConfig.Backend should be used when set
		if !strings.Contains(script, "--backend 'openai'") {
			t.Errorf("expected --backend 'openai' from config override, got script:\n%s", script)
		}
	})
}

// ---------- ensurePolicyFile ----------

func TestSandboxStrategy_EnsurePolicyFile(t *testing.T) {
	t.Run("network open generates file", func(t *testing.T) {
		tmpDir := t.TempDir()
		s := &SandboxStrategy{
			cfg:        SandboxConfig{Network: "open"},
			projectDir: tmpDir,
		}

		path := s.ensurePolicyFile()
		expected := filepath.Join(tmpDir, ".loom", "sandbox-policy-open.yaml")
		if path != expected {
			t.Errorf("ensurePolicyFile() = %q, want %q", path, expected)
		}

		// Verify file was created and contains valid content
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read generated policy file: %v", err)
		}
		if !strings.Contains(string(data), "version: 1") {
			t.Error("policy file missing 'version: 1'")
		}
		if !strings.Contains(string(data), "filesystem_policy") {
			t.Error("policy file missing 'filesystem_policy'")
		}
	})

	t.Run("empty network defaults to open", func(t *testing.T) {
		tmpDir := t.TempDir()
		s := &SandboxStrategy{
			cfg:        SandboxConfig{Network: ""},
			projectDir: tmpDir,
		}

		path := s.ensurePolicyFile()
		expected := filepath.Join(tmpDir, ".loom", "sandbox-policy-open.yaml")
		if path != expected {
			t.Errorf("ensurePolicyFile() = %q, want %q", path, expected)
		}

		// Verify file exists
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected policy file to exist at %q: %v", path, err)
		}
	})

	t.Run("custom network path returned as-is", func(t *testing.T) {
		s := &SandboxStrategy{
			cfg:        SandboxConfig{Network: "./custom.yaml"},
			projectDir: t.TempDir(),
		}

		path := s.ensurePolicyFile()
		if path != "./custom.yaml" {
			t.Errorf("ensurePolicyFile() = %q, want %q", path, "./custom.yaml")
		}
	})
}

// ---------- shellQuote ----------

func TestShellQuote_Sandbox(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple string",
			input: "hello",
			want:  "'hello'",
		},
		{
			name:  "string with single quote",
			input: "it's",
			want:  "'it'\\''s'",
		},
		{
			name:  "empty string",
			input: "",
			want:  "''",
		},
		{
			name:  "string with spaces",
			input: "hello world",
			want:  "'hello world'",
		},
		{
			name:  "string with multiple single quotes",
			input: "it's a 'test'",
			want:  "'it'\\''s a '\\''test'\\'''",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------- mergeSandboxConfig ----------

func TestMergeSandboxConfig(t *testing.T) {
	t.Run("agent config overrides daemon config", func(t *testing.T) {
		daemon := &SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
			From:      "ubuntu:20.04",
			Backend:   "claude",
		}
		agent := &SandboxConfig{
			Providers: []string{"github"},
			From:      "ubuntu:22.04",
			Backend:   "openai",
		}

		merged := mergeSandboxConfig(daemon, agent)

		if len(merged.Providers) != 1 || merged.Providers[0] != "github" {
			t.Errorf("Providers = %v, want [github]", merged.Providers)
		}
		if merged.From != "ubuntu:22.04" {
			t.Errorf("From = %q, want %q", merged.From, "ubuntu:22.04")
		}
		if merged.Backend != "openai" {
			t.Errorf("Backend = %q, want %q", merged.Backend, "openai")
		}
		// Network not overridden because agent.Network is empty
		if merged.Network != "open" {
			t.Errorf("Network = %q, want %q", merged.Network, "open")
		}
	})

	t.Run("nil agent config uses daemon defaults", func(t *testing.T) {
		daemon := &SandboxConfig{
			Providers: []string{"claude", "github"},
			Network:   "open",
			From:      "ubuntu:22.04",
		}

		merged := mergeSandboxConfig(daemon, nil)

		if len(merged.Providers) != 2 {
			t.Errorf("Providers = %v, want [claude github]", merged.Providers)
		}
		if merged.From != "ubuntu:22.04" {
			t.Errorf("From = %q, want %q", merged.From, "ubuntu:22.04")
		}
		if merged.Network != "open" {
			t.Errorf("Network = %q, want %q", merged.Network, "open")
		}
	})

	t.Run("nil daemon config uses zero values", func(t *testing.T) {
		agent := &SandboxConfig{
			Providers: []string{"claude"},
			From:      "alpine",
		}

		merged := mergeSandboxConfig(nil, agent)

		if len(merged.Providers) != 1 || merged.Providers[0] != "claude" {
			t.Errorf("Providers = %v, want [claude]", merged.Providers)
		}
		if merged.From != "alpine" {
			t.Errorf("From = %q, want %q", merged.From, "alpine")
		}
	})

	t.Run("both nil returns zero SandboxConfig", func(t *testing.T) {
		merged := mergeSandboxConfig(nil, nil)

		if len(merged.Providers) != 0 {
			t.Errorf("Providers = %v, want empty", merged.Providers)
		}
		if merged.Network != "" {
			t.Errorf("Network = %q, want empty", merged.Network)
		}
		if merged.From != "" {
			t.Errorf("From = %q, want empty", merged.From)
		}
		if merged.Backend != "" {
			t.Errorf("Backend = %q, want empty", merged.Backend)
		}
	})
}

// ---------- overlaySandboxConfig ----------

func TestOverlaySandboxConfig(t *testing.T) {
	t.Run("non-empty src fields override dst", func(t *testing.T) {
		dst := SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
			From:      "ubuntu:20.04",
			Backend:   "claude",
		}
		src := SandboxConfig{
			Providers: []string{"github", "aws"},
			Network:   "./custom.yaml",
			From:      "alpine:latest",
			Backend:   "openai",
		}

		overlaySandboxConfig(&dst, &src)

		if len(dst.Providers) != 2 || dst.Providers[0] != "github" || dst.Providers[1] != "aws" {
			t.Errorf("Providers = %v, want [github aws]", dst.Providers)
		}
		if dst.Network != "./custom.yaml" {
			t.Errorf("Network = %q, want %q", dst.Network, "./custom.yaml")
		}
		if dst.From != "alpine:latest" {
			t.Errorf("From = %q, want %q", dst.From, "alpine:latest")
		}
		if dst.Backend != "openai" {
			t.Errorf("Backend = %q, want %q", dst.Backend, "openai")
		}
	})

	t.Run("empty src fields leave dst unchanged", func(t *testing.T) {
		dst := SandboxConfig{
			Providers: []string{"claude", "github"},
			Network:   "open",
			From:      "ubuntu:22.04",
			Backend:   "claude",
		}
		src := SandboxConfig{} // all zero values

		overlaySandboxConfig(&dst, &src)

		if len(dst.Providers) != 2 || dst.Providers[0] != "claude" {
			t.Errorf("Providers = %v, want [claude github]", dst.Providers)
		}
		if dst.Network != "open" {
			t.Errorf("Network = %q, want %q", dst.Network, "open")
		}
		if dst.From != "ubuntu:22.04" {
			t.Errorf("From = %q, want %q", dst.From, "ubuntu:22.04")
		}
		if dst.Backend != "claude" {
			t.Errorf("Backend = %q, want %q", dst.Backend, "claude")
		}
	})

	t.Run("providers are replaced not appended", func(t *testing.T) {
		dst := SandboxConfig{
			Providers: []string{"claude", "github"},
		}
		src := SandboxConfig{
			Providers: []string{"aws"},
		}

		overlaySandboxConfig(&dst, &src)

		if len(dst.Providers) != 1 || dst.Providers[0] != "aws" {
			t.Errorf("Providers = %v, want [aws] (should replace, not append)", dst.Providers)
		}
	})
}

// ---------- resolveStrategy ----------

func TestResolveStrategy(t *testing.T) {
	t.Run("empty execution returns DirectStrategy", func(t *testing.T) {
		agent := AgentEntry{Worktree: "falcon", Role: "task", Execution: ""}
		strategy := resolveStrategy(agent, nil, t.TempDir())
		if strategy.Name() != "direct" {
			t.Errorf("resolveStrategy() returned %q, want direct", strategy.Name())
		}
	})

	t.Run("execution=direct returns DirectStrategy", func(t *testing.T) {
		agent := AgentEntry{Worktree: "falcon", Role: "task", Execution: "direct"}
		strategy := resolveStrategy(agent, nil, t.TempDir())
		if strategy.Name() != "direct" {
			t.Errorf("resolveStrategy() returned %q, want direct", strategy.Name())
		}
	})

	t.Run("execution=sandbox returns SandboxStrategy", func(t *testing.T) {
		agent := AgentEntry{
			Worktree:  "falcon",
			Role:      "task",
			Execution: "sandbox",
			Sandbox:   &SandboxConfig{Providers: []string{"claude"}},
		}
		daemonSandbox := &SandboxConfig{
			Providers: []string{"github"},
			Network:   "open",
		}
		strategy := resolveStrategy(agent, daemonSandbox, t.TempDir())
		if strategy.Name() != "sandbox" {
			t.Errorf("resolveStrategy() returned %q, want sandbox", strategy.Name())
		}
	})

	t.Run("default (no execution set) returns DirectStrategy", func(t *testing.T) {
		agent := AgentEntry{Worktree: "falcon", Role: "task"}
		strategy := resolveStrategy(agent, nil, t.TempDir())
		if strategy.Name() != "direct" {
			t.Errorf("resolveStrategy() returned %q, want direct", strategy.Name())
		}
	})
}

// ---------- Sandbox config validation ----------

func TestSandboxConfigValidation(t *testing.T) {
	t.Run("execution=sandbox with no providers is an error", func(t *testing.T) {
		agents := []AgentEntry{
			{Worktree: "falcon", Role: "task", Execution: "sandbox"},
		}
		r := &ValidationResult{}
		validateSandboxConfig(r, nil, agents, t.TempDir())

		if !r.HasErrors() {
			t.Error("expected validation error for sandbox with no providers")
		}
		found := false
		for _, issue := range r.Issues {
			if strings.Contains(issue.Message, "no providers configured") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'no providers configured' error, got issues: %v", r.Issues)
		}
	})

	t.Run("execution=sandbox with providers passes", func(t *testing.T) {
		agents := []AgentEntry{
			{Worktree: "falcon", Role: "task", Execution: "sandbox"},
		}
		daemonSandbox := &SandboxConfig{
			Providers: []string{"claude"},
		}
		r := &ValidationResult{}
		validateSandboxConfig(r, daemonSandbox, agents, t.TempDir())

		if r.HasErrors() {
			t.Errorf("expected no errors, got: %s", r.FormatIssues())
		}
	})

	t.Run("execution=sandbox with agent-level providers passes", func(t *testing.T) {
		agents := []AgentEntry{
			{
				Worktree:  "falcon",
				Role:      "task",
				Execution: "sandbox",
				Sandbox:   &SandboxConfig{Providers: []string{"claude"}},
			},
		}
		r := &ValidationResult{}
		validateSandboxConfig(r, nil, agents, t.TempDir())

		if r.HasErrors() {
			t.Errorf("expected no errors, got: %s", r.FormatIssues())
		}
	})

	t.Run("execution=invalid is an error", func(t *testing.T) {
		dc := &DaemonConfig{
			Agents: []AgentEntry{
				{Worktree: "falcon", Role: "task", Execution: "invalid"},
			},
			Roles: map[string]RoleConfig{"task": {}},
		}
		r := ValidateProjectConfig(dc, t.TempDir())
		if !r.HasErrors() {
			t.Error("expected validation error for execution=invalid")
		}
		found := false
		for _, issue := range r.Issues {
			if strings.Contains(issue.Message, "must be one of: direct, sandbox") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'must be one of' error, got issues: %v", r.Issues)
		}
	})

	t.Run("execution=empty passes", func(t *testing.T) {
		agents := []AgentEntry{
			{Worktree: "falcon", Role: "task", Execution: ""},
		}
		r := &ValidationResult{}
		validateSandboxConfig(r, nil, agents, t.TempDir())

		if r.HasErrors() {
			t.Errorf("expected no errors for empty execution, got: %s", r.FormatIssues())
		}
	})
}
