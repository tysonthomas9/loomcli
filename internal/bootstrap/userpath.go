// Package bootstrap includes startup helpers for Loom processes.
//
// GUI-launched Loom processes can inherit launchd's bare PATH, which omits
// user-installed backend CLIs. This file collects well-known user bin
// directories so codex and claude can be found without executing a shell.
// Child backend processes inherit the augmented PATH because envfilter
// allowlists PATH.
package bootstrap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var ensureUserPATHOnce sync.Once

// EnsureUserPATH augments PATH once with existing, well-known user bin
// directories. GUI-launched Loom processes may inherit launchd's bare PATH;
// child backend processes inherit this value because envfilter allowlists PATH.
func EnsureUserPATH() {
	if runtime.GOOS == "windows" || os.Getenv("LOOM_SKIP_PATH_FIXUP") != "" {
		return
	}
	ensureUserPATHOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return
		}
		current := os.Getenv("PATH")
		merged := augmentedPATH(current, home)
		if merged != current {
			_ = os.Setenv("PATH", merged)
		}
	})
}

func augmentedPATH(current, home string) string {
	return mergePATH(current, existingDirs(candidateUserBinDirs(home)))
}

func candidateUserBinDirs(home string) []string {
	dirs := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, "go", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	matches, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin"))
	if err == nil {
		dirs = append(dirs, matches...)
	}
	return dirs
}

func existingDirs(dirs []string) []string {
	existing := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			existing = append(existing, dir)
		}
	}
	return existing
}

func mergePATH(current string, add []string) string {
	merged := make([]string, 0)
	seen := make(map[string]struct{})
	for _, entry := range filepath.SplitList(current) {
		if entry == "" {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		merged = append(merged, entry)
	}
	for _, entry := range add {
		if entry == "" || !filepath.IsAbs(entry) {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		merged = append(merged, entry)
	}
	return strings.Join(merged, string(os.PathListSeparator))
}
