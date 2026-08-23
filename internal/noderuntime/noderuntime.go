// Package noderuntime resolves the Node executable every Loom exec site uses
// to run Flue bundles. It is a stdlib-only leaf so internal/driver and
// internal/driver/sandbox can depend on it without widening their fan-out.
//
// Resolution order (DEV-V5-31 §3c, extended by DEV-V5-37):
//  1. LOOM_NODE_BIN — developer escape hatch; an invalid value is an error
//     with NO fallback so a typo never silently runs a different node.
//  2. A bundled sibling next to the running executable (`node`, `node.exe`,
//     `node-<triple>`, or `node-<triple>.exe`), the desktop sidecar shape
//     (Tauri externalBin → Contents/MacOS/node). The executable path is
//     symlink-resolved first so `/usr/local/bin/loom -> …/Contents/MacOS/loom`
//     still finds the bundle.
//  3. The reserved full-distribution location under the app bundle's
//     Resources: `<exeDir>/../Resources/node-runtime/bin/node` (and the
//     `Resources/resources/` variant). Nothing ships there today; it is
//     probed after the siblings and reported as "bundled" too.
//  4. `node` on PATH.
package noderuntime

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Source values reported on a Resolved runtime.
const (
	SourceOverride = "override"
	SourceBundled  = "bundled"
	SourcePath     = "path"
)

// EnvNodeBin is the developer override: an absolute path to a node executable.
const EnvNodeBin = "LOOM_NODE_BIN"

// ErrNodeRuntimeMissing is the sentinel every resolution failure wraps; the
// error class string is its message so callers can surface it verbatim.
var ErrNodeRuntimeMissing = errors.New("node_runtime_missing")

// Resolved is a usable Node executable and where it came from.
type Resolved struct {
	Path   string
	Source string
}

var (
	// executablePath is a seam for tests: the running binary's path (empty when
	// unknown), whose directory is probed for a bundled sibling node.
	executablePath = func() string {
		p, err := os.Executable()
		if err != nil {
			return ""
		}
		return p
	}
	// lookPath is a seam for tests.
	lookPath = exec.LookPath
)

var cache struct {
	mu       sync.Mutex
	valid    bool
	override string
	result   Resolved
	err      error
}

// Resolve returns the Node executable to exec. The result is cached
// process-wide, keyed on the current LOOM_NODE_BIN value so flipping the
// override re-resolves without an explicit reset.
func Resolve() (Resolved, error) {
	override := strings.TrimSpace(os.Getenv(EnvNodeBin))
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.valid && cache.override == override {
		return cache.result, cache.err
	}
	result, err := resolve(override)
	if err != nil {
		// Failures are not cached: a long-running serve must pick up a Node
		// installed (or a PATH fixed) after it started.
		return Resolved{}, err
	}
	cache.valid = true
	cache.override = override
	cache.result = result
	cache.err = nil
	slog.Info("node runtime resolved", "source", result.Source, "path", result.Path)
	return result, nil
}

// ResetForTest clears the resolution cache.
func ResetForTest() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.valid = false
	cache.override = ""
	cache.result = Resolved{}
	cache.err = nil
}

func resolve(override string) (Resolved, error) {
	if override != "" {
		if reason := executableFileProblem(override); reason != "" {
			return Resolved{}, fmt.Errorf("%w: %s=%s: %s", ErrNodeRuntimeMissing, EnvNodeBin, override, reason)
		}
		return Resolved{Path: override, Source: SourceOverride}, nil
	}
	exeDir := resolveExecutableDir()
	if exeDir != "" {
		if bundled := bundledNode(exeDir); bundled != "" {
			return Resolved{Path: bundled, Source: SourceBundled}, nil
		}
	}
	if path, err := lookPath("node"); err == nil && strings.TrimSpace(path) != "" {
		return Resolved{Path: path, Source: SourcePath}, nil
	}
	where := "<unknown executable dir>"
	if exeDir != "" {
		where = exeDir
	}
	return Resolved{}, fmt.Errorf("%w: no bundled Node next to %s and none on PATH; set %s to override", ErrNodeRuntimeMissing, where, EnvNodeBin)
}

// resolveExecutableDir returns the directory of the running executable with
// symlinks resolved ("" when the executable is unknown). A link that cannot
// be resolved (dangling) falls back to the raw path's directory.
func resolveExecutableDir() string {
	exe := executablePath()
	if exe == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

// bundledNode looks for the desktop sidecar, in order: next to the
// executable as `node`, `node.exe`, or the Tauri sidecar shape
// `node-<host triple>` (optionally `.exe`); then the reserved full
// distribution location `<exeDir>/../Resources/node-runtime/bin/node` (and
// its `Resources/resources/` variant). The sibling names are exact on
// purpose — this runs on every Flue exec, including plain CLI installs,
// where a `/usr/local/bin/node-gyp` next to loom must never be mistaken
// for Node.
func bundledNode(dir string) string {
	triple := hostTargetTriple()
	candidates := []string{
		filepath.Join(dir, "node"),
		filepath.Join(dir, "node.exe"),
		filepath.Join(dir, "node-"+triple),
		filepath.Join(dir, "node-"+triple+".exe"),
		filepath.Join(dir, "..", "Resources", "node-runtime", "bin", "node"),
		filepath.Join(dir, "..", "Resources", "resources", "node-runtime", "bin", "node"),
	}
	for _, candidate := range candidates {
		if executableFileProblem(candidate) == "" {
			return candidate
		}
	}
	return ""
}

// hostTargetTriple is the Tauri sidecar suffix for this OS/arch (kept in
// step with packaged.HostTargetTriple; duplicated so this leaf stays
// stdlib-only).
func hostTargetTriple() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "aarch64-apple-darwin"
	case "darwin/amd64":
		return "x86_64-apple-darwin"
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu"
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu"
	case "windows/amd64":
		return "x86_64-pc-windows-msvc"
	}
	return runtime.GOARCH + "-" + runtime.GOOS
}

// executableFileProblem returns "" when path is an existing regular file
// (symlinks followed) that is executable, else a short reason.
func executableFileProblem(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return err.Error()
	}
	if info.IsDir() {
		return "is a directory"
	}
	if !info.Mode().IsRegular() {
		return "is not a regular file"
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "is not executable"
	}
	return ""
}

// Describe reports the resolver state for readiness surfaces. It never panics.
func Describe() map[string]any {
	resolved, err := Resolve()
	out := map[string]any{
		"ok":     err == nil,
		"path":   resolved.Path,
		"source": resolved.Source,
		"error":  "",
	}
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}
