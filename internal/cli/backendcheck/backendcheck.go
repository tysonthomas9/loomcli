// Package backendcheck is the single seam between loom code and the
// in-tree harness CLI discovery primitive. It exists as its own package
// (rather than a var in internal/cli) to keep internal/cli's import
// fanout and file count under the project's per-package gates.
package backendcheck

import (
	"fmt"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// CheckBackend reports whether a named backend is installed and, when a
// version probe is registered, at what version. Registered Loom backends
// with health checks are authoritative; raw CLI names fall back to
// harness-wrapper PATH discovery.
//
// Production code reads CheckBackend; tests reassign it to inject a
// fake without spawning subprocesses or mutating PATH:
//
//	prev := backendcheck.CheckBackend
//	backendcheck.CheckBackend = func(string) (discovery.Info, error) { ... }
//	t.Cleanup(func() { backendcheck.CheckBackend = prev })
var CheckBackend = lookupBackend

func lookupBackend(name string) (discovery.Info, error) {
	if hs, ok := backends.CheckBackendHealth(name); ok {
		info := discovery.Info{
			Name:              name,
			Binary:            name,
			Installed:         hs.Installed,
			DetectedVersion:   hs.Version,
			VersionMatchesPin: true,
		}
		if !hs.Installed {
			if hs.Message != "" {
				info.InstallHint = hs.Message
			} else {
				info.InstallHint = fmt.Sprintf("%q not on PATH", name)
			}
		}
		return info, nil
	}
	return discovery.Lookup(name)
}
