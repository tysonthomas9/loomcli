package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	zshShimMarkerEnv = "LOOM_SHIM_USER_ZDOTDIR"
	zshShimOptOutEnv = "LOOM_NO_SHELL_SHIM"
)

var zshStartupFiles = []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"}

// ZshRcShimEnv installs a zsh startup-file shim that sources the user's real
// startup files before restoring the running Loom binary to the front of PATH.
func ZshRcShimEnv(env []string) []string {
	if runtime.GOOS == "windows" || envTruthy(env, zshShimOptOutEnv) || isTruthy(os.Getenv(zshShimOptOutEnv)) {
		return env
	}

	userZdotdir, zdotdirSet := lookupEnvExact(env, "ZDOTDIR")
	if !zdotdirSet {
		userZdotdir = envValueExact(env, "HOME")
	}
	if userZdotdir != "" && isZshShimDir(userZdotdir) {
		return env
	}

	executable, err := selfExecutable()
	if err != nil {
		return env
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return env
	}
	selfDir := filepath.Dir(executable)

	configDir := envValueExact(env, "LOOM_CONFIG_DIR")
	if configDir == "" {
		home := envValueExact(env, "HOME")
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return env
			}
		}
		configDir = filepath.Join(home, ".loom")
	}

	digest := sha256.Sum256([]byte(selfDir))
	shimDir := filepath.Join(configDir, "shell-shim", fmt.Sprintf("zsh-%x", digest[:4]))
	if err := materializeZshShim(shimDir, selfDir); err != nil {
		return env
	}

	updated := setEnvExact(env, "ZDOTDIR", shimDir)
	return setEnvExact(updated, zshShimMarkerEnv, userZdotdir)
}

func materializeZshShim(shimDir, selfDir string) error {
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(shimDir, 0o700); err != nil {
		return err
	}

	for _, name := range zshStartupFiles {
		if err := writeFileIfChanged(filepath.Join(shimDir, name), zshShimContent(name, selfDir), 0o600); err != nil {
			return err
		}
	}
	if err := writeFileIfChanged(filepath.Join(shimDir, ".zlogout"), zshLogoutContent(), 0o600); err != nil {
		return err
	}
	return writeFileIfChanged(filepath.Join(shimDir, ".loom-shim"), selfDir+"\n", 0o600)
}

func zshShimContent(name, selfDir string) string {
	quotedDir := shellSingleQuote(selfDir)
	quotedPathPrefix := shellSingleQuote(selfDir + ":")
	return fmt.Sprintf(`# managed by loom — sources the user's real %s, then pins the loom that spawned this agent
__loom_user_zdotdir="${LOOM_SHIM_USER_ZDOTDIR:-$HOME}"
if [ -f "$__loom_user_zdotdir/%s" ]; then
  ZDOTDIR="$__loom_user_zdotdir" source "$__loom_user_zdotdir/%s"
fi
case ":$PATH:" in *":"%s":"*) path=("${(@)path:#%s}") ;; esac
export PATH=%s:"${PATH#%s}"
`, name, name, name, quotedDir, quotedDir, quotedDir, quotedPathPrefix)
}

func zshLogoutContent() string {
	return `# managed by loom — sources the user's real .zlogout
__loom_user_zdotdir="${LOOM_SHIM_USER_ZDOTDIR:-$HOME}"
if [ -f "$__loom_user_zdotdir/.zlogout" ]; then
  ZDOTDIR="$__loom_user_zdotdir" source "$__loom_user_zdotdir/.zlogout"
fi
`
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeFileIfChanged(path, content string, mode os.FileMode) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return os.Chmod(path, mode)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func isZshShimDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".loom-shim"))
	return err == nil && !info.IsDir()
}

func envTruthy(env []string, key string) bool {
	value, ok := lookupEnvExact(env, key)
	return ok && isTruthy(value)
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func envValueExact(env []string, key string) string {
	value, _ := lookupEnvExact(env, key)
	return value
}

func lookupEnvExact(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, ok := strings.Cut(env[i], "=")
		if ok && name == key {
			return value, true
		}
	}
	return "", false
}

func setEnvExact(env []string, key, value string) []string {
	updated := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name != key {
			updated = append(updated, entry)
			continue
		}
		if !replaced {
			updated = append(updated, key+"="+value)
			replaced = true
		}
	}
	if !replaced {
		updated = append(updated, key+"="+value)
	}
	return updated
}
