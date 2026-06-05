// Package flue manages the embedded flue agent-harness project that loom uses
// to run agents through the flue runtime.
//
// loom ships a default flue project as an embedded template. On first use the
// Manager scaffolds it to ~/.loom/flue, installs its npm dependencies, and
// builds it. Subsequent runs reuse the built project. Setting
// LOOM_FLUE_PROJECT_DIR points loom at a user-supplied flue project instead;
// loom then only installs/builds it (never scaffolds over it).
package flue

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// Environment variables that tune flue project bootstrap.
const (
	// EnvProjectDir overrides the managed project with a user-supplied flue
	// project directory.
	EnvProjectDir = "LOOM_FLUE_PROJECT_DIR"
	// EnvPkgManager forces the package manager ("npm" or "pnpm"). When unset,
	// pnpm is used if on PATH, otherwise npm.
	EnvPkgManager = "LOOM_FLUE_PKG_MGR"
	// EnvForceRebuild forces a full reinstall + rebuild on the next use.
	EnvForceRebuild = "LOOM_FLUE_FORCE_REBUILD"
)

const (
	projectSubdir     = "flue"
	serverArtifactRel = "dist/server.mjs"
	nodeModulesRel    = "node_modules"
	setupLockName     = "setup.lock"

	minNodeMajor = 22
	minNodeMinor = 18
)

// Manager owns the lifecycle of a flue project directory. It is safe for
// concurrent use: in-process callers serialize on mu, and cross-process
// callers serialize on a file lock during setup.
type Manager struct {
	projectDir string
	managed    bool

	mu      sync.Mutex
	ready   bool
	flueBin string
}

var (
	defaultManager     *Manager
	defaultManagerOnce sync.Once
)

// DefaultManager returns the process-wide Manager, resolving the project
// directory from LOOM_FLUE_PROJECT_DIR (user-supplied) or ~/.loom/flue
// (managed default).
func DefaultManager() *Manager {
	defaultManagerOnce.Do(func() {
		dir := strings.TrimSpace(os.Getenv(EnvProjectDir))
		managed := false
		if dir == "" {
			dir = filepath.Join(loomDir(), projectSubdir)
			managed = true
		}
		defaultManager = &Manager{projectDir: dir, managed: managed}
	})
	return defaultManager
}

// ProjectDir returns the resolved flue project directory.
func (m *Manager) ProjectDir() string { return m.projectDir }

// EnsureProject makes sure the flue project is scaffolded (managed only),
// its dependencies installed, and the project built. It returns the path to
// the project-local flue binary and the project directory. The first call in
// a fresh environment may take ~30-90s (npm install + build); later calls are
// effectively free.
func (m *Manager) EnsureProject(ctx context.Context) (flueBin, projectDir string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready {
		return m.flueBin, m.projectDir, nil
	}

	if _, err := resolveNode(); err != nil {
		return "", "", err
	}

	if err := os.MkdirAll(m.projectDir, 0o755); err != nil {
		return "", "", fmt.Errorf("flue backend: create project dir %s: %w", m.projectDir, err)
	}

	// Serialize setup across concurrent loom processes. Blocking (not
	// try-lock): a second process simply waits for the first to finish
	// installing, then re-checks state and returns fast.
	lock, err := acquireSetupLock(m.projectDir)
	if err != nil {
		return "", "", err
	}
	defer lock.release()

	flueBin, err = m.bootstrapLocked(ctx)
	if err != nil {
		return "", "", err
	}

	m.flueBin = flueBin
	m.ready = true
	return m.flueBin, m.projectDir, nil
}

// bootstrapLocked scaffolds (managed only), installs, and builds the project
// as needed, then records runtime state. The caller must hold the setup lock.
// Returns the resolved project-local flue binary path.
func (m *Manager) bootstrapLocked(ctx context.Context) (string, error) {
	hash, err := computeTemplateHash()
	if err != nil {
		return "", err
	}
	force := os.Getenv(EnvForceRebuild) != ""
	stale, err := m.resolveStaleAndScaffold(hash, force)
	if err != nil {
		return "", err
	}

	pkgMgr := detectPkgManager()
	needInstall := stale || !dirExists(filepath.Join(m.projectDir, nodeModulesRel))
	needBuild := stale || !fileExists(filepath.Join(m.projectDir, serverArtifactRel))

	if needInstall {
		progressf("Setting up flue runtime in %s (first run, this can take a minute)...", m.projectDir)
		if err := runInstall(ctx, pkgMgr, m.projectDir); err != nil {
			return "", err
		}
	}

	flueBin, err := resolveFlueBin(m.projectDir)
	if err != nil {
		return "", err
	}

	if needBuild {
		progressf("Building flue project...")
		if err := runBuild(ctx, flueBin, m.projectDir); err != nil {
			return "", err
		}
	}

	if needInstall || needBuild {
		nodeVer, _ := nodeVersionString()
		_ = writeRuntime(m.projectDir, projectRuntime{
			TemplateVersion: hash,
			NodeVersion:     nodeVer,
			PkgManager:      pkgMgr,
			InstalledAt:     time.Now().UTC(),
			BuiltAt:         time.Now().UTC(),
		})
	}

	return flueBin, nil
}

// resolveStaleAndScaffold decides whether the project needs a full reinstall +
// rebuild and, for the managed project, scaffolds the embedded template when
// stale. Template-version staleness only applies to the managed project; a
// user-supplied LOOM_FLUE_PROJECT_DIR is only "stale" when force is set (its
// sources are never owned by loom).
func (m *Manager) resolveStaleAndScaffold(hash string, force bool) (bool, error) {
	if !m.managed {
		return force, nil
	}
	rt, _ := readRuntime(m.projectDir) // missing/corrupt → nil → treat as fresh
	stale := force || rt == nil || rt.TemplateVersion != hash
	if stale {
		if err := scaffoldTemplate(m.projectDir); err != nil {
			return false, fmt.Errorf("flue backend: scaffold project: %w", err)
		}
	}
	return stale, nil
}

