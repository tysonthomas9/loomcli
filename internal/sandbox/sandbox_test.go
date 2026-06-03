package sandbox

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
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Network != "open" {
		t.Errorf("Network = %q, want open", c.Network)
	}
	if !slices.Equal(c.Providers, []string{"claude", "github"}) {
		t.Errorf("Providers = %v, want [claude github]", c.Providers)
	}
}

func TestDefaultConfig_EnvOverrides(t *testing.T) {
	t.Run("policy + providers + backend", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_POLICY", "/tmp/p.yaml")
		t.Setenv("LOOM_SANDBOX_PROVIDERS", "claude")
		t.Setenv("LOOM_SANDBOX_BACKEND", "playground")
		c := DefaultConfig()
		if c.Network != "/tmp/p.yaml" {
			t.Errorf("Network = %q, want /tmp/p.yaml", c.Network)
		}
		if !slices.Equal(c.Providers, []string{"claude"}) {
			t.Errorf("Providers = %v, want [claude]", c.Providers)
		}
		if c.Backend != "playground" {
			t.Errorf("Backend = %q, want playground", c.Backend)
		}
	})
	t.Run("empty providers disables attachment", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_PROVIDERS", "")
		if c := DefaultConfig(); len(c.Providers) != 0 {
			t.Errorf("Providers = %v, want none", c.Providers)
		}
	})
}

func TestBuildCreateArgs(t *testing.T) {
	t.Run("open network with providers", func(t *testing.T) {
		args := BuildCreateArgs("loom-falcon-abc",
			Config{Network: "open", Providers: []string{"claude", "github"}})

		if !slices.Equal(args[:4], []string{"sandbox", "create", "--name", "loom-falcon-abc"}) {
			t.Errorf("unexpected leading args: %v", args[:4])
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"--provider claude", "--provider github", "--auto-providers"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in %v", want, args)
			}
		}
		// v0.0.53: no --upload create flag; "open" passes no --policy. Create ends
		// with a trivial `-- true` so it returns instead of attaching a shell (F2).
		if slices.Contains(args, "--upload") {
			t.Error("v0.0.53 has no --upload create flag")
		}
		if slices.Contains(args, "--policy") {
			t.Error("open network must not pass --policy")
		}
		if n := len(args); n < 2 || args[n-2] != "--" || args[n-1] != "true" {
			t.Errorf("create must end with `-- true` (keep-alive without a shell), got %v", args)
		}
	})

	t.Run("custom network and image", func(t *testing.T) {
		args := BuildCreateArgs("loom-x-1",
			Config{Network: "/etc/policy.yaml", From: "ubuntu:22.04"})
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

func TestResolveLoomBinary(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		t.Setenv("LOOM_SANDBOX_LOOM_BIN", "/tmp/loom-linux")
		got, err := ResolveLoomBinary()
		if err != nil || got != "/tmp/loom-linux" {
			t.Fatalf("got (%q, %v), want (/tmp/loom-linux, nil)", got, err)
		}
	})
	t.Run("non-linux host without override errors", func(t *testing.T) {
		if runtime.GOOS == "linux" {
			t.Skip("on a linux host the running binary is uploadable")
		}
		t.Setenv("LOOM_SANDBOX_LOOM_BIN", "")
		_, err := ResolveLoomBinary()
		if err == nil || !strings.Contains(err.Error(), "linux") {
			t.Fatalf("expected a 'needs a linux build' error, got %v", err)
		}
	})
}
