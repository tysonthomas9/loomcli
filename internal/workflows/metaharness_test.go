package workflows

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyRuntimeAssets stages the meta-harness PTY bridge + pruned node-pty into
// a temp output dir and asserts the layout the launcher's META_HARNESS_PTY_HOST
// depends on. copyRuntimeAssets also runs a real node-pty PTY self-test, so a green
// run proves the staged prebuild actually loads and spawns for this platform.
//
// Gated on a built meta-harness clone being resolvable (set LOOM_META_HARNESS_ROOT
// or place the clone at ../meta-harness); skipped otherwise so CI without the clone
// stays green.
func TestCopyRuntimeAssets(t *testing.T) {
	if _, err := metaHarnessRoot(); err != nil {
		t.Skipf("built meta-harness clone not available: %v", err)
	}

	out := t.TempDir()
	if err := copyRuntimeAssets(out, true); err != nil {
		t.Fatalf("copyRuntimeAssets: %v", err)
	}

	// needs=false is a no-op (unrelated bundles must not pull in PTY assets).
	skip := t.TempDir()
	if err := copyRuntimeAssets(skip, false); err != nil {
		t.Fatalf("copyRuntimeAssets(needs=false): %v", err)
	}
	if _, err := os.Stat(filepath.Join(skip, "ptyHost.mjs")); err == nil {
		t.Error("needs=false should stage nothing, but ptyHost.mjs appeared")
	}

	// ptyHost.mjs sits next to where server.mjs would be.
	if _, err := os.Stat(filepath.Join(out, "ptyHost.mjs")); err != nil {
		t.Fatalf("ptyHost.mjs not staged: %v", err)
	}
	// Pruned node-pty: package manifest + lib + this platform's prebuild present...
	nodePty := filepath.Join(out, "node_modules", "node-pty")
	for _, rel := range []string{
		"package.json",
		filepath.Join("lib", "index.js"),
		filepath.Join("prebuilds", nodePlatformArch(), "pty.node"),
	} {
		if _, err := os.Stat(filepath.Join(nodePty, rel)); err != nil {
			t.Fatalf("node-pty/%s not staged: %v", rel, err)
		}
	}
	// ...and the heavy build-only trees are pruned OUT (keeps the bundle + its
	// per-run digest small).
	for _, rel := range []string{"src", "deps", "third_party"} {
		if _, err := os.Stat(filepath.Join(nodePty, rel)); err == nil {
			t.Errorf("node-pty/%s should be pruned but was staged", rel)
		}
	}

	// @xterm/headless must be staged too: meta-harness loads it via createRequire,
	// so esbuild leaves it a runtime require that resolves from the bundle.
	xterm := filepath.Join(out, "node_modules", "@xterm", "headless")
	for _, rel := range []string{"package.json", filepath.Join("lib-headless", "xterm-headless.js")} {
		if _, err := os.Stat(filepath.Join(xterm, rel)); err != nil {
			t.Fatalf("@xterm/headless/%s not staged: %v", rel, err)
		}
	}
}

func TestNodePlatformArch(t *testing.T) {
	// Sanity: the mapping never emits a Go arch/os token node-pty wouldn't know.
	got := nodePlatformArch()
	if got == "" {
		t.Fatal("empty platform-arch")
	}
	for _, bad := range []string{"amd64", "windows", "386"} {
		if got == bad || filepath.Base(got) == bad {
			t.Errorf("nodePlatformArch %q leaked a Go token %q", got, bad)
		}
	}
}
