// Package backendcheck is the single seam between loom code and the
// in-tree harness CLI discovery primitive. It exists as its own package
// (rather than a var in internal/cli) to keep internal/cli's import
// fanout and file count under the project's per-package gates.
package backendcheck

import (
	"os/exec"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/discovery"
)

// CheckBackend reports whether a named backend's CLI is installed on
// PATH and, when a version probe is registered, at what version.
//
// Production code reads CheckBackend; tests reassign it to inject a
// fake without spawning subprocesses or mutating PATH:
//
//	prev := backendcheck.CheckBackend
//	backendcheck.CheckBackend = func(string) (discovery.Info, error) { ... }
//	t.Cleanup(func() { backendcheck.CheckBackend = prev })
var CheckBackend = Lookup

// Lookup resolves built-in harness CLIs through harness-wrapper and also
// recognizes Loom external backend plugins named loom-backend-<backend>.
func Lookup(name string) (discovery.Info, error) {
	info, err := discovery.Lookup(name)
	if err != nil || info.Installed || name == "" || strings.HasPrefix(name, "loom-backend-") {
		return info, err
	}

	pluginBinary := "loom-backend-" + name
	path, lookErr := exec.LookPath(pluginBinary)
	if lookErr != nil {
		return info, nil
	}

	info.Binary = pluginBinary
	info.Path = path
	info.Installed = true
	info.InstallHint = ""
	return info, nil
}
