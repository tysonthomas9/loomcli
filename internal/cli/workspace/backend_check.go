package workspace

import (
	"fmt"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// checkBackend reports whether a named backend is installed and, when a
// version probe is registered, at what version. Registered Loom backends
// with health checks are authoritative; raw CLI names fall back to
// harness-wrapper PATH discovery.
//
// Tests may reassign the variable to inject a fake without spawning
// subprocesses or mutating PATH.
var checkBackend = lookupBackend

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
