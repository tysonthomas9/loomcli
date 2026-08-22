package cli

import (
	"os"
	"path/filepath"
	"strings"
)

var selfExecutable = os.Executable

// SelfBinPathEnv returns env with the running Loom binary's directory first
// on PATH, so subprocess login shells resolve loom back to the same build.
func SelfBinPathEnv(env []string) []string {
	executable, err := selfExecutable()
	if err != nil {
		return env
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return env
	}
	dir := filepath.Dir(executable)

	for i, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.EqualFold(key, "PATH") {
			continue
		}
		parts := filepath.SplitList(value)
		if len(parts) > 0 && parts[0] == dir {
			return env
		}
		updated := append([]string(nil), env...)
		if value == "" {
			updated[i] = key + "=" + dir
		} else {
			updated[i] = key + "=" + dir + string(os.PathListSeparator) + value
		}
		return updated
	}

	return append(append([]string(nil), env...), "PATH="+dir)
}
