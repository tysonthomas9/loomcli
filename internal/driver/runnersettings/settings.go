// Package runnersettings owns process-local settings reads needed by the
// bundled task runner. It keeps the driver root independent of concrete local
// node and desktop settings stores.
package runnersettings

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

func RuntimeProvider(workspace string) string {
	provider, _ := localnodeconfig.RuntimeProvider(workspace)
	return strings.TrimSpace(provider)
}

func LocalTaskRunnerEnv(settingsDir string, existing []string) []string {
	settingsDir = strings.TrimSpace(settingsDir)
	if settingsDir == "" {
		return nil
	}
	settings, err := localsettings.Load(settingsDir)
	if err != nil {
		return nil
	}
	model := strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel)
	if model == "" || HasAny(existing, "LOOM_OPENCODE_MODEL") {
		return nil
	}
	return []string{"LOOM_OPENCODE_MODEL=" + model}
}

func HasAny(env []string, names ...string) bool {
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
