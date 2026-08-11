package driver

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

func runtimeProvider(workspace string) string {
	provider, _ := bootstrap.RuntimeProvider(workspace)
	return strings.TrimSpace(provider)
}

func localTaskRunnerEnv(settingsDir string, existing []string) []string {
	settingsDir = strings.TrimSpace(settingsDir)
	if settingsDir == "" {
		return nil
	}
	settings, err := localsettings.Load(settingsDir)
	if err != nil {
		return nil
	}
	model := strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel)
	if model == "" || hasAny(existing, "LOOM_OPENCODE_MODEL") {
		return nil
	}
	return []string{"LOOM_OPENCODE_MODEL=" + model}
}

func hasAny(env []string, names ...string) bool {
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		for _, want := range names {
			if name == want {
				return true
			}
		}
	}
	return false
}