// Status is a read-only snapshot of the managed project's readiness, used to
// build a backend HealthStatus without triggering a bootstrap.
type Status struct {
	ProjectDir    string
	NodeInstalled bool
	NodeVersion   string
	NodeError     string
	Installed     bool // node_modules present
	Built         bool // dist/server.mjs present
}

// Probe reports the current readiness of the project without mutating it.
func (m *Manager) Probe() Status {
	s := Status{ProjectDir: m.projectDir}
	if ver, err := nodeVersionString(); err != nil {
		s.NodeError = err.Error()
	} else if err := checkNodeVersion(ver); err != nil {
		s.NodeVersion = ver
		s.NodeError = err.Error()
	} else {
		s.NodeInstalled = true
		s.NodeVersion = ver
	}
	s.Installed = dirExists(filepath.Join(m.projectDir, nodeModulesRel))
	s.Built = fileExists(filepath.Join(m.projectDir, serverArtifactRel))
	return s
}

// ─── setup lock ───────────────────────────────────────────────────────────

type setupLock struct {
	file *os.File
}

func acquireSetupLock(projectDir string) (*setupLock, error) {
	path := filepath.Join(projectDir, setupLockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // user-private setup lock
	if err != nil {
		return nil, fmt.Errorf("flue backend: open setup lock %s: %w", path, err)
	}
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flue backend: lock setup %s: %w", path, err)
	}
	return &setupLock{file: f}, nil
}

func (l *setupLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = lockfile.FlockUnlock(l.file)
	_ = l.file.Close()
	l.file = nil
}

// ─── node / package manager / build ─────────────────────────────────────────

// resolveNode confirms node is on PATH at a supported version.
func resolveNode() (string, error) {
	path, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("flue backend: node not found on PATH. Install Node.js >= %d.%d from https://nodejs.org/", minNodeMajor, minNodeMinor)
	}
	ver, err := nodeVersionString()
	if err != nil {
		return "", err
	}
	if err := checkNodeVersion(ver); err != nil {
		return "", err
	}
	return path, nil
}

func nodeVersionString() (string, error) {
	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("flue backend: `node --version` failed (is Node.js installed?): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// checkNodeVersion validates a "vMAJOR.MINOR.PATCH" string against the
// minimum supported Node version.
func checkNodeVersion(v string) error {
	major, minor, err := parseNodeVersion(v)
	if err != nil {
		return err
	}
	if major > minNodeMajor || (major == minNodeMajor && minor >= minNodeMinor) {
		return nil
	}
	return fmt.Errorf("flue backend: node %s found, need >= %d.%d. Upgrade at https://nodejs.org/", v, minNodeMajor, minNodeMinor)
}

func parseNodeVersion(v string) (major, minor int, err error) {
	s := strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("flue backend: unrecognized node version %q", v)
	}
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, fmt.Errorf("flue backend: unrecognized node version %q", v)
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, fmt.Errorf("flue backend: unrecognized node version %q", v)
	}
	return major, minor, nil
}

// detectPkgManager picks pnpm when available, else npm. LOOM_FLUE_PKG_MGR
// overrides the choice.
func detectPkgManager() string {
	if forced := strings.TrimSpace(os.Getenv(EnvPkgManager)); forced != "" {
		return forced
	}
	if _, err := exec.LookPath("pnpm"); err == nil {
		return "pnpm"
	}
	return "npm"
}

func runInstall(ctx context.Context, pkgMgr, dir string) error {
	var args []string
	switch pkgMgr {
	case "pnpm":
		args = []string{"install"}
	default: // npm
		if fileExists(filepath.Join(dir, "package-lock.json")) {
			args = []string{"ci"}
		} else {
			args = []string{"install"}
		}
	}
	cmd := exec.CommandContext(ctx, pkgMgr, args...) //nolint:gosec // pkgMgr is npm/pnpm or operator-set
	cmd.Dir = dir
	// Install output is progress, not agent output: keep it off stdout so it
	// never pollutes loom's structured streams.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flue backend: dependency install failed (run manually: cd %s && %s %s): %w",
			dir, pkgMgr, strings.Join(args, " "), err)
	}
	return nil
}

func runBuild(ctx context.Context, flueBin, dir string) error {
	cmd := exec.CommandContext(ctx, flueBin, "build", "--target", "node", "--root", dir) //nolint:gosec // flueBin resolved from project node_modules
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flue backend: project build failed (run manually: cd %s && %s build --target node): %w",
			dir, flueBin, err)
	}
	return nil
}

// resolveFlueBin returns the project-local flue executable. After install it
// lives in node_modules/.bin; if absent it falls back to a flue on PATH.
func resolveFlueBin(dir string) (string, error) {
	name := "flue"
	if runtime.GOOS == "windows" {
		name = "flue.cmd"
	}
	local := filepath.Join(dir, nodeModulesRel, ".bin", name)
	if fileExists(local) {
		return local, nil
	}
	if p, err := exec.LookPath("flue"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("flue backend: flue CLI not found at %s and not on PATH (dependency install may have failed)", local)
}

// ─── helpers ────────────────────────────────────────────────────────────────

// loomDir mirrors bootstrap.LoomDir without importing it, keeping this a
// dependency-light leaf package.
func loomDir() string {
	if dir := os.Getenv("LOOM_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".loom"
	}
	return filepath.Join(home, ".loom")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// progressf prints a one-line progress message to stderr so it is visible to
// the operator without contaminating stdout (which carries agent output).
func progressf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[loom] "+format+"\n", args...)
}
