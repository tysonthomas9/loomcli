package claude

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Test isolation (test setup only; product code never replaces HOME,
// R1). The test binary runs with an owned /tmp HOME and CLAUDE_CONFIG_DIR, so
// any fallback to "the user's root" lands in /tmp, and every launch is
// checked by isolated before it runs.

// realHome is the account's home from the user database, not $HOME.
var realHome string

// ownTestHome points HOME and CLAUDE_CONFIG_DIR at an owned /tmp home for
// the whole test binary and returns its cleanup.
func ownTestHome() func() {
	if u, err := user.Current(); err == nil {
		realHome = u.HomeDir
	}
	home, err := os.MkdirTemp("/tmp", "loom-claude-test-home-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	return func() { _ = os.RemoveAll(home) }
}

// isolated fails closed unless every path the launch resolves is absolute
// and outside the real home: HOME, the effective Claude config dir, the
// transcript root and the working directory.
func isolated(cfg Config, spec ProcessSpec) error {
	if realHome == "" {
		return errors.New("isolation: the real home is unknown")
	}
	env := NewProcess(cfg, spec).env()
	home, _ := lookup(env, "HOME")
	config, ok := lookup(env, "CLAUDE_CONFIG_DIR")
	if !ok {
		config = filepath.Join(home, ".claude")
	}
	root := spec.Launch.Root
	if root == "" {
		root = config
	}
	for name, p := range map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "root": root, "dir": spec.Dir} {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("isolation: %s %q is not an absolute owned path", name, p)
		}
		if p == realHome || strings.HasPrefix(filepath.Clean(p)+"/", filepath.Clean(realHome)+"/") {
			return fmt.Errorf("isolation: %s %q is inside the real home %s", name, p, realHome)
		}
	}
	return nil
}

// newTestProcess is NewProcess after the isolation check.
func newTestProcess(t *testing.T, cfg Config, spec ProcessSpec) *Process {
	t.Helper()
	if err := isolated(cfg, spec); err != nil {
		t.Fatal(err)
	}
	return NewProcess(cfg, spec)
}

func TestClaudeTestIsolationGuard(t *testing.T) {
	owned := t.TempDir()
	ok := ProcessSpec{Launch: loomharness.Launch{Root: owned}, Dir: owned}
	base := []string{"HOME=" + owned}
	if err := isolated(Config{Env: base}, ok); err != nil {
		t.Fatalf("owned paths refused: %v", err)
	}
	for name, c := range map[string]struct {
		env  []string
		spec ProcessSpec
	}{
		"root in ~/.claude":        {base, ProcessSpec{Launch: loomharness.Launch{Root: filepath.Join(realHome, ".claude")}, Dir: owned}},
		"real HOME":                {[]string{"HOME=" + realHome}, ProcessSpec{Launch: loomharness.Launch{Root: owned}, Dir: owned}},
		"config dir in ~/.claude":  {append(base, "CLAUDE_CONFIG_DIR="+filepath.Join(realHome, ".claude")), ok},
		"launch config in ~":       {base, ProcessSpec{Launch: loomharness.Launch{Root: owned, Env: map[string]string{"CLAUDE_CONFIG_DIR": realHome + "/x"}}, Dir: owned}},
		"empty root falls to HOME": {[]string{"HOME=" + realHome}, ProcessSpec{Dir: owned}},
		"relative dir":             {base, ProcessSpec{Launch: loomharness.Launch{Root: owned}, Dir: "wt"}},
	} {
		if err := isolated(Config{Env: c.env}, c.spec); err == nil {
			t.Errorf("%s: not refused", name)
		}
	}
	if h := os.Getenv("HOME"); !strings.HasPrefix(h, "/tmp/") || h == realHome {
		t.Fatalf("the test binary's HOME %q is not an owned /tmp home", h)
	}
}
