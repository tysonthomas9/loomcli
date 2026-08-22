package cli

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestZshRcShimEnvMaterializesShim(t *testing.T) {
	t.Setenv(zshShimOptOutEnv, "")
	home := t.TempDir()
	configDir := filepath.Join(home, "loom-config")
	userZdotdir := filepath.Join(home, "user-zdotdir")
	selfDir := filepath.Join(home, "self'bin")
	executable := writeStubExecutable(t, selfDir)
	stubSelfExecutable(t, executable, nil)
	selfDir = resolvedPath(t, selfDir)

	env := []string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
		"LOOM_CONFIG_DIR=" + configDir,
		"ZDOTDIR=" + userZdotdir,
		"LOOM_SHIM_USER_ZDOTDIR=/stale",
		"zdotdir=case-sensitive",
	}
	got := ZshRcShimEnv(env)

	digest := sha256.Sum256([]byte(selfDir))
	wantShimDir := filepath.Join(configDir, "shell-shim", fmt.Sprintf("zsh-%x", digest[:4]))
	if value := envValueExact(got, "ZDOTDIR"); value != wantShimDir {
		t.Fatalf("ZDOTDIR = %q, want %q", value, wantShimDir)
	}
	if value := envValueExact(got, zshShimMarkerEnv); value != userZdotdir {
		t.Fatalf("%s = %q, want %q", zshShimMarkerEnv, value, userZdotdir)
	}
	if value := envValueExact(got, "zdotdir"); value != "case-sensitive" {
		t.Fatalf("lowercase zdotdir = %q, want it preserved", value)
	}
	assertSingleEnvKey(t, got, "ZDOTDIR")
	assertSingleEnvKey(t, got, zshShimMarkerEnv)
	assertFileMode(t, wantShimDir, 0o700)

	for _, name := range zshStartupFiles {
		assertFileContent(t, filepath.Join(wantShimDir, name), wantZshShimContent(name, selfDir))
		assertFileMode(t, filepath.Join(wantShimDir, name), 0o600)
	}
	assertFileContent(t, filepath.Join(wantShimDir, ".zlogout"), wantZshLogoutContent())
	assertFileMode(t, filepath.Join(wantShimDir, ".zlogout"), 0o600)
	assertFileContent(t, filepath.Join(wantShimDir, ".loom-shim"), selfDir+"\n")
	assertFileMode(t, filepath.Join(wantShimDir, ".loom-shim"), 0o600)
}

func TestZshRcShimEnvUsesHomeForDefaults(t *testing.T) {
	t.Setenv(zshShimOptOutEnv, "")
	home := t.TempDir()
	executable := writeStubExecutable(t, filepath.Join(home, "bin"))
	stubSelfExecutable(t, executable, nil)

	got := ZshRcShimEnv([]string{"HOME=" + home, "PATH=/usr/bin"})
	if value := envValueExact(got, zshShimMarkerEnv); value != home {
		t.Fatalf("%s = %q, want HOME %q", zshShimMarkerEnv, value, home)
	}
	shimDir := envValueExact(got, "ZDOTDIR")
	wantParent := filepath.Join(home, ".loom", "shell-shim") + string(os.PathSeparator)
	if !strings.HasPrefix(shimDir, wantParent) {
		t.Fatalf("ZDOTDIR = %q, want it below %q", shimDir, wantParent)
	}
}

func TestZshRcShimEnvPreservesExplicitEmptyZdotdir(t *testing.T) {
	t.Setenv(zshShimOptOutEnv, "")
	home := t.TempDir()
	executable := writeStubExecutable(t, filepath.Join(home, "bin"))
	stubSelfExecutable(t, executable, nil)

	got := ZshRcShimEnv([]string{"HOME=" + home, "PATH=/usr/bin", "ZDOTDIR="})
	if value := envValueExact(got, zshShimMarkerEnv); value != "" {
		t.Fatalf("%s = %q, want explicitly empty original ZDOTDIR", zshShimMarkerEnv, value)
	}
}

