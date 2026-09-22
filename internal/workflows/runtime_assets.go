package workflows

// Runtime-asset staging for the meta-harness leaf.
//
// The flue bundle imports meta-harness at build time and spawns a real PTY at
// run time, so the generated server.mjs needs a built meta-harness clone next
// to it plus a pruned, platform-correct node-pty and @xterm/headless. These
// helpers resolve that clone and stage those assets; split out of workflows.go
// to keep that file under the package LOC gate.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// metaHarnessRoot resolves a BUILT meta-harness clone (the TS harness driver
// loomcli bundles for the local/sandbox leaf). It mirrors flueRuntimeRoot's
// env→sibling resolution and validates that `npm run build` has produced a dist/
// and that node-pty is installed — failing loud otherwise, since the flue bundle
// imports meta-harness at build time and needs its PTY assets at run time.
func metaHarnessRoot() (string, error) {
	candidates := []string{}
	if root := strings.TrimSpace(os.Getenv("LOOM_META_HARNESS_ROOT")); root != "" {
		candidates = append(candidates, root)
	}
	if root := strings.TrimSpace(os.Getenv("META_HARNESS_ROOT")); root != "" {
		candidates = append(candidates, root)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "..", "meta-harness"))
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if metaHarnessRootValid(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("built meta-harness not found; set LOOM_META_HARNESS_ROOT and run `npm run build` in the clone (needs package.json, dist/, node_modules/node-pty)")
}

// metaHarnessRootValid reports whether root is a built, installed meta-harness
// clone: the package manifest, a compiled dist entry, the copied PTY bridge, and
// the node-pty package must all be present.
func metaHarnessRootValid(root string) bool {
	required := []string{
		"package.json",
		filepath.Join("dist", "oneshot", "index.js"),
		filepath.Join("dist", "wrapper", "internal", "ptyHost.mjs"),
		filepath.Join("node_modules", "node-pty", "package.json"),
		filepath.Join("node_modules", "@xterm", "headless", "package.json"),
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return false
		}
	}
	return true
}

// filesUseMetaHarness reports whether any workflow source file imports the
// meta-harness package — the signal that its bundle needs the meta-harness build
// symlink and the staged PTY runtime assets.
func filesUseMetaHarness(files map[string]string) bool {
	for _, content := range files {
		if strings.Contains(content, "meta-harness") {
			return true
		}
	}
	return false
}

// copyRuntimeAssets stages the meta-harness PTY bridge, a pruned platform-correct
// node-pty, and @xterm/headless into the flue bundle output dir, next to the
// generated server.mjs (which bundles meta-harness and spawns a PTY at run time).
// Called for BOTH build paths: the daemon-leaf materialized bundle
// (BuildBuiltinBundle, outputDir == destDir) and the registered driver bundle
// (BuildAndRegister, outputDir == <buildRoot>/dist, which RegisterFlueDriver then
// copies+digests wholesale into the staged bundle) — so the launcher's
// META_HARNESS_PTY_HOST (dir(server.mjs)/ptyHost.mjs) always points at real assets.
//
// `needs` gates staging on the bundle actually importing meta-harness. When the
// clone is unresolvable we skip rather than fail: a real esbuild build that truly
// needs meta-harness has already failed at import resolution (the symlink was
// absent too), so reaching here clone-less means a stub/fake build (tests) with
// nothing to stage.
func copyRuntimeAssets(outputDir string, needs bool) error {
	if !needs {
		return nil
	}
	root, err := metaHarnessRoot()
	if err != nil {
		return nil //nolint:nilerr // see doc comment: clone-less here == stub build, nothing to stage.
	}
	if err := copyFile(
		filepath.Join(root, "dist", "wrapper", "internal", "ptyHost.mjs"),
		filepath.Join(outputDir, "ptyHost.mjs"),
	); err != nil {
		return fmt.Errorf("stage ptyHost.mjs: %w", err)
	}
	if err := copyPrunedNodePty(root, outputDir); err != nil {
		return err
	}
	if err := copyXtermHeadless(root, outputDir); err != nil {
		return err
	}
	// Fail the build LOUDLY if the staged native addon cannot load and drive a PTY
	// for this platform — an import check alone would miss a bad/missing prebuild.
	if err := verifyStagedNodePTY(outputDir); err != nil {
		return fmt.Errorf("staged node-pty PTY self-test failed: %w", err)
	}
	return nil
}

