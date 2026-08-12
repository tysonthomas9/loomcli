package serveadapter

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

// LocalTaskRunnerEnvProvider projects only the optional non-secret model
// setting at execution time. Driver supplies its already-filtered environment,
// so this composition seam cannot reintroduce forge or control-plane secrets.
func LocalTaskRunnerEnvProvider(settingsDir string) func([]string) []string {
	settingsDir = strings.TrimSpace(settingsDir)
	if settingsDir == "" {
		return nil
	}
	return func(existing []string) []string {
		settings, err := localsettings.Load(settingsDir)
		if err != nil {
			return nil
		}
		model := strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel)
		if model == "" || envHasValue(existing, "LOOM_OPENCODE_MODEL") {
			return nil
		}
		return []string{"LOOM_OPENCODE_MODEL=" + model}
	}
}

func envHasValue(env []string, name string) bool {
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == name && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