func TestZshRcShimEnvIdempotentForExistingShim(t *testing.T) {
	t.Setenv(zshShimOptOutEnv, "")
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, ".loom-shim"), []byte("/self/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=/tmp/home", "ZDOTDIR=" + shimDir, "PATH=/usr/bin"}
	stubSelfExecutable(t, "", errors.New("must not be called"))

	got := ZshRcShimEnv(env)
	if !reflect.DeepEqual(got, env) {
		t.Fatalf("ZshRcShimEnv() = %v, want unchanged %v", got, env)
	}
}

func TestZshRcShimEnvOptOut(t *testing.T) {
	home := t.TempDir()
	executable := writeStubExecutable(t, filepath.Join(home, "bin"))
	stubSelfExecutable(t, executable, nil)

	t.Run("input env", func(t *testing.T) {
		t.Setenv(zshShimOptOutEnv, "")
		env := []string{"HOME=" + home, "PATH=/usr/bin", zshShimOptOutEnv + "=YeS"}
		if got := ZshRcShimEnv(env); !reflect.DeepEqual(got, env) {
			t.Fatalf("ZshRcShimEnv() = %v, want unchanged %v", got, env)
		}
	})

	t.Run("process env", func(t *testing.T) {
		t.Setenv(zshShimOptOutEnv, "true")
		env := []string{"HOME=" + home, "PATH=/usr/bin", zshShimOptOutEnv + "=0"}
		if got := ZshRcShimEnv(env); !reflect.DeepEqual(got, env) {
			t.Fatalf("ZshRcShimEnv() = %v, want unchanged %v", got, env)
		}
	})
}

func TestZshRcShimEnvExecutableFailure(t *testing.T) {
	t.Setenv(zshShimOptOutEnv, "")
	stubSelfExecutable(t, "", errors.New("boom"))
	env := []string{"HOME=/tmp/home", "PATH=/usr/bin"}
	if got := ZshRcShimEnv(env); !reflect.DeepEqual(got, env) {
		t.Fatalf("ZshRcShimEnv() = %v, want unchanged %v", got, env)
	}
}

func TestZshRcShimEnvSkipsUnchangedRewrite(t *testing.T) {
	t.Setenv(zshShimOptOutEnv, "")
	home := t.TempDir()
	executable := writeStubExecutable(t, filepath.Join(home, "bin"))
	stubSelfExecutable(t, executable, nil)
	env := []string{"HOME=" + home, "PATH=/usr/bin", "LOOM_CONFIG_DIR=" + filepath.Join(home, "config")}

	first := ZshRcShimEnv(env)
	zshrc := filepath.Join(envValueExact(first, "ZDOTDIR"), ".zshrc")
	oldTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(zshrc, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	second := ZshRcShimEnv(env)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second env = %v, want %v", second, first)
	}
	info, err := os.Stat(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Fatalf("unchanged .zshrc mtime = %v, want %v", info.ModTime(), oldTime)
	}
}

func TestZshRcShimEnvWithZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not found on PATH")
	}
	t.Setenv(zshShimOptOutEnv, "")

	home := t.TempDir()
	selfDir := filepath.Join(home, "self'bin")
	executable := writeStubExecutable(t, selfDir)
	stubSelfExecutable(t, executable, nil)
	selfDir = resolvedPath(t, selfDir)
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "user-marker"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zprofile"), []byte("export MARKER=profile-ran\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zshrc := `export PATH="$HOME/fakebin:$PATH"
if [ -f "$ZDOTDIR/user-marker" ]; then
  export ZDOTDIR_RC=user-dir-resolved
fi
`
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(zshrc), 0o600); err != nil {
		t.Fatal(err)
	}

	env := ZshRcShimEnv([]string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
		"TERM=dumb",
		"LOOM_CONFIG_DIR=" + filepath.Join(home, "config"),
	})
	shimDir := envValueExact(env, "ZDOTDIR")

	login := runZshShimTest(t, zsh, env, "-lc")
	assertPinnedPath(t, login.path, selfDir)
	if login.marker != "profile-ran" {
		t.Fatalf("login MARKER = %q, want profile-ran", login.marker)
	}
	if login.zdotdir != shimDir {
		t.Fatalf("login ZDOTDIR = %q, want shim dir %q", login.zdotdir, shimDir)
	}

	interactive := runZshShimTest(t, zsh, env, "-ic")
	assertPinnedPath(t, interactive.path, selfDir)
	if !containsPathElement(interactive.path, fakeBin) {
		t.Fatalf("interactive PATH = %q, want user .zshrc entry %q", interactive.path, fakeBin)
	}
	if interactive.zdotdirRC != "user-dir-resolved" {
		t.Fatalf("ZDOTDIR_RC = %q, want user-dir-resolved", interactive.zdotdirRC)
	}
	if interactive.zdotdir != shimDir {
		t.Fatalf("interactive ZDOTDIR = %q, want shim dir %q", interactive.zdotdir, shimDir)
	}
}

type zshShimResult struct {
	path      string
	marker    string
	zdotdirRC string
	zdotdir   string
}

func runZshShimTest(t *testing.T, zsh string, env []string, mode string) zshShimResult {
	t.Helper()
	command := `echo "$PATH"; echo "${MARKER-}"; echo "${ZDOTDIR_RC-}"; echo "$ZDOTDIR"`
	cmd := exec.Command(zsh, mode, command)
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", zsh, mode, err, output)
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("%s %s output = %q, want four lines", zsh, mode, output)
	}
	return zshShimResult{path: lines[0], marker: lines[1], zdotdirRC: lines[2], zdotdir: lines[3]}
}

func assertPinnedPath(t *testing.T, path, selfDir string) {
	t.Helper()
	parts := filepath.SplitList(path)
	if len(parts) == 0 || parts[0] != selfDir {
		t.Fatalf("PATH = %q, want first element %q", path, selfDir)
	}
	count := 0
	for _, part := range parts {
		if part == selfDir {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("PATH = %q, want %q exactly once; got %d", path, selfDir, count)
	}
}

func containsPathElement(path, want string) bool {
	for _, part := range filepath.SplitList(path) {
		if part == want {
			return true
		}
	}
	return false
}

func writeStubExecutable(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "loom")
	if err := os.WriteFile(path, []byte("loom\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func assertSingleEnvKey(t *testing.T, env []string, key string) {
	t.Helper()
	count := 0
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && name == key {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%s occurs %d times in %v, want exactly once", key, count, env)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s content:\n%s\nwant:\n%s", path, got, want)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func wantZshShimContent(name, selfDir string) string {
	quotedDir := testShellSingleQuote(selfDir)
	quotedPathPrefix := testShellSingleQuote(selfDir + ":")
	return fmt.Sprintf(`# managed by loom — sources the user's real %s, then pins the loom that spawned this agent
__loom_user_zdotdir="${LOOM_SHIM_USER_ZDOTDIR:-$HOME}"
if [ -f "$__loom_user_zdotdir/%s" ]; then
  ZDOTDIR="$__loom_user_zdotdir" source "$__loom_user_zdotdir/%s"
fi
case ":$PATH:" in *":"%s":"*) path=("${(@)path:#%s}") ;; esac
export PATH=%s:"${PATH#%s}"
`, name, name, name, quotedDir, quotedDir, quotedDir, quotedPathPrefix)
}

func wantZshLogoutContent() string {
	return `# managed by loom — sources the user's real .zlogout
__loom_user_zdotdir="${LOOM_SHIM_USER_ZDOTDIR:-$HOME}"
if [ -f "$__loom_user_zdotdir/.zlogout" ]; then
  ZDOTDIR="$__loom_user_zdotdir" source "$__loom_user_zdotdir/.zlogout"
fi
`
}

func testShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