// copyXtermHeadless stages @xterm/headless (package.json + lib-headless/) next to
// server.mjs. meta-harness loads xterm via createRequire so the compiled dist runs
// under raw Node ESM (where xterm's CommonJS named export is invisible to Node's
// ESM lexer) — but that also stops esbuild from inlining it, so the flue bundle
// keeps a runtime require('@xterm/headless') that must resolve from the bundle.
// Pure JS, no native addon (~740KB).
func copyXtermHeadless(root, outputDir string) error {
	src := filepath.Join(root, "node_modules", "@xterm", "headless")
	dst := filepath.Join(outputDir, "node_modules", "@xterm", "headless")
	for _, rel := range []string{"package.json", "lib-headless"} {
		if err := copyPath(filepath.Join(src, rel), filepath.Join(dst, rel)); err != nil {
			return fmt.Errorf("stage @xterm/headless/%s: %w", rel, err)
		}
	}
	return nil
}

// copyPrunedNodePty copies only the runtime-necessary slice of node-pty
// (package.json + lib/ + prebuilds/<platform>-<arch>/) into
// outputDir/node_modules/node-pty. The full package is ~62MB of cross-platform
// prebuilds and C++ sources; the pruned tree is ~400KB, which matters because
// RegisterFlueDriver re-hashes the whole dist on every registration AND every run.
func copyPrunedNodePty(root, outputDir string) error {
	src := filepath.Join(root, "node_modules", "node-pty")
	dst := filepath.Join(outputDir, "node_modules", "node-pty")
	prebuild := filepath.Join("prebuilds", nodePlatformArch())
	if _, err := os.Stat(filepath.Join(src, prebuild)); err != nil {
		return fmt.Errorf("node-pty prebuild %s missing (build node-pty for this platform): %w", prebuild, err)
	}
	for _, rel := range []string{"package.json", "lib", prebuild} {
		if err := copyPath(filepath.Join(src, rel), filepath.Join(dst, rel)); err != nil {
			return fmt.Errorf("stage node-pty/%s: %w", filepath.ToSlash(rel), err)
		}
	}
	return nil
}

// nodePlatformArch maps Go's GOOS/GOARCH onto node's process.platform-process.arch
// naming (node-pty resolves prebuilds/<platform>-<arch>/pty.node).
func nodePlatformArch() string {
	osName := runtime.GOOS
	if osName == "windows" {
		osName = "win32"
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "ia32"
	}
	return osName + "-" + arch
}

// verifyStagedNodePTY spawns a node process that loads the staged node-pty and
// drives a trivial PTY round-trip from outputDir, proving the prebuild loads and
// spawns for this platform. Skipped on Windows (loom bundles build/run on unix).
func verifyStagedNodePTY(outputDir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	anchor := filepath.Join(outputDir, "ptyHost.mjs")
	script := fmt.Sprintf(`
const { createRequire } = require('node:module');
const pty = createRequire(%q)('node-pty');
const p = pty.spawn('/bin/echo', ['loom-pty-ok'], { name: 'xterm', cols: 80, rows: 24 });
let out = '';
p.onData((d) => { out += d; });
p.onExit(({ exitCode }) => {
  if (out.includes('loom-pty-ok') && exitCode === 0) process.exit(0);
  console.error('unexpected pty output/exit:', JSON.stringify(out), exitCode);
  process.exit(1);
});
setTimeout(() => { console.error('pty self-test timed out'); process.exit(1); }, 10000);
`, anchor)
	cmd := exec.Command("node", "-e", script) //nolint:gosec // fixed local runtime; script is a constant with a quoted path.
	cmd.Dir = outputDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyPath recursively copies a file or directory tree (real files only — no
// symlinks), so the result survives RegisterFlueDriver's copyTree + digest walk.
func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// copyFile copies a single regular file, creating parent dirs and preserving the
// source's permission bits (node-pty's pty.node must stay executable/loadable).
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec // src is a resolved package path.
	if err != nil {
		return err
	}
	defer in.Close()                                                                     //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm()) //nolint:gosec // dst derived from outputDir.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
