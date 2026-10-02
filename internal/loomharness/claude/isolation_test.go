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

// Test isolation (test setup only; product code never replaces HOME, R1).
// The test binary runs with HOME, CLAUDE_CONFIG_DIR and TMPDIR inside one
// owned /tmp root, so every t.TempDir and any fallback to "the user's root"
// lands there, and every launch is checked by isolated before it runs.

var (
	realHome  string // the account's home from the user database, not $HOME
	ownedRoot string // the owned test root, symlinks resolved
)

// ownTestHome creates the owned root, points HOME, CLAUDE_CONFIG_DIR and
// TMPDIR into it for the whole test binary, and returns its cleanup.
func ownTestHome() func() {
	if u, err := user.Current(); err == nil {
		realHome = u.HomeDir
	}
	home, err := os.MkdirTemp("/tmp", "loom-claude-test-home-")
	if err != nil {
		panic(err)
	}
	// Use the resolved path (/tmp is a symlink on macOS) so test roots are
	// real directories, as Purge requires.
	if ownedRoot, err = filepath.EvalSymlinks(home); err != nil {
		panic(err)
	}
	home = ownedRoot
	tmp := filepath.Join(home, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	_ = os.Setenv("TMPDIR", tmp)
	return func() { _ = os.RemoveAll(home) }
}

// within reports whether resolved path p is root or below it.
func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// noSymlink refuses absolute path p if it or any existing directory above it
// is a symlink, checked with Lstat and never followed (as realDir does), so a
// dangling alias is refused even when its target does not exist.
func noSymlink(p string) error {
	for cur := filepath.Clean(p); ; cur = filepath.Dir(cur) {
		info, err := os.Lstat(cur)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; refusing", cur)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if cur == filepath.Dir(cur) {
			return nil
		}
	}
}

// isolated fails closed unless every path the launch resolves (HOME, the
// effective Claude config dir, the transcript root and the working dir) is
// reached without any symlink and lies inside the owned test root.
func isolated(cfg Config, spec ProcessSpec) error {
	if ownedRoot == "" {
		return errors.New("isolation: no owned test root")
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
			return fmt.Errorf("isolation: %s %q is not an absolute path", name, p)
		}
		if err := noSymlink(p); err != nil {
			return fmt.Errorf("isolation: %s %q: %w", name, p, err)
		}
		r, err := canonical(p)
		if err != nil {
			return fmt.Errorf("isolation: %s %q: %w", name, p, err)
		}
		if !within(r, ownedRoot) {
			return fmt.Errorf("isolation: %s %q resolves to %q, outside the owned root %s", name, p, r, ownedRoot)
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
	if err := isolated(Config{Env: base}, ProcessSpec{Launch: loomharness.Launch{Root: filepath.Join(owned, "not", "yet")}, Dir: owned}); err != nil {
		t.Fatalf("a not-yet-existing owned path refused: %v", err)
	}
	// Symlink aliases of the real home's Claude root: one inside the owned
	// root, one in /tmp outside it.
	alias := filepath.Join(owned, "alias")
	if err := os.Symlink(filepath.Join(realHome, ".claude"), alias); err != nil {
		t.Fatal(err)
	}
	outside, err := os.MkdirTemp("/tmp", "loom-claude-unowned-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	homeAlias := filepath.Join(outside, "home")
	if err := os.Symlink(realHome, homeAlias); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		env  []string
		spec ProcessSpec
	}{
		"root in ~/.claude":             {base, ProcessSpec{Launch: loomharness.Launch{Root: filepath.Join(realHome, ".claude")}, Dir: owned}},
		"owned symlink to ~/.claude":    {base, ProcessSpec{Launch: loomharness.Launch{Root: alias}, Dir: owned}},
		"/tmp symlink to the real home": {[]string{"HOME=" + homeAlias}, ok},
		"unowned absolute path":         {base, ProcessSpec{Launch: loomharness.Launch{Root: "/user/own"}, Dir: owned}},
		"unowned /tmp dir":              {base, ProcessSpec{Launch: loomharness.Launch{Root: outside}, Dir: owned}},
		"real HOME":                     {[]string{"HOME=" + realHome}, ok},
		"config dir in ~/.claude":       {append(base, "CLAUDE_CONFIG_DIR="+filepath.Join(realHome, ".claude")), ok},
		"launch config in ~":            {base, ProcessSpec{Launch: loomharness.Launch{Root: owned, Env: map[string]string{"CLAUDE_CONFIG_DIR": realHome + "/x"}}, Dir: owned}},
		"relative dir":                  {base, ProcessSpec{Launch: loomharness.Launch{Root: owned}, Dir: "wt"}},
	} {
		if err := isolated(Config{Env: c.env}, c.spec); err == nil {
			t.Errorf("%s: not refused", name)
		}
	}
	tmp, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := canonical(os.Getenv("HOME")); !within(h, ownedRoot) || filepath.Dir(ownedRoot) != tmp {
		t.Fatalf("the test binary's HOME %q is not in an owned /tmp root (%q)", h, ownedRoot)
	}
}
