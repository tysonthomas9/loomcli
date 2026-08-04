package sandbox

import (
	"strings"
	"testing"
)

// TestFlueLocalLauncherNeverPrintsRuntimeEnv guards the launcher temp file
// against ever dumping the runtime env (which carries LOOM_RUN_TOKEN) onto
// its stdio streams: the only stdout line is the terminal result frame.
func TestFlueLocalLauncherNeverPrintsRuntimeEnv(t *testing.T) {
	for _, banned := range []string{
		"LOOM_RUN_TOKEN",
		"JSON.stringify(process.env",
		"console.log(process.env",
		"console.error(process.env",
	} {
		if strings.Contains(flueLocalLauncher, banned) {
			t.Fatalf("flueLocalLauncher references %q; the launcher must never print the runtime env", banned)
		}
	}
}
